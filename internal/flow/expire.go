package flow

import (
	"context"
	"database/sql"
	"time"
)

// ExpireEvery is how often the scheduler closes pending orders past expires_at.
const ExpireEvery = time.Minute

// ExpireDue fails pending orders whose payment window has ended and returns
// trust once. A dispute row keeps the order open. A second pass finds nothing
// to refund.
func (s *Service) ExpireDue(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT uuid FROM orders
		WHERE status = 'pending' AND expires_at IS NOT NULL AND expires_at <= ?
		  AND NOT EXISTS (SELECT 1 FROM disputes d WHERE d.order_id = orders.id)
		ORDER BY expires_at
		LIMIT ?`, s.now(), limit)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	applied := 0
	var failed error
	for _, id := range ids {
		ok, err := s.expireOne(ctx, id)
		if err != nil {
			failed = err
			continue
		}
		if ok {
			applied++
		}
	}
	return applied, failed
}

func (s *Service) expireOne(ctx context.Context, orderUUID string) (bool, error) {
	var applied bool
	err := s.tx(ctx, func(tx *sql.Tx) error {
		row, err := lockOrder(ctx, tx, orderUUID)
		if err != nil {
			return err
		}
		var expires sql.NullTime
		if err := tx.QueryRowContext(ctx, `SELECT expires_at FROM orders WHERE id = ?`, row.ID).Scan(&expires); err != nil {
			return err
		}
		var dispute int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM disputes WHERE order_id = ? LIMIT 1`, row.ID).Scan(&dispute)
		hasDispute := err == nil
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if !dueForExpiry(row.Status, expires, s.now(), hasDispute) {
			return nil
		}
		applied = true
		return s.markFailed(ctx, tx, row, "expired")
	})
	return applied, err
}

func dueForExpiry(status string, expires sql.NullTime, now time.Time, hasDispute bool) bool {
	if status != "pending" || hasDispute || !expires.Valid {
		return false
	}
	return !expires.Time.After(now)
}
