package schema

import (
	"strings"
	"testing"
)

func TestBaselineAndSeedPresent(t *testing.T) {
	body, err := migrations.ReadFile("migrations/00001_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "-- +goose Up") || !strings.Contains(text, "-- +goose Down") {
		t.Fatal("baseline is missing goose directions")
	}
	if !strings.Contains(text, "CREATE TABLE `users`") || !strings.Contains(text, "CREATE TABLE `outbox`") {
		t.Fatal("baseline is missing the stand tables")
	}
	if !strings.Contains(demoSeed, "admin-1@demo.local") || !strings.Contains(demoSeed, "method-card-1") {
		t.Fatal("seed is missing the stand admin or card method")
	}
	if len(splitSQL(demoSeed)) < 10 {
		t.Fatal("seed did not split into statements")
	}
}
