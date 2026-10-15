package impersonate

import (
	"testing"
	"time"
)

func TestUntilStopsAtHardCap(t *testing.T) {
	created := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	now := created.Add(90 * time.Minute)
	got := until(now, created, SessionTTL, HardCap)
	want := created.Add(HardCap)
	if !got.Equal(want) {
		t.Fatalf("cap %s, got %s", want, got)
	}
	early := until(created, created, SessionTTL, HardCap)
	if !early.Equal(created.Add(SessionTTL)) {
		t.Fatalf("session %s", early)
	}
}

func TestAllowsCabinetOnly(t *testing.T) {
	if !allows("/api/frontend/merchant/dashboard", "Merchant") {
		t.Fatal("merchant")
	}
	if allows("/api/v1/order", "Merchant") {
		t.Fatal("v1")
	}
	if allows("/api/frontend/trader/payments/list", "Merchant") {
		t.Fatal("wrong cabinet")
	}
	if allows("/api/bot/order/1", "Trader") {
		t.Fatal("bot")
	}
}
