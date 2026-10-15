package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"finance-platform/go/internal/store"

	goredis "github.com/redis/go-redis/v9"
)

// DBUsers reads users.api_access_token.
type DBUsers struct {
	DB *sql.DB
}

// FindUserIDByAccessToken implements Users.
func (d DBUsers) FindUserIDByAccessToken(ctx context.Context, token string) (int64, error) {
	return store.FindUserIDByAccessToken(ctx, d.DB, token)
}

// RedisCache is the access-token cache (REDIS_CACHE_DB).
type RedisCache struct {
	Client *goredis.Client
}

// Get implements Cache.
func (r RedisCache) Get(ctx context.Context, key string) (string, error) {
	value, err := r.Client.Get(ctx, key).Result()
	if errors.Is(err, goredis.Nil) {
		return "", ErrMiss
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

// Set implements Cache.
func (r RedisCache) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	return r.Client.Set(ctx, key, value, ttl).Err()
}
