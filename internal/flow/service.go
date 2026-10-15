package flow

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"finance-platform/go/internal/market"
	"finance-platform/go/internal/read"

	"net/http"
)

// Service writes the payment flows the tester drives through HTTP.
type Service struct {
	DB              *sql.DB
	Quotes          market.Book
	Now             func() time.Time
	Location        *time.Location
	MaxWait         time.Duration
	Poll            time.Duration
	Gate            *Gate
	Holds           Hold
	Sleep           func(context.Context, time.Duration) error
	LocalWithdraw   bool
	Cascade         bool
	CascadeDetailID int64
	Requisites      Requisites
	HTTP            *http.Client
}

// Requisites stores the external card JSON for an order that has no provider row of its own.
type Requisites interface {
	Save(ctx context.Context, orderID int64, detail read.External) error
	Load(ctx context.Context, orderID int64) (*read.External, error)
}

func (s *Service) now() time.Time {
	current := time.Now()
	if s.Now != nil {
		current = s.Now()
	}
	if s.Location != nil {
		return current.In(s.Location)
	}
	return current
}

func (s *Service) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func randomToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func newUUID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16]), nil
}

func zero(value string) string {
	if value == "" {
		return "0"
	}
	return value
}
