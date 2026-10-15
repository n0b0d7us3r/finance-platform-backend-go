package flow

import (
	"database/sql"
	"testing"
	"time"
)

func TestPrimeWindow(t *testing.T) {
	loc := time.FixedZone("MSK", 3*60*60)
	inside := time.Date(2026, 10, 15, 2, 29, 0, 0, loc)
	if !primeActive(inside, "00:00", "05:00") {
		t.Fatal("02:29 is inside 00:00-05:00")
	}
	edge := time.Date(2026, 10, 15, 5, 0, 0, 0, loc)
	if !primeActive(edge, "00:00", "05:00") {
		t.Fatal("05:00 is included")
	}
	after := time.Date(2026, 10, 15, 5, 0, 1, 0, loc)
	if primeActive(after, "00:00", "05:00") {
		t.Fatal("05:00:01 is outside")
	}
	if primeActive(inside, "22:00", "02:00") {
		t.Fatal("a same-day window with end before start is empty")
	}
	if addPrime(7, 1) != 8 {
		t.Fatalf("prime add %v", addPrime(7, 1))
	}
}

func TestGatewayPrefs(t *testing.T) {
	raw := `{"4":{"active":false},"9":{"custom_gateway_commission":8.5,"custom_gateway_reservation_time":20},"3":{"custom_gateway_commission":null}}`
	prefs := parseGatewayPrefs(raw)
	off, ok := applyGatewayPrefs(gatewayRow{ID: 4, TotalRate: 10, Reservation: 15}, prefs)
	if ok || off.TotalRate != 10 {
		t.Fatalf("disabled %+v %v", off, ok)
	}
	custom, ok := applyGatewayPrefs(gatewayRow{ID: 9, TotalRate: 10, Reservation: 15}, prefs)
	if !ok || custom.TotalRate != 8.5 || custom.Reservation != 20 {
		t.Fatalf("custom %+v", custom)
	}
	plain, ok := applyGatewayPrefs(gatewayRow{ID: 3, TotalRate: 10, Reservation: 15}, prefs)
	if !ok || plain.TotalRate != 10 || plain.Reservation != 15 {
		t.Fatalf("null commission %+v", plain)
	}
}

func TestUniqueBoundsAndRank(t *testing.T) {
	low, high, ok := uniqueBounds("10000", "3")
	if !ok || low != "9700" || high != "10300" {
		t.Fatalf("%s %s %v", low, high, ok)
	}
	if categoryRank(sql.NullInt64{Int64: 2, Valid: true}, []int64{2, 3}) != 0 {
		t.Fatal("first category")
	}
	if categoryRank(sql.NullInt64{}, []int64{2, 3}) != 2 {
		t.Fatal("missing category")
	}
	if categoryRank(sql.NullInt64{Int64: 9, Valid: true}, []int64{2, 3}) != -1 {
		t.Fatal("other category")
	}
	if !failedStreak([]string{"fail", "fail", "fail"}, 3) {
		t.Fatal("streak")
	}
	if failedStreak([]string{"fail", "success", "fail"}, 3) || failedStreak([]string{"fail"}, 3) {
		t.Fatal("broken streak")
	}
	if trustFloor("0") != "1" || trustFloor("1500") != "1500" {
		t.Fatal("trust floor")
	}
}

func TestDetailOrder(t *testing.T) {
	older := time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	items := []rankedDetail{
		{rank: 1, lastUsed: sql.NullTime{Time: older, Valid: true}, id: 1},
		{rank: 0, lastUsed: sql.NullTime{Time: newer, Valid: true}, id: 2},
		{rank: 0, id: 3},
	}
	sortDetails(items)
	if items[0].id != 3 || items[1].id != 2 || items[2].id != 1 {
		t.Fatalf("order %d %d %d", items[0].id, items[1].id, items[2].id)
	}
}
