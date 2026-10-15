package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"finance-platform/go/internal/store"
)

func TestResolveCachesHitAndMiss(t *testing.T) {
	users := &fakeUsers{id: 15}
	cache := &fakeCache{values: map[string]string{}}
	resolver := CachedTokens{Users: users, Cache: cache, Prefix: "laravel_cache_"}

	id, ok, err := resolver.Resolve(context.Background(), "token-a")
	if err != nil || !ok || id != 15 {
		t.Fatalf("id %d ok %v err %v", id, ok, err)
	}
	if users.calls != 1 {
		t.Fatalf("calls %d", users.calls)
	}

	id, ok, err = resolver.Resolve(context.Background(), "token-a")
	if err != nil || !ok || id != 15 || users.calls != 1 {
		t.Fatalf("cached id %d ok %v calls %d err %v", id, ok, users.calls, err)
	}

	users.missing = true
	_, ok, err = resolver.Resolve(context.Background(), "missing")
	if err != nil || ok {
		t.Fatalf("miss ok %v err %v", ok, err)
	}
	_, ok, err = resolver.Resolve(context.Background(), "missing")
	if err != nil || ok || users.calls != 2 {
		t.Fatalf("cached miss ok %v calls %d err %v", ok, users.calls, err)
	}
}

func TestEmptyTokenDoesNotTouchCache(t *testing.T) {
	resolver := CachedTokens{Users: &fakeUsers{}, Cache: &fakeCache{values: map[string]string{}}}
	_, ok, err := resolver.Resolve(context.Background(), "")
	if err != nil || ok {
		t.Fatalf("ok %v err %v", ok, err)
	}
}

type fakeUsers struct {
	id      int64
	missing bool
	calls   int
}

func (f *fakeUsers) FindUserIDByAccessToken(context.Context, string) (int64, error) {
	f.calls++
	if f.missing {
		return 0, store.ErrNotFound
	}
	return f.id, nil
}

type fakeCache struct {
	values map[string]string
}

func (f *fakeCache) Get(_ context.Context, key string) (string, error) {
	value, ok := f.values[key]
	if !ok {
		return "", ErrMiss
	}
	return value, nil
}

func (f *fakeCache) Set(_ context.Context, key, value string, ttl time.Duration) error {
	if ttl != tokenTTL {
		return errors.New("ttl")
	}
	f.values[key] = value
	return nil
}
