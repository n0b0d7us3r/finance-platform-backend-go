package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"finance-platform/go/internal/config"
	"finance-platform/go/internal/schema"
	"finance-platform/go/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load(config.ProcessAPI)
	if err != nil {
		logger.Error("config", "err", err.Error())
		os.Exit(1)
	}
	db, err := store.OpenMySQL(cfg.DB.Host, cfg.DB.Port, cfg.DB.Username, cfg.DB.Password, cfg.DB.Database, cfg.Timezone)
	if err != nil {
		logger.Error("mysql", "err", err.Error())
		os.Exit(1)
	}
	defer db.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := db.PingContext(ctx); err != nil {
		logger.Error("mysql", "err", err.Error())
		os.Exit(1)
	}
	if err := schema.Seed(ctx, db); err != nil {
		logger.Error("seed", "err", err.Error())
		os.Exit(1)
	}
	logger.Info("stand seed is current")
}
