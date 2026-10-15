package schedule

import (
	"testing"
	"time"
)

func TestAtMidnight(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	midnight := time.Date(2026, 10, 16, 0, 0, 30, 0, loc)
	if !AtMidnight(midnight) {
		t.Fatal("00:00 is the daily tick")
	}
	noon := time.Date(2026, 10, 15, 12, 2, 0, 0, loc)
	if AtMidnight(noon) {
		t.Fatal("noon is not the daily tick")
	}
}

func TestUnknownInterval(t *testing.T) {
	_, err := Jobs{}.Run(t.Context(), 2*time.Minute)
	if err == nil {
		t.Fatal("expected unknown interval")
	}
}
