package requisite

import (
	"context"
	"testing"
	"time"

	"finance-platform/go/internal/auth"
	"finance-platform/go/internal/read"
)

func TestRequisiteJSONRoundTrip(t *testing.T) {
	cache := memCache{values: map[string]string{}}
	ctx := context.Background()
	want := read.External{Detail: "+7900", Type: "phone", Initials: "IVAN"}
	if err := Save(ctx, cache, "cache_", 7, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(ctx, cache, "cache_", 7)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != want {
		t.Fatalf("got %+v", got)
	}
}

func TestLoadIgnoresForeignPayload(t *testing.T) {
	cache := memCache{values: map[string]string{
		"cache_order-requisite:3": `a:1:{s:6:"detail";s:3:"php";}`,
	}}
	got, err := Load(context.Background(), cache, "cache_", 3)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("got %+v", got)
	}
}

type memCache struct {
	values map[string]string
}

func (m memCache) Get(_ context.Context, key string) (string, error) {
	value, ok := m.values[key]
	if !ok {
		return "", auth.ErrMiss
	}
	return value, nil
}

func (m memCache) Set(_ context.Context, key, value string, _ time.Duration) error {
	m.values[key] = value
	return nil
}
