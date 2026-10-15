package config

import (
	"strings"
	"testing"
	"time"
)

func TestLocalDefaults(t *testing.T) {
	cfg, err := load(ProcessAPI, mapEnv{"APP_ENV": "local"}.Lookup)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.Host != "127.0.0.1" || cfg.DB.Database != "laravel" || cfg.DB.Username != "root" {
		t.Fatalf("db defaults %+v", cfg.DB)
	}
	if cfg.Redis.DB != 1 || cfg.Redis.Prefix != "laravel_cache_" {
		t.Fatalf("redis %+v", cfg.Redis)
	}
	if cfg.Audit.DB != 2 || cfg.Audit.Host != cfg.Prices.Host {
		t.Fatalf("audit redis %+v", cfg.Audit)
	}
	if cfg.OrderPoolingMaxWait != 30*time.Second || cfg.OrderPoolingPoll != 100*time.Millisecond || cfg.OrderPoolingMaxWaiters != 64 {
		t.Fatalf("pooling %s %s %d", cfg.OrderPoolingMaxWait, cfg.OrderPoolingPoll, cfg.OrderPoolingMaxWaiters)
	}
	if !cfg.CallbackInsecureSkipVerify {
		t.Fatal("callback tls skip should default to on")
	}
	if cfg.HTTPAddr != "127.0.0.1:8080" {
		t.Fatalf("addr %s", cfg.HTTPAddr)
	}
	if cfg.Timezone != "UTC" {
		t.Fatalf("timezone %s", cfg.Timezone)
	}
}

func TestProductionRequiresSecrets(t *testing.T) {
	_, err := load(ProcessAPI, mapEnv{}.Lookup)
	if err == nil || !strings.Contains(err.Error(), "DB_PASSWORD") || !strings.Contains(err.Error(), "CASCADE_PAYMENT_DETAIL_ID") {
		t.Fatalf("got %v", err)
	}
}

func TestProductionAllowsEmptyPasswordWhenSet(t *testing.T) {
	env := mapEnv{
		"DB_HOST":                   "db",
		"DB_PORT":                   "3306",
		"DB_DATABASE":               "app",
		"DB_USERNAME":               "app",
		"DB_PASSWORD":               "",
		"REDIS_HOST":                "redis",
		"REDIS_PORT":                "6379",
		"PRICES_REDIS_HOST":         "prices",
		"PRICES_REDIS_PORT":         "6379",
		"CASCADE_PAYMENT_DETAIL_ID": "1",
	}
	cfg, err := load(ProcessAPI, env.Lookup)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.Password != "" || cfg.CascadePaymentDetailID != "1" {
		t.Fatalf("%+v", cfg)
	}
}

func TestRelayRequiresKafka(t *testing.T) {
	_, err := load(ProcessRelay, mapEnv{"APP_ENV": "local"}.Lookup)
	if err == nil || !strings.Contains(err.Error(), "KAFKA_BROKERS") {
		t.Fatalf("got %v", err)
	}
}

func TestBoolParsing(t *testing.T) {
	cfg, err := load(ProcessScheduler, mapEnv{
		"APP_ENV":         "local",
		"FEATURE_CASCADE": "yes",
	}.Lookup)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.FeatureCascade {
		t.Fatal("expected cascade on")
	}
}

func TestSlug(t *testing.T) {
	if slug("demo.local") != "demo_local" {
		t.Fatalf("%s", slug("demo.local"))
	}
}

type mapEnv map[string]string

func (m mapEnv) Lookup(name string) (string, bool) {
	value, ok := m[name]
	return value, ok
}
