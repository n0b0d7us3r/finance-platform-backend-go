package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"finance-platform/go/internal/auditlog"
	"finance-platform/go/internal/auth"
	"finance-platform/go/internal/cascade"
	"finance-platform/go/internal/config"
	"finance-platform/go/internal/flow"
	"finance-platform/go/internal/httpapi"
	"finance-platform/go/internal/impersonate"
	"finance-platform/go/internal/market"
	"finance-platform/go/internal/outbox"
	"finance-platform/go/internal/store"

	goredis "github.com/redis/go-redis/v9"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load(config.ProcessAPI)
	if err != nil {
		logger.Error("config", "err", err.Error())
		os.Exit(1)
	}

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		logger.Error("timezone", "err", err.Error())
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

	redisClient := openRedis(cfg.Redis)
	defer func() {
		if err := redisClient.Close(); err != nil {
			logger.Error("redis close", "err", err.Error())
		}
	}()

	priceClient := openRedis(cfg.Prices)
	defer func() {
		if err := priceClient.Close(); err != nil {
			logger.Error("prices redis close", "err", err.Error())
		}
	}()
	auditClient := openRedis(cfg.Audit)
	defer func() {
		if err := auditClient.Close(); err != nil {
			logger.Error("audit redis close", "err", err.Error())
		}
	}()

	if err := store.EnsureUserRoles(context.Background(), mysqlDB); err != nil {
		logger.Error("user roles", "err", err.Error())
		os.Exit(1)
	}
	if err := outbox.Ensure(context.Background(), mysqlDB); err != nil {
		logger.Error("outbox", "err", err.Error())
		os.Exit(1)
	}
	if err := cascade.RequireDetail(context.Background(), mysqlDB, cfg.CascadePaymentDetailID, cfg.FeatureCascade); err != nil {
		logger.Error("cascade detail", "err", err.Error())
		os.Exit(1)
	}

	cache := auth.RedisCache{Client: redisClient}
	handler := httpapi.NewHandler(httpapi.Deps{
		Tokens: auth.CachedTokens{
			Users:  auth.DBUsers{DB: mysqlDB},
			Cache:  cache,
			Prefix: cfg.Redis.Prefix,
		},
		MySQL:                  mysqlPinger{DB: mysqlDB},
		Redis:                  redisPinger{Client: redisClient},
		PriceRedis:             redisPinger{Client: priceClient},
		DB:                     httpapi.DB{SQL: mysqlDB},
		Prices:                 httpapi.Prices{Cache: cache, Prefix: cfg.Redis.Prefix},
		Quotes:                 market.RedisBook{Client: priceClient},
		AppEnv:                 cfg.AppEnv,
		Location:               loc,
		MaxWait:                cfg.OrderPoolingMaxWait,
		Poll:                   cfg.OrderPoolingPoll,
		Gate:                   flow.NewGate(cfg.OrderPoolingMaxWaiters),
		Holds:                  flow.RedisHold{Client: redisClient, Prefix: cfg.Redis.Prefix},
		BotToken:               cfg.BotToken,
		DepositToken:           cfg.DepositToken,
		WithdrawToken:          cfg.WithdrawToken,
		LocalWithdraw:          cfg.WithdrawalHost == "",
		ReceiptsDir:            cfg.ReceiptsDir,
		FeatureCascade:         cfg.FeatureCascade,
		CascadePaymentDetailID: cfg.CascadePaymentDetailID,
		Audit:                  auditlog.RedisLog{Client: auditClient},
		Sessions: &impersonate.Service{
			DB:          mysqlDB,
			Key:         []byte(cfg.ImpersonationKey),
			Now:         time.Now,
			MerchantURL: cfg.MerchantAppURL,
			TraderURL:   cfg.TraderAppURL,
			SupportURL:  cfg.SupportAppURL,
			LeaderURL:   cfg.LeaderAppURL,
		},
	})

	writeTimeout := cfg.OrderPoolingMaxWait + 30*time.Second
	if writeTimeout < 60*time.Second {
		writeTimeout = 60 * time.Second
	}
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown", "err", err.Error())
		}
	}()

	logger.Info("api listening", "addr", cfg.HTTPAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("listen", "err", err.Error())
		os.Exit(1)
	}
}

func openRedis(cfg config.Redis) *goredis.Client {
	return goredis.NewClient(&goredis.Options{
		Addr:     net.JoinHostPort(cfg.Host, cfg.Port),
		Username: cfg.Username,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
}

type mysqlPinger struct {
	DB *sql.DB
}

func (p mysqlPinger) Ping(ctx context.Context) error {
	return p.DB.PingContext(ctx)
}

type redisPinger struct {
	Client *goredis.Client
}

func (p redisPinger) Ping(ctx context.Context) error {
	return p.Client.Ping(ctx).Err()
}
