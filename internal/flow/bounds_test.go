package flow

import (
	"database/sql"
	"testing"
	"time"

	"finance-platform/go/internal/money"
)

func TestDailyAndBounds(t *testing.T) {
	amount, err := money.FromUnits("150000", "rub")
	if err != nil {
		t.Fatal(err)
	}
	if dailyFits("0", "0", amount) {
		t.Fatal("zero daily limit has no room")
	}
	if !dailyFits("200000", "12000", amount) {
		t.Fatal("remaining daily limit should fit")
	}
	if !boundFits("0", "0", amount) {
		t.Fatal("zero bounds are unset")
	}
	if boundFits("1000", "80000", amount) {
		t.Fatal("amount above the detail max")
	}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	last := sql.NullTime{Time: now.Add(-time.Minute), Valid: true}
	if intervalOpen(2, last, now) {
		t.Fatal("interval has not elapsed")
	}
	if !intervalOpen(0, last, now) || !intervalOpen(2, sql.NullTime{}, now) {
		t.Fatal("unset interval is open")
	}
}
