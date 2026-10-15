package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"finance-platform/go/internal/bus"
	"finance-platform/go/internal/config"
	"finance-platform/go/internal/event"
	"finance-platform/go/internal/flow"
	"finance-platform/go/internal/outbox"
	"finance-platform/go/internal/store"
	"finance-platform/go/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load(config.ProcessWorker)
	if err != nil {
		logger.Error("config", "err", err.Error())
		os.Exit(1)
	}
	brokers := bus.Brokers(cfg.Kafka.Brokers)
	if len(brokers) == 0 {
		logger.Error("config", "err", "KAFKA_BROKERS is empty")
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
	defer mysqlDB.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := outbox.Ensure(ctx, mysqlDB); err != nil {
		logger.Error("outbox", "err", err.Error())
		os.Exit(1)
	}
	if err := bus.EnsureTopics(ctx, brokers, event.Topics()); err != nil {
		logger.Error("topics", "err", err.Error())
		os.Exit(1)
	}
	logger.Info("worker started")
	err = (&worker.Worker{
		DB:       mysqlDB,
		Orders:   &flow.Service{DB: mysqlDB, Location: loc},
		Log:      logger,
		Brokers:  brokers,
		Insecure: cfg.CallbackInsecureSkipVerify,
	}).Run(ctx)
	if err != nil {
		logger.Error("worker", "err", err.Error())
		os.Exit(1)
	}
}
