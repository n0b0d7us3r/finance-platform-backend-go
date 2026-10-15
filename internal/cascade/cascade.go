// Package cascade chooses an external requisite after the internal pool misses.
// The catalog is the provider integration name. One loop walks the terminals.
package cascade

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
)

// ErrSkip means this adapter does not implement the terminal's integration.
var ErrSkip = errors.New("cascade: integration is not handled")

// ErrMiss means the provider did not return a usable requisite.
var ErrMiss = errors.New("cascade: provider returned no requisite")

// Terminal is one active provider terminal already limited to the merchant.
type Terminal struct {
	ID           int64
	UUID         string
	ProviderID   int64
	ProviderUUID string
	ProviderName string
	Integration  string
	TraderID     int64
	WebhookMode  string
	Rate         float64
	Min          string
	Max          string
	TimeoutMS    int
	Retries      int
	DetailTypes  []string
	Trust        string
	Settings     json.RawMessage
}

// Request is the order the adapter sends to the provider.
type Request struct {
	OrderUUID  string
	Amount     string
	Currency   string
	DetailType string
}

// Quote is a requisite the provider returned. Response is the raw body for the call log.
type Quote struct {
	ProviderOrderID string
	Detail          string
	DetailType      string
	Initials        string
	Response        []byte
	Millis          int
	Attempt         int
}

// Priority is round(rate × 10). A lower number is tried first.
func Priority(rate float64) int {
	return int(math.Round(rate * 10))
}

// Select keeps terminals the flag, the amount, the detail type and a positive trust allow.
// The result is ordered by priority, then id.
func Select(enabled bool, terminals []Terminal, amount, detailType string) []Terminal {
	if !enabled {
		return nil
	}
	var ready []Terminal
	for _, terminal := range terminals {
		if !allowsType(terminal.DetailTypes, detailType) {
			continue
		}
		if !inRange(terminal.Min, terminal.Max, amount) {
			continue
		}
		if !positive(terminal.Trust) {
			continue
		}
		ready = append(ready, terminal)
	}
	sort.SliceStable(ready, func(i, j int) bool {
		left := Priority(ready[i].Rate)
		right := Priority(ready[j].Rate)
		if left != right {
			return left < right
		}
		return ready[i].ID < ready[j].ID
	})
	return ready
}

func allowsType(types []string, detailType string) bool {
	if detailType == "" {
		return len(types) > 0
	}
	for _, item := range types {
		if item == detailType {
			return true
		}
	}
	return false
}

func inRange(min, max, amount string) bool {
	value, err := decimal.NewFromString(amount)
	if err != nil {
		return false
	}
	if strings.TrimSpace(min) != "" {
		floor, err := decimal.NewFromString(min)
		if err != nil || value.LessThan(floor) {
			return false
		}
	}
	if strings.TrimSpace(max) != "" {
		ceiling, err := decimal.NewFromString(max)
		if err != nil || value.GreaterThan(ceiling) {
			return false
		}
	}
	return true
}

func positive(units string) bool {
	value, err := decimal.NewFromString(strings.TrimSpace(units))
	return err == nil && value.IsPositive()
}

// ParseDetailID checks CASCADE_PAYMENT_DETAIL_ID before the process listens.
// check is true when the id must exist in payment_details.
func ParseDetailID(raw string, enabled bool) (id int64, check bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if enabled {
			return 0, false, errors.New("CASCADE_PAYMENT_DETAIL_ID is required when FEATURE_CASCADE is enabled")
		}
		return 0, false, nil
	}
	id, err = strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false, errors.New("CASCADE_PAYMENT_DETAIL_ID is invalid")
	}
	return id, true, nil
}

// RequireDetail exits the API before it listens when the id is set and missing,
// and when the cascade flag is on without an id.
func RequireDetail(ctx context.Context, db *sql.DB, raw string, enabled bool) error {
	id, check, err := ParseDetailID(raw, enabled)
	if err != nil || !check {
		return err
	}
	var found int
	err = db.QueryRowContext(ctx, `SELECT 1 FROM payment_details WHERE id = ?`, id).Scan(&found)
	if err == sql.ErrNoRows {
		return errors.New("CASCADE_PAYMENT_DETAIL_ID does not match a payment detail")
	}
	return err
}
