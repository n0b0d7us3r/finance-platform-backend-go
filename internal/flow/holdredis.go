package flow

import (
	"context"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// RedisHold stores wait claims in the token-cache Redis.
type RedisHold struct {
	Client *goredis.Client
	Prefix string
}

// Try claims key until ttl. false means another create already holds it.
func (h RedisHold) Try(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		ttl = time.Second
	}
	return h.Client.SetNX(ctx, h.Prefix+"order:wait:"+key, "waiting", ttl).Result()
}

// Drop releases a claim this process took.
func (h RedisHold) Drop(ctx context.Context, key string) error {
	return h.Client.Del(ctx, h.Prefix+"order:wait:"+key).Err()
}
