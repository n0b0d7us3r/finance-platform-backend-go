package schedule

import (
	"context"
	"database/sql"
	"time"

	"finance-platform/go/internal/flow"
)

const (
	EveryMinute   = time.Minute
	EveryFive     = 5 * time.Minute
	EveryHalfHour = 30 * time.Minute
	EveryHour     = time.Hour
	EveryDay      = 24 * time.Hour
)

// Result counts rows a tick changed.
type Result struct {
	Orders       int
	Holds        int
	Invoices     int
	Manual       int
	Sessions     int
	Deposits     int
	Inactive     int
	Limits       int
	Sms          int
	Statistics   int
	MethodsSaved bool
}

// Jobs is one scheduler pass. Now and Location decide the clock; nil Now is time.Now.
type Jobs struct {
	DB       *sql.DB
	Orders   *flow.Service
	Location *time.Location
	Now      func() time.Time
	Methods  MethodCache
	Deposits Deposits
}

// Run executes the jobs that Laravel scheduled at this interval.
func (j Jobs) Run(ctx context.Context, every time.Duration) (Result, error) {
	switch every {
	case EveryMinute:
		return j.minute(ctx)
	case EveryFive:
		return j.five(ctx)
	case EveryHalfHour:
		return j.half(ctx)
	case EveryHour:
		return j.hour(ctx)
	case EveryDay:
		return j.day(ctx)
	default:
		return Result{}, errInterval(every)
	}
}

func (j Jobs) minute(ctx context.Context) (Result, error) {
	var result Result
	var err error
	if j.Orders != nil {
		result.Orders, err = j.Orders.ExpireDue(ctx, 100)
		if err != nil {
			return result, err
		}
		result.Holds, err = j.Orders.ExecuteDue(ctx, 100)
		if err != nil {
			return result, err
		}
	}
	now := j.now()
	result.Invoices, err = j.expireInvoices(ctx, now)
	if err != nil {
		return result, err
	}
	result.Manual, err = j.closeManual(ctx, now)
	if err != nil {
		return result, err
	}
	result.Sessions, err = j.expireSessions(ctx, now)
	if err != nil {
		return result, err
	}
	if j.Deposits.Enabled() {
		result.Deposits, err = j.Deposits.Check(ctx, j.DB, now)
	}
	return result, err
}

func (j Jobs) five(ctx context.Context) (Result, error) {
	n, err := j.updateStatistics(ctx, j.now())
	return Result{Statistics: n}, err
}

func (j Jobs) half(ctx context.Context) (Result, error) {
	n, err := j.disconnectInactive(ctx, j.now())
	return Result{Inactive: n}, err
}

func (j Jobs) hour(ctx context.Context) (Result, error) {
	if j.Methods == nil {
		return Result{}, nil
	}
	saved, err := j.Methods.Load(ctx)
	return Result{MethodsSaved: saved}, err
}

func (j Jobs) day(ctx context.Context) (Result, error) {
	limits, err := j.resetLimits(ctx)
	if err != nil {
		return Result{}, err
	}
	sms, err := j.clearSms(ctx, j.now())
	return Result{Limits: limits, Sms: sms}, err
}

func (j Jobs) now() time.Time {
	current := time.Now()
	if j.Now != nil {
		current = j.Now()
	}
	if j.Location != nil {
		return current.In(j.Location)
	}
	return current
}

func (j Jobs) expireInvoices(ctx context.Context, now time.Time) (int, error) {
	res, err := j.DB.ExecContext(ctx, `
		UPDATE invoices
		SET status = 'fail', updated_at = ?
		WHERE status = 'pending' AND type = 'deposit' AND balance_type = 'trust' AND created_at < ?`,
		now, now.Add(-30*time.Minute))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (j Jobs) closeManual(ctx context.Context, now time.Time) (int, error) {
	res, err := j.DB.ExecContext(ctx, `
		UPDATE orders
		SET status = 'fail', finished_at = ?, updated_at = ?
		WHERE expires_at IS NULL AND payment_detail_id IS NULL AND status = 'pending'
		  AND DATE(created_at) < ?`,
		now, now, now.Add(-6*time.Hour).Format("2006-01-02"))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (j Jobs) expireSessions(ctx context.Context, now time.Time) (int, error) {
	res, err := j.DB.ExecContext(ctx, `
		UPDATE impersonation_sessions
		SET revoked_at = ?, updated_at = ?
		WHERE revoked_at IS NULL AND expires_at <= ?`, now, now, now)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (j Jobs) disconnectInactive(ctx context.Context, now time.Time) (int, error) {
	res, err := j.DB.ExecContext(ctx, `
		UPDATE users u
		SET is_online = 0, updated_at = ?
		WHERE u.is_online = 1
		  AND u.updated_at < ?
		  AND EXISTS (SELECT 1 FROM orders o WHERE o.trader_id = u.id)
		  AND NOT EXISTS (
		    SELECT 1 FROM orders o WHERE o.trader_id = u.id AND o.created_at > ?
		  )`, now, now.Add(-3*time.Hour), now.Add(-6*time.Hour))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (j Jobs) resetLimits(ctx context.Context) (int, error) {
	res, err := j.DB.ExecContext(ctx, `UPDATE payment_details SET current_daily_limit = '0'`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (j Jobs) clearSms(ctx context.Context, now time.Time) (int, error) {
	res, err := j.DB.ExecContext(ctx, `
		DELETE FROM sms_logs
		WHERE order_id IS NULL AND DATE(created_at) < ?`, now.Add(-72*time.Hour).Format("2006-01-02"))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
