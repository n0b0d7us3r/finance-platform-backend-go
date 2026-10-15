package flow

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
)

type paySettings struct {
	PrimeStart  string
	PrimeEnd    string
	PrimeRate   float64
	MaxDisputes int
	FailCount   int
	FailMinutes int
}

func (s *Service) loadPaySettings(ctx context.Context) (paySettings, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT `+"`key`"+`, value FROM settings
		WHERE `+"`key`"+` IN (
			'prime_time_bonus_starts', 'prime_time_bonus_ends', 'prime_time_bonus_rate',
			'max_pending_disputes', 'max_consecutive_failed_orders'
		)`)
	if err != nil {
		return paySettings{}, err
	}
	defer rows.Close()
	var got paySettings
	for rows.Next() {
		var key string
		var value sql.NullString
		if err := rows.Scan(&key, &value); err != nil {
			return paySettings{}, err
		}
		switch key {
		case "prime_time_bonus_starts":
			got.PrimeStart = value.String
		case "prime_time_bonus_ends":
			got.PrimeEnd = value.String
		case "prime_time_bonus_rate":
			got.PrimeRate, _ = strconv.ParseFloat(value.String, 64)
		case "max_pending_disputes":
			got.MaxDisputes, _ = strconv.Atoi(value.String)
		case "max_consecutive_failed_orders":
			var parsed struct {
				Count  int `json:"count"`
				Period int `json:"period"`
			}
			if value.Valid {
				_ = json.Unmarshal([]byte(value.String), &parsed)
			}
			got.FailCount = parsed.Count
			got.FailMinutes = parsed.Period
		}
	}
	return got, rows.Err()
}

type clock struct {
	hour, min, sec int
}

func parseClock(value string) (clock, error) {
	parsed, err := time.Parse("15:04:05", value)
	if err != nil {
		parsed, err = time.Parse("15:04", value)
		if err != nil {
			return clock{}, err
		}
	}
	return clock{parsed.Hour(), parsed.Minute(), parsed.Second()}, nil
}

// primeActive reports whether now is inside the same-day window, both ends included.
// A window whose end is earlier than its start is empty, matching a same-day comparison.
func primeActive(now time.Time, starts, ends string) bool {
	if starts == "" || ends == "" {
		return false
	}
	start, err := parseClock(starts)
	if err != nil {
		return false
	}
	end, err := parseClock(ends)
	if err != nil {
		return false
	}
	loc := now.Location()
	year, month, day := now.Date()
	from := time.Date(year, month, day, start.hour, start.min, start.sec, 0, loc)
	to := time.Date(year, month, day, end.hour, end.min, end.sec, 0, loc)
	if to.Before(from) {
		return false
	}
	return !now.Before(from) && !now.After(to)
}

func addPrime(traderRate, bonus float64) float64 {
	sum := decimal.NewFromFloat(traderRate).Add(decimal.NewFromFloat(bonus))
	out, _ := sum.Round(2).Float64()
	return out
}

type gatewayPrefs map[string]gatewayPref

type gatewayPref struct {
	Active      *bool
	Commission  *float64
	Reservation *int
}

func parseGatewayPrefs(raw string) gatewayPrefs {
	if raw == "" {
		return nil
	}
	var decoded map[string]map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil
	}
	out := make(gatewayPrefs, len(decoded))
	for id, item := range decoded {
		var pref gatewayPref
		if value, ok := item["active"].(bool); ok {
			pref.Active = &value
		}
		if value, ok := number(item["custom_gateway_commission"]); ok {
			pref.Commission = &value
		}
		if value, ok := item["custom_gateway_reservation_time"]; ok && value != nil {
			if minutes, ok := number(value); ok && minutes > 0 {
				whole := int(minutes)
				pref.Reservation = &whole
			}
		}
		out[id] = pref
	}
	return out
}

func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

// applyGatewayPrefs returns false when the merchant turned this gateway off.
func applyGatewayPrefs(row gatewayRow, prefs gatewayPrefs) (gatewayRow, bool) {
	pref, ok := prefs[strconv.FormatInt(row.ID, 10)]
	if !ok {
		return row, true
	}
	if pref.Active != nil && !*pref.Active {
		return row, false
	}
	if pref.Commission != nil {
		if *pref.Commission > 0 || int(*pref.Commission) == 0 {
			row.TotalRate = *pref.Commission
		}
	}
	if pref.Reservation != nil {
		row.Reservation = *pref.Reservation
	}
	return row, true
}

func uniqueBounds(units, percent string) (string, string, bool) {
	amount, err := decimal.NewFromString(units)
	if err != nil {
		return "", "", false
	}
	rate, err := decimal.NewFromString(percent)
	if err != nil || rate.IsNegative() {
		return "", "", false
	}
	ratio := rate.Div(decimal.NewFromInt(100))
	low := amount.Mul(decimal.NewFromInt(1).Sub(ratio)).Round(0)
	high := amount.Mul(decimal.NewFromInt(1).Add(ratio)).Round(0)
	return low.String(), high.String(), true
}

// categoryRank orders details the way the merchant's category list does.
// A category absent from a non-empty list is excluded: those traders are not
// candidates. A missing category stays last. An empty list keeps everyone.
func categoryRank(category sql.NullInt64, order []int64) int {
	if len(order) == 0 {
		return 0
	}
	if !category.Valid {
		return len(order)
	}
	for i, id := range order {
		if id == category.Int64 {
			return i
		}
	}
	return -1
}

type rankedDetail struct {
	rank     int
	lastUsed sql.NullTime
	id       int64
}

func lessDetail(a, b rankedDetail) bool {
	if a.rank != b.rank {
		return a.rank < b.rank
	}
	if a.lastUsed.Valid != b.lastUsed.Valid {
		return !a.lastUsed.Valid
	}
	if a.lastUsed.Valid && !a.lastUsed.Time.Equal(b.lastUsed.Time) {
		return a.lastUsed.Time.Before(b.lastUsed.Time)
	}
	return a.id < b.id
}

func sortDetails(items []rankedDetail) {
	sort.SliceStable(items, func(i, j int) bool { return lessDetail(items[i], items[j]) })
}

func failedStreak(statuses []string, count int) bool {
	if count <= 0 || len(statuses) < count {
		return false
	}
	for _, status := range statuses[:count] {
		if status != "fail" {
			return false
		}
	}
	return true
}

func trustFloor(units string) string {
	value, err := decimal.NewFromString(zero(units))
	if err != nil || value.LessThan(decimal.NewFromInt(1)) {
		return "1"
	}
	return value.String()
}
