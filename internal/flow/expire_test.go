package flow

import (
	"database/sql"
	"testing"
	"time"
)

func TestDueForExpiry(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	past := sql.NullTime{Time: now.Add(-time.Minute), Valid: true}
	future := sql.NullTime{Time: now.Add(time.Minute), Valid: true}
	if !dueForExpiry("pending", past, now, false) {
		t.Fatal("past pending should expire")
	}
	if dueForExpiry("pending", past, now, true) {
		t.Fatal("dispute stays open")
	}
	if dueForExpiry("fail", past, now, false) {
		t.Fatal("already failed is not due again")
	}
	if dueForExpiry("pending", future, now, false) {
		t.Fatal("future window stays open")
	}
	if dueForExpiry("pending", sql.NullTime{}, now, false) {
		t.Fatal("missing expires_at stays open")
	}
}
