package flow

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestWaitBudgetCapsAndFloor(t *testing.T) {
	ceiling := 30 * time.Second
	if got := waitBudget(0, 0, ceiling); got != ceiling {
		t.Fatalf("default %s", got)
	}
	if got := waitBudget(0, 50*time.Millisecond, ceiling); got != time.Second {
		t.Fatalf("floor %s", got)
	}
	if got := waitBudget(5000, 90*time.Second, ceiling); got != ceiling {
		t.Fatalf("cap %s", got)
	}
	if got := waitBudget(4000, 2*time.Second, ceiling); got != 2*time.Second {
		t.Fatalf("header %s", got)
	}
	if got := waitBudget(0, 0, 0); got != 0 {
		t.Fatalf("disabled %s", got)
	}
}

func TestRunWaitBusyDoesNotAssign(t *testing.T) {
	gate := NewGate(1)
	if !gate.Enter() {
		t.Fatal("enter")
	}
	calls := 0
	_, err := runWait(context.Background(), time.Now, gate, nil, "k", time.Now().Add(time.Second), time.Millisecond,
		func(context.Context, time.Duration) error { return nil },
		func(context.Context) (string, error) {
			calls++
			return "x", nil
		}, nil)
	var client *Error
	if !errors.As(err, &client) || client.Status != 503 || calls != 0 {
		t.Fatalf("err %v calls %d", err, calls)
	}
}

func TestRunWaitTimeoutAssignsNothing(t *testing.T) {
	start := time.Now()
	now := start
	calls := 0
	_, err := runWait(context.Background(), func() time.Time { return now }, nil, nil, "k", start.Add(250*time.Millisecond), 100*time.Millisecond,
		func(context.Context, time.Duration) error {
			now = now.Add(100 * time.Millisecond)
			return nil
		},
		func(context.Context) (string, error) {
			calls++
			return "", requisiteMiss()
		}, nil)
	var client *Error
	if !errors.As(err, &client) || client.Status != 504 {
		t.Fatalf("err %v", err)
	}
	if calls == 0 {
		t.Fatal("expected retries before timeout")
	}
}

func TestRunWaitCancelDoesNotSucceed(t *testing.T) {
	calls := 0
	_, err := runWait(context.Background(), time.Now, nil, nil, "k", time.Now().Add(time.Second), time.Millisecond,
		func(context.Context, time.Duration) error { return context.Canceled },
		func(context.Context) (string, error) {
			calls++
			return "", requisiteMiss()
		}, nil)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("err %v calls %d", err, calls)
	}
}

func TestRunWaitForeignHoldDoesNotAssign(t *testing.T) {
	hold := &memHold{keys: map[string]struct{}{"m:ext": {}}}
	calls := 0
	start := time.Now()
	_, err := runWait(context.Background(), func() time.Time { return start.Add(time.Second) }, nil, hold, "m:ext", start, time.Millisecond,
		func(context.Context, time.Duration) error { return nil },
		func(context.Context) (string, error) {
			calls++
			return "made", nil
		},
		func(context.Context) (bool, error) { return true, nil })
	var client *Error
	if !errors.As(err, &client) || client.Status != 422 || calls != 0 {
		t.Fatalf("err %v calls %d", err, calls)
	}
}

func TestRunWaitSecondTryCommitsOnce(t *testing.T) {
	start := time.Now()
	now := start
	calls := 0
	id, err := runWait(context.Background(), func() time.Time { return now }, NewGate(2), &memHold{keys: map[string]struct{}{}}, "m:ext", start.Add(time.Second), 10*time.Millisecond,
		func(context.Context, time.Duration) error {
			now = now.Add(10 * time.Millisecond)
			return nil
		},
		func(context.Context) (string, error) {
			calls++
			if calls == 1 {
				return "", requisiteMiss()
			}
			return "once", nil
		}, nil)
	if err != nil || id != "once" || calls != 2 {
		t.Fatalf("id %s err %v calls %d", id, err, calls)
	}
}

type memHold struct {
	mu   sync.Mutex
	keys map[string]struct{}
}

func (m *memHold) Try(_ context.Context, key string, _ time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.keys[key]; ok {
		return false, nil
	}
	m.keys[key] = struct{}{}
	return true, nil
}

func (m *memHold) Drop(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.keys, key)
	return nil
}
