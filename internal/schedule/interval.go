package schedule

import (
	"fmt"
	"time"
)

type intervalError struct {
	every time.Duration
}

func (e intervalError) Error() string {
	return fmt.Sprintf("unknown schedule interval %s", e.every)
}

func errInterval(every time.Duration) error {
	return intervalError{every: every}
}

// AtMidnight reports whether the local clock is in the 00:00 minute.
func AtMidnight(now time.Time) bool {
	return now.Hour() == 0 && now.Minute() == 0
}
