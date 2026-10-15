package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"finance-platform/go/internal/store"
)

const tokenTTL = 24 * time.Hour

// Resolver turns an Access-Token into a user id.
type Resolver interface {
	Resolve(ctx context.Context, token string) (int64, bool, error)
}

// Cache is the 24h Access-Token cache. Get returns ErrMiss when the key is absent.
type Cache interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
}

// ErrMiss is a cache miss.
var ErrMiss = errors.New("cache miss")

// Users looks up api_access_token.
type Users interface {
	FindUserIDByAccessToken(ctx context.Context, token string) (int64, error)
}

// CachedTokens remembers both hits and misses for tokenTTL.
// The Redis key is a hash of the token, so the secret is not the key.
type CachedTokens struct {
	Users  Users
	Cache  Cache
	Prefix string
}

// Resolve implements Resolver.
func (c CachedTokens) Resolve(ctx context.Context, token string) (int64, bool, error) {
	if token == "" {
		return 0, false, nil
	}
	key := c.Prefix + "go:api-access-token:" + hashToken(token)
	raw, err := c.Cache.Get(ctx, key)
	if err == nil {
		if raw == "-" {
			return 0, false, nil
		}
		id, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			return 0, false, parseErr
		}
		return id, true, nil
	}
	if !errors.Is(err, ErrMiss) {
		return 0, false, err
	}

	id, err := c.Users.FindUserIDByAccessToken(ctx, token)
	if errors.Is(err, store.ErrNotFound) {
		if setErr := c.Cache.Set(ctx, key, "-", tokenTTL); setErr != nil {
			return 0, false, setErr
		}
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if setErr := c.Cache.Set(ctx, key, strconv.FormatInt(id, 10), tokenTTL); setErr != nil {
		return 0, false, setErr
	}
	return id, true, nil
}

// ForgetAccessToken marks a replaced cabinet token as a miss for the rest of the cache TTL.
func ForgetAccessToken(ctx context.Context, cache Cache, prefix, token string) error {
	if cache == nil || token == "" {
		return nil
	}
	return cache.Set(ctx, prefix+"go:api-access-token:"+hashToken(token), "-", tokenTTL)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
