package requisite

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"finance-platform/go/internal/auth"
	"finance-platform/go/internal/read"
)

const requisiteTTL = 24 * time.Hour

type storedRequisite struct {
	Detail   string `json:"detail"`
	Type     string `json:"detail_type"`
	Initials string `json:"initials"`
}

// Load reads a cascade requisite stored as JSON for an order.
// A missing or empty value returns nil.
func Load(ctx context.Context, cache auth.Cache, prefix string, orderID int64) (*read.External, error) {
	raw, err := cache.Get(ctx, key(prefix, orderID))
	if errors.Is(err, auth.ErrMiss) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var body storedRequisite
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		return nil, nil
	}
	if body.Detail == "" && body.Type == "" && body.Initials == "" {
		return nil, nil
	}
	return &read.External{Detail: body.Detail, Type: body.Type, Initials: body.Initials}, nil
}

// Store is the Redis JSON requisite used by order assignment and callbacks.
type Store struct {
	Cache  auth.Cache
	Prefix string
}

// Save writes one order requisite.
func (s Store) Save(ctx context.Context, orderID int64, detail read.External) error {
	return Save(ctx, s.Cache, s.Prefix, orderID, detail)
}

// Load reads one order requisite.
func (s Store) Load(ctx context.Context, orderID int64) (*read.External, error) {
	return Load(ctx, s.Cache, s.Prefix, orderID)
}

// Save stores a cascade requisite as JSON.
func Save(ctx context.Context, cache auth.Cache, prefix string, orderID int64, detail read.External) error {
	raw, err := json.Marshal(storedRequisite{Detail: detail.Detail, Type: detail.Type, Initials: detail.Initials})
	if err != nil {
		return err
	}
	return cache.Set(ctx, key(prefix, orderID), string(raw), requisiteTTL)
}

func key(prefix string, orderID int64) string {
	return prefix + "order-requisite:" + strconv.FormatInt(orderID, 10)
}
