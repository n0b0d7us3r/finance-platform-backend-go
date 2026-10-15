package flow

import (
	"context"
	"errors"
	"time"
)

const (
	waitTimeoutMessage = "Не удалось обработать запрос вовремя. Повторите попытку позже."
	waitBusyMessage    = "Слишком много ожиданий реквизита. Повторите попытку позже."
)

// Gate limits how many order creates may wait for a requisite at once.
type Gate struct {
	slots chan struct{}
}

// NewGate allows n concurrent waits. A non-positive n uses 64.
func NewGate(n int) *Gate {
	if n <= 0 {
		n = 64
	}
	return &Gate{slots: make(chan struct{}, n)}
}

// Enter reports whether a wait slot was taken.
func (g *Gate) Enter() bool {
	if g == nil {
		return true
	}
	select {
	case g.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

// Leave returns a slot taken by Enter.
func (g *Gate) Leave() {
	if g == nil {
		return
	}
	select {
	case <-g.slots:
	default:
	}
}

// Hold is the short claim that one create is waiting for a given external id.
// A lost process releases the claim when the TTL ends, and no order row exists
// until assignment commits.
type Hold interface {
	Try(ctx context.Context, key string, ttl time.Duration) (bool, error)
	Drop(ctx context.Context, key string) error
}

func waitBudget(merchantMS int, header, ceiling time.Duration) time.Duration {
	ms := merchantMS
	if header > 0 {
		ms = int(header / time.Millisecond)
	}
	if ms <= 0 {
		if ceiling <= 0 {
			return 0
		}
		ms = int(ceiling / time.Millisecond)
	}
	if ms < 1000 {
		ms = 1000
	}
	budget := time.Duration(ms) * time.Millisecond
	if ceiling > 0 && budget > ceiling {
		return ceiling
	}
	return budget
}

func retryable(err error) bool {
	var client *Error
	return errors.As(err, &client) && client.Retry
}

func (s *Service) pollInterval() time.Duration {
	if s != nil && s.Poll > 0 {
		return s.Poll
	}
	return 100 * time.Millisecond
}

func (s *Service) sleep(ctx context.Context, d time.Duration) error {
	if s != nil && s.Sleep != nil {
		return s.Sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// runWait retries assign until deadline. The order row is created only inside
// a successful assign. A full gate returns 503 and does not call assign.
// A claim held by someone else does not assign; it reports the existing order
// or 504.
func runWait(
	ctx context.Context,
	now func() time.Time,
	gate *Gate,
	hold Hold,
	key string,
	deadline time.Time,
	poll time.Duration,
	sleep func(context.Context, time.Duration) error,
	assign func(context.Context) (string, error),
	taken func(context.Context) (bool, error),
) (string, error) {
	if now == nil {
		now = time.Now
	}
	if gate != nil {
		if !gate.Enter() {
			return "", fail(503, waitBusyMessage)
		}
		defer gate.Leave()
	}
	owned := true
	if hold != nil {
		ok, err := hold.Try(ctx, key, time.Until(deadline)+poll)
		if err != nil {
			return "", err
		}
		owned = ok
		if ok {
			defer func() { _ = hold.Drop(context.WithoutCancel(ctx), key) }()
		}
	}
	for {
		if !now().Before(deadline) {
			if !owned && taken != nil {
				exists, err := taken(ctx)
				if err != nil {
					return "", err
				}
				if exists {
					return "", duplicateExternal()
				}
			}
			return "", fail(504, waitTimeoutMessage)
		}
		if owned {
			id, err := assign(ctx)
			if err == nil || !retryable(err) {
				return id, err
			}
		}
		if err := sleep(ctx, poll); err != nil {
			return "", err
		}
	}
}
