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
	"finance-platform/go/internal/outbox"
	"finance-platform/go/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load(config.ProcessRelay)
	if err != nil {
		logger.Error("config", "err", err.Error())
		os.Exit(1)
	}
	brokers := bus.Brokers(cfg.Kafka.Brokers)
	if len(brokers) == 0 {
		logger.Error("config", "err", "KAFKA_BROKERS is empty")
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
	publisher := bus.NewPublisher(brokers)
	defer publisher.Close()
	logger.Info("relay started")
	for {
		if ctx.Err() != nil {
			return
		}
		n, err := outbox.Claim(ctx, mysqlDB, time.Now(), func(topic, key string, payload []byte) error {
			return publisher.Publish(ctx, topic, key, payload)
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Error("publish", "err", err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		if n == 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
}
