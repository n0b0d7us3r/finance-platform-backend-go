package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Process is one of the four binaries.
type Process string

const (
	ProcessAPI       Process = "api"
	ProcessRelay     Process = "relay"
	ProcessWorker    Process = "worker"
	ProcessScheduler Process = "scheduler"
)

// Config is the single settings object. Domain code does not read the environment.
type Config struct {
	AppEnv   string
	AppName  string
	Timezone string

	HTTPAddr string

	DB     DB
	Redis  Redis
	Prices Redis
	Audit  Redis
	Kafka  Kafka

	OrderPoolingMaxWait    time.Duration
	OrderPoolingPoll       time.Duration
	OrderPoolingMaxWaiters int

	FeatureCascade             bool
	CascadePaymentDetailID     string
	CallbackInsecureSkipVerify bool

	BotToken    string
	ReceiptsDir string

	DepositToken   string
	WithdrawToken  string
	WithdrawalHost string

	ImpersonationKey string
	MerchantAppURL   string
	TraderAppURL     string
	SupportAppURL    string
	LeaderAppURL     string

	ScheduleAddr  string
	ScheduleToken string
	TronAPIKey    string
}

// DB is the MySQL connection.
type DB struct {
	Host     string
	Port     string
	Database string
	Username string
	Password string
}

// Redis is the access-token cache (REDIS_CACHE_DB).
type Redis struct {
	Host     string
	Port     string
	Username string
	Password string
	DB       int
	Prefix   string
}

// Kafka lists brokers. Empty is valid for api and scheduler.
type Kafka struct {
	Brokers string
}

type lookup func(string) (string, bool)

// Load reads the process environment.
func Load(process Process) (Config, error) {
	return load(process, os.LookupEnv)
}

