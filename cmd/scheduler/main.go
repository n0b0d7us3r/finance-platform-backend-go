package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"finance-platform/go/internal/config"
	"finance-platform/go/internal/flow"
	"finance-platform/go/internal/market"
	"finance-platform/go/internal/outbox"
	"finance-platform/go/internal/schedule"
	"finance-platform/go/internal/store"

	goredis "github.com/redis/go-redis/v9"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load(config.ProcessScheduler)
	if err != nil {
		logger.Error("config", "err", err.Error())
		os.Exit(1)
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		logger.Error("timezone", "err", err.Error())
		os.Exit(1)
	}

	prices := goredis.NewClient(&goredis.Options{
		Addr:     net.JoinHostPort(cfg.Prices.Host, cfg.Prices.Port),
		Username: cfg.Prices.Username,
		Password: cfg.Prices.Password,
		DB:       cfg.Prices.DB,
	})
	defer func() {
		if err := prices.Close(); err != nil {
			logger.Error("prices redis close", "err", err.Error())
		}
	}()
	cache := goredis.NewClient(&goredis.Options{
		Addr:     net.JoinHostPort(cfg.Redis.Host, cfg.Redis.Port),
		Username: cfg.Redis.Username,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	defer func() {
		if err := cache.Close(); err != nil {
			logger.Error("cache redis close", "err", err.Error())
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := prices.Ping(ctx).Err(); err != nil {
		logger.Error("prices redis", "err", err.Error())
		os.Exit(1)
	}
	if err := cache.Ping(ctx).Err(); err != nil {
		logger.Error("cache redis", "err", err.Error())
		os.Exit(1)
	}

	mysqlDB, err := store.OpenMySQL(cfg.DB.Host, cfg.DB.Port, cfg.DB.Username, cfg.DB.Password, cfg.DB.Database, cfg.Timezone)
	if err != nil {
		logger.Error("mysql", "err", err.Error())
		os.Exit(1)
	}
	defer func() {
		if err := mysqlDB.Close(); err != nil {
			logger.Error("mysql close", "err", err.Error())
		}
	}()
	if err := mysqlDB.PingContext(ctx); err != nil {
		logger.Error("mysql", "err", err.Error())
		os.Exit(1)
	}
	if err := outbox.Ensure(ctx, mysqlDB); err != nil {
		logger.Error("outbox", "err", err.Error())
		os.Exit(1)
	}

	orders := &flow.Service{DB: mysqlDB, Location: loc}
	jobs := schedule.Jobs{
		DB:       mysqlDB,
		Orders:   orders,
		Location: loc,
		Methods: schedule.BybitMethods{
			Client: cache,
			Prefix: cfg.Redis.Prefix,
		},
		Deposits: schedule.Deposits{Key: cfg.TronAPIKey, Orders: orders},
	}

	if cfg.ScheduleToken != "" {
		go serveSchedule(ctx, logger, cfg.ScheduleAddr, cfg.ScheduleToken, jobs)
	}

	book := market.RedisBook{Client: prices}
	feed := market.NewClient()
	logger.Info("price feed started", "every", market.RefreshEvery.String())
	refresh(ctx, logger, feed, book)
	go pricesLoop(ctx, logger, feed, book)

	logger.Info("scheduler started")
	runClock(ctx, logger, jobs, loc)
}

func pricesLoop(ctx context.Context, logger *slog.Logger, feed *market.Client, book market.Book) {
	ticker := time.NewTicker(market.RefreshEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh(ctx, logger, feed, book)
		}
	}
}

func runClock(ctx context.Context, logger *slog.Logger, jobs schedule.Jobs, loc *time.Location) {
	run(ctx, logger, jobs, schedule.EveryMinute)
	var lastDay string
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().In(loc)
			run(ctx, logger, jobs, schedule.EveryMinute)
			if now.Minute()%5 == 0 {
				run(ctx, logger, jobs, schedule.EveryFive)
			}
			if now.Minute()%30 == 0 {
				run(ctx, logger, jobs, schedule.EveryHalfHour)
			}
			if now.Minute() == 0 {
				run(ctx, logger, jobs, schedule.EveryHour)
			}
			day := now.Format("2006-01-02")
			if schedule.AtMidnight(now) && lastDay != day {
				run(ctx, logger, jobs, schedule.EveryDay)
				lastDay = day
			}
		}
	}
}

func run(ctx context.Context, logger *slog.Logger, jobs schedule.Jobs, every time.Duration) {
	result, err := jobs.Run(ctx, every)
	if err != nil {
		logger.Error("schedule", "every", every.String(), "err", err.Error())
		return
	}
	logger.Info("schedule", "every", every.String(),
		"orders", result.Orders,
		"holds", result.Holds,
		"invoices", result.Invoices,
		"manual", result.Manual,
		"sessions", result.Sessions,
		"deposits", result.Deposits,
		"inactive", result.Inactive,
		"limits", result.Limits,
		"sms", result.Sms,
		"statistics", result.Statistics,
		"methods", result.MethodsSaved,
	)
}

func serveSchedule(ctx context.Context, logger *slog.Logger, addr, token string, jobs schedule.Jobs) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /run", func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Schedule-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		type step struct {
			Every   string          `json:"every"`
			Result  schedule.Result `json:"result"`
			Warning string          `json:"warning,omitempty"`
		}
		steps := make([]step, 0, 5)
		failed := false
		for _, every := range []time.Duration{schedule.EveryMinute, schedule.EveryFive, schedule.EveryHalfHour, schedule.EveryHour, schedule.EveryDay} {
			result, err := jobs.Run(r.Context(), every)
			item := step{Every: every.String(), Result: result}
			if err != nil && every == schedule.EveryHour {
				item.Warning = "payment methods unavailable"
				logger.Warn("payment methods", "err", err.Error())
				err = nil
			}
			if err != nil {
				item.Warning = "failed"
				logger.Error("schedule run", "every", every.String(), "err", err.Error())
				failed = true
			}
			steps = append(steps, item)
		}
		w.Header().Set("Content-Type", "application/json")
		if failed {
			w.WriteHeader(http.StatusInternalServerError)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": !failed, "steps": steps})
	})
	server := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	logger.Info("schedule run listening", "addr", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("schedule run", "err", err.Error())
	}
}

func refresh(ctx context.Context, logger *slog.Logger, feed *market.Client, book market.Book) {
	result, err := feed.Load(ctx, book)
	if err != nil {
		logger.Error("price feed", "err", err.Error())
		return
	}
	logger.Info("price feed", "saved", result.Saved, "kept", result.Kept, "skipped", result.Skipped, "failed", result.Failed)
	for _, item := range result.Errors {
		logger.Warn("price source", "currency", item.Currency, "market", item.Market, "err", item.Err)
	}
}