func load(process Process, getenv lookup) (Config, error) {
	appEnv := orDefault(getenv, "APP_ENV", "production")
	production := appEnv == "production"
	appName := orDefault(getenv, "APP_NAME", "laravel")

	var missing []string
	require := func(name string) string {
		value, ok := getenv(name)
		if !ok {
			missing = append(missing, name)
			return ""
		}
		return value
	}

	cfg := Config{
		AppEnv:   appEnv,
		AppName:  appName,
		Timezone: orDefault(getenv, "APP_TIMEZONE", "UTC"),
		HTTPAddr: orDefault(getenv, "HTTP_ADDR", "127.0.0.1:8080"),
	}

	if production {
		cfg.DB.Host = require("DB_HOST")
		cfg.DB.Port = require("DB_PORT")
		cfg.DB.Database = require("DB_DATABASE")
		cfg.DB.Username = require("DB_USERNAME")
		cfg.DB.Password = require("DB_PASSWORD")
		cfg.Redis.Host = require("REDIS_HOST")
		cfg.Redis.Port = require("REDIS_PORT")
		cfg.Prices.Host = require("PRICES_REDIS_HOST")
		cfg.Prices.Port = require("PRICES_REDIS_PORT")
		cfg.CascadePaymentDetailID = require("CASCADE_PAYMENT_DETAIL_ID")
	} else {
		cfg.DB.Host = orDefault(getenv, "DB_HOST", "127.0.0.1")
		cfg.DB.Port = orDefault(getenv, "DB_PORT", "3306")
		cfg.DB.Database = orDefault(getenv, "DB_DATABASE", "laravel")
		cfg.DB.Username = orDefault(getenv, "DB_USERNAME", "root")
		cfg.DB.Password = orDefault(getenv, "DB_PASSWORD", "")
		cfg.Redis.Host = orDefault(getenv, "REDIS_HOST", "127.0.0.1")
		cfg.Redis.Port = orDefault(getenv, "REDIS_PORT", "6379")
		cfg.Prices.Host = orDefault(getenv, "PRICES_REDIS_HOST", "127.0.0.1")
		cfg.Prices.Port = orDefault(getenv, "PRICES_REDIS_PORT", "6379")
		cfg.CascadePaymentDetailID = orDefault(getenv, "CASCADE_PAYMENT_DETAIL_ID", "")
	}

	cfg.Redis.Username = orDefault(getenv, "REDIS_USERNAME", "")
	cfg.Redis.Password = orDefault(getenv, "REDIS_PASSWORD", "")

	cacheDB, err := parseIntDefault(getenv, "REDIS_CACHE_DB", 1)
	if err != nil {
		return Config{}, fmt.Errorf("REDIS_CACHE_DB: %w", err)
	}
	cfg.Redis.DB = cacheDB

	cfg.Prices.Username = orDefault(getenv, "PRICES_REDIS_USERNAME", "")
	cfg.Prices.Password = orDefault(getenv, "PRICES_REDIS_PASSWORD", "")
	pricesDB, err := parseIntDefault(getenv, "PRICES_REDIS_DB", 0)
	if err != nil {
		return Config{}, fmt.Errorf("PRICES_REDIS_DB: %w", err)
	}
	cfg.Prices.DB = pricesDB

	cfg.Audit = cfg.Prices
	auditDB, err := parseIntDefault(getenv, "AUDIT_REDIS_DB", 2)
	if err != nil {
		return Config{}, fmt.Errorf("AUDIT_REDIS_DB: %w", err)
	}
	cfg.Audit.DB = auditDB

	prefix, ok := getenv("CACHE_PREFIX")
	if !ok || prefix == "" {
		prefix = slug(appName) + "_cache_"
	}
	cfg.Redis.Prefix = prefix

	waitMS, err := parseIntDefault(getenv, "ORDER_POOLING_MAX_WAIT_TIME", 30000)
	if err != nil {
		return Config{}, fmt.Errorf("ORDER_POOLING_MAX_WAIT_TIME: %w", err)
	}
	pollMS, err := parseIntDefault(getenv, "ORDER_POOLING_POLL_INTERVAL", 100)
	if err != nil {
		return Config{}, fmt.Errorf("ORDER_POOLING_POLL_INTERVAL: %w", err)
	}
	cfg.OrderPoolingMaxWait = time.Duration(waitMS) * time.Millisecond
	cfg.OrderPoolingPoll = time.Duration(pollMS) * time.Millisecond
	waiters, err := parseIntDefault(getenv, "ORDER_POOLING_MAX_WAITERS", 64)
	if err != nil {
		return Config{}, fmt.Errorf("ORDER_POOLING_MAX_WAITERS: %w", err)
	}
	if waiters <= 0 {
		waiters = 64
	}
	cfg.OrderPoolingMaxWaiters = waiters

	cascade, err := parseBool(getenv, "FEATURE_CASCADE", false)
	if err != nil {
		return Config{}, fmt.Errorf("FEATURE_CASCADE: %w", err)
	}
	cfg.FeatureCascade = cascade

	skipVerify, err := parseBool(getenv, "CALLBACK_INSECURE_SKIP_VERIFY", true)
	if err != nil {
		return Config{}, fmt.Errorf("CALLBACK_INSECURE_SKIP_VERIFY: %w", err)
	}
	cfg.CallbackInsecureSkipVerify = skipVerify

	cfg.BotToken = strings.TrimSpace(orDefault(getenv, "API_BOT_TOKEN", ""))
	cfg.ReceiptsDir = orDefault(getenv, "RECEIPTS_DIR", "/tmp/receipts")
	cfg.DepositToken = strings.TrimSpace(orDefault(getenv, "API_DEPOSIT_TOKEN", ""))
	cfg.WithdrawToken = strings.TrimSpace(orDefault(getenv, "API_WITHDRAW_TOKEN", ""))
	cfg.WithdrawalHost = strings.TrimSpace(orDefault(getenv, "WITHDRAWAL_SERVICE_HOST", ""))
	cfg.ImpersonationKey = orDefault(getenv, "IMPERSONATION_KEY", "local-impersonation-key")
	cfg.MerchantAppURL = orDefault(getenv, "MERCHANT_APP_URL", "http://localhost:3001")
	cfg.TraderAppURL = orDefault(getenv, "TRADER_APP_URL", "http://localhost:3002")
	cfg.SupportAppURL = orDefault(getenv, "SUPPORT_APP_URL", "http://localhost:3003")
	cfg.LeaderAppURL = orDefault(getenv, "LEADER_APP_URL", "http://localhost:3004")
	cfg.ScheduleAddr = orDefault(getenv, "SCHEDULE_ADDR", "127.0.0.1:8090")
	cfg.ScheduleToken = strings.TrimSpace(orDefault(getenv, "SCHEDULE_TOKEN", ""))
	cfg.TronAPIKey = strings.TrimSpace(orDefault(getenv, "TRONGRID_API_KEY", ""))

	brokers, brokersSet := getenv("KAFKA_BROKERS")
	cfg.Kafka.Brokers = brokers
	if process == ProcessRelay || process == ProcessWorker {
		if !brokersSet || strings.TrimSpace(brokers) == "" {
			missing = append(missing, "KAFKA_BROKERS")
		}
	}

	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required env: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}

func orDefault(getenv lookup, name, fallback string) string {
	value, ok := getenv(name)
	if !ok {
		return fallback
	}
	return value
}

func parseIntDefault(getenv lookup, name string, fallback int) (int, error) {
	raw, ok := getenv(name)
	if !ok || raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	return parsed, nil
}

func parseBool(getenv lookup, name string, fallback bool) (bool, error) {
	raw, ok := getenv(name)
	if !ok || raw == "" {
		return fallback, nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q", raw)
	}
}

func slug(name string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		if !prevUnderscore && b.Len() > 0 {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}
