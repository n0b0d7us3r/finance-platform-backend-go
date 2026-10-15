package flow

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"
)

// OpenDispute starts or reopens a dispute and parks the order for support.
func (s *Service) OpenDispute(ctx context.Context, userID int64, orderUUID, receipt string) error {
	return s.openDispute(ctx, orderUUID, receipt, userID, true)
}

// OpenDisputeUnchecked starts a dispute without the merchant-owner check.
// Support and the bot call it. The receipt may be empty.
func (s *Service) OpenDisputeUnchecked(ctx context.Context, orderUUID, receipt string) error {
	return s.openDispute(ctx, orderUUID, receipt, 0, false)
}

func (s *Service) openDispute(ctx context.Context, orderUUID, receipt string, userID int64, checkOwner bool) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		row, err := lockOrder(ctx, tx, orderUUID)
		if err != nil {
			return err
		}
		if checkOwner && row.Owner != userID {
			return plain(403, "This action is unauthorized.")
		}
		var disputeID int64
		var status string
		err = tx.QueryRowContext(ctx, `SELECT id, status FROM disputes WHERE order_id = ? FOR UPDATE`, row.ID).Scan(&disputeID, &status)
		if err == nil {
			if status != "canceled" {
				return fail(400, "Dispute already exists.")
			}
			if row.Status != "pending" {
				if err := s.reopen(ctx, tx, row, "waiting_for_dispute_to_be_resolved"); err != nil {
					return err
				}
			}
			if _, err = tx.ExecContext(ctx, `
				UPDATE disputes
				SET status = 'pending', reason = NULL, cancel_reason = NULL, receipt = COALESCE(?, receipt), updated_at = ?
				WHERE id = ?`, emptyAsNil(receipt), s.now(), disputeID); err != nil {
				return err
			}
			return s.guardPending(ctx, tx, row.TraderID)
		}
		if err != sql.ErrNoRows {
			return err
		}
		switch row.Status {
		case "pending":
			if err := s.markFailed(ctx, tx, row, "cancelled"); err != nil {
				return err
			}
			row.Status = "fail"
			if err := s.reopen(ctx, tx, row, "waiting_for_dispute_to_be_resolved"); err != nil {
				return err
			}
		case "success", "fail":
			if err := s.reopen(ctx, tx, row, "waiting_for_dispute_to_be_resolved"); err != nil {
				return err
			}
		default:
			return fail(400, "Сделку нельзя отправить в спор.")
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO disputes (receipt, order_id, trader_id, status, created_at, updated_at)
			VALUES (?, ?, ?, 'pending', ?, ?)`, emptyAsNil(receipt), row.ID, row.TraderID, s.now(), s.now()); err != nil {
			return err
		}
		return s.guardPending(ctx, tx, row.TraderID)
	})
}

// AcceptDispute pays the merchant and marks the dispute accepted.
func (s *Service) AcceptDispute(ctx context.Context, actor int64, orderUUID string) error {
	if err := s.requireStaff(ctx, actor); err != nil {
		return err
	}
	return s.acceptDispute(ctx, orderUUID, "По сделке пока что небыло споров.")
}

// AcceptDisputeBot pays the merchant. A missing dispute uses the bot message.
func (s *Service) AcceptDisputeBot(ctx context.Context, orderUUID string) error {
	return s.acceptDispute(ctx, orderUUID, "Dispute not found.")
}

func (s *Service) acceptDispute(ctx context.Context, orderUUID, missing string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		row, disputeID, err := s.lockPendingDispute(ctx, tx, orderUUID, missing)
		if err != nil {
			return err
		}
		if row.Status != "pending" {
			if err := s.reopen(ctx, tx, row, "waiting_for_dispute_to_be_resolved"); err != nil {
				return err
			}
		}
		if err := s.markSuccess(ctx, tx, row, "successfully_paid_by_resolved_dispute"); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE disputes SET status = 'accepted', updated_at = ? WHERE id = ?`, s.now(), disputeID)
		return err
	})
}

// CancelDispute fails the order with the support reason.
func (s *Service) CancelDispute(ctx context.Context, actor int64, orderUUID, reason, comment string) error {
	if err := s.requireStaff(ctx, actor); err != nil {
		return err
	}
	return s.cancelDispute(ctx, orderUUID, reason, comment, true, "По сделке пока что небыло споров.")
}

// CancelDisputeBot uses the bot reason list, which omits cancelled_by_provider.
func (s *Service) CancelDisputeBot(ctx context.Context, orderUUID, reason, comment string) error {
	return s.cancelDispute(ctx, orderUUID, reason, comment, false, "Dispute not found.")
}

func (s *Service) cancelDispute(ctx context.Context, orderUUID, reason, comment string, allowProvider bool, missing string) error {
	if utf8.RuneCountInString(comment) > 500 {
		return invalid(map[string][]string{"comment": {"The comment field must not be greater than 500 characters."}})
	}
	if !validCancelReason(reason, allowProvider) {
		return invalid(map[string][]string{"cancel_reason": {"The selected cancel reason is invalid."}})
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		row, disputeID, err := s.lockPendingDispute(ctx, tx, orderUUID, missing)
		if err != nil {
			return err
		}
		if row.Status != "pending" {
			if err := s.reopen(ctx, tx, row, "waiting_for_dispute_to_be_resolved"); err != nil {
				return err
			}
			row.Status = "pending"
		}
		if err := s.markFailed(ctx, tx, row, "canceled_by_dispute"); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `
			UPDATE disputes SET status = 'canceled', cancel_reason = ?, reason = ?, updated_at = ? WHERE id = ?`,
			reason, emptyAsNil(comment), s.now(), disputeID); err != nil {
			return err
		}
		return s.guardRejected(ctx, tx, row.TraderID)
	})
}

// RollbackDispute returns a resolved dispute to pending and reopens the order.
func (s *Service) RollbackDispute(ctx context.Context, actor int64, orderUUID string) error {
	if err := s.requireStaff(ctx, actor); err != nil {
		return err
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		row, err := lockOrder(ctx, tx, orderUUID)
		if err != nil {
			return err
		}
		var disputeID int64
		var status string
		err = tx.QueryRowContext(ctx, `SELECT id, status FROM disputes WHERE order_id = ? FOR UPDATE`, row.ID).Scan(&disputeID, &status)
		if err == sql.ErrNoRows {
			return fail(400, "По сделке пока что небыло споров.")
		}
		if err != nil {
			return err
		}
		if status == "pending" {
			return fail(400, "Cannot rollback pending dispute.")
		}
		if err := s.reopen(ctx, tx, row, "waiting_for_dispute_to_be_resolved"); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE disputes SET status = 'pending', reason = NULL, cancel_reason = NULL, updated_at = ? WHERE id = ?`,
			s.now(), disputeID)
		return err
	})
}

// SupportAcceptOrder confirms payment unless a dispute is already open.
func (s *Service) SupportAcceptOrder(ctx context.Context, actor int64, orderUUID string) (string, error) {
	if err := s.requireStaff(ctx, actor); err != nil {
		return "", err
	}
	message := "Заказ подтверждён как оплаченный"
	err := s.tx(ctx, func(tx *sql.Tx) error {
		row, err := lockOrder(ctx, tx, orderUUID)
		if err != nil {
			return err
		}
		var disputeID int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM disputes WHERE order_id = ? LIMIT 1`, row.ID).Scan(&disputeID)
		if err == nil {
			return fail(400, "По заказу открыт спор, подтверждение оплаты невозможно.")
		}
		if err != sql.ErrNoRows {
			return err
		}
		if row.Status == "success" {
			message = "Заказ уже успешно оплачен"
			return nil
		}
		if row.Status == "fail" {
			if err := s.reopen(ctx, tx, row, "waiting_for_payment"); err != nil {
				return err
			}
		}
		return s.markSuccess(ctx, tx, row, "accepted")
	})
	return message, err
}

// PayoutsEnabled reports the merchant flag used by the payout menu.
func (s *Service) PayoutsEnabled(ctx context.Context, userID int64) (bool, error) {
	var enabled bool
	err := s.DB.QueryRowContext(ctx, `SELECT payouts_enabled FROM users WHERE id = ?`, userID).Scan(&enabled)
	return enabled, err
}

func (s *Service) lockPendingDispute(ctx context.Context, tx *sql.Tx, orderUUID, missing string) (orderLock, int64, error) {
	row, err := lockOrder(ctx, tx, orderUUID)
	if err != nil {
		return row, 0, err
	}
	var disputeID int64
	var status string
	err = tx.QueryRowContext(ctx, `SELECT id, status FROM disputes WHERE order_id = ? FOR UPDATE`, row.ID).Scan(&disputeID, &status)
	if err == sql.ErrNoRows {
		return row, 0, fail(400, missing)
	}
	if err != nil {
		return row, 0, err
	}
	if status != "pending" {
		return row, 0, fail(400, "Dispute must be pending.")
	}
	return row, disputeID, nil
}

func validCancelReason(reason string, allowProvider bool) bool {
	switch reason {
	case "requires_bank_statement", "requires_video_proof", "wrong_payment_refund_required", "incorrect_amount_received":
		return true
	case "cancelled_by_provider":
		return allowProvider
	default:
		return false
	}
}

func (s *Service) requireStaff(ctx context.Context, userID int64) error {
	for _, role := range []string{"Support", "Admin"} {
		ok, err := hasRole(ctx, s.DB, userID, role)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return plain(403, "This action is unauthorized.")
}

func (s *Service) guardPending(ctx context.Context, tx *sql.Tx, traderID int64) error {
	if traderID == 0 {
		return nil
	}
	var raw sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE `key` = 'max_pending_disputes'").Scan(&raw)
	if err == sql.ErrNoRows || !raw.Valid {
		return nil
	}
	if err != nil {
		return err
	}
	max, err := strconv.Atoi(strings.TrimSpace(raw.String))
	if err != nil || max <= 0 {
		return nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM disputes WHERE trader_id = ? AND status = 'pending'`, traderID).Scan(&count); err != nil {
		return err
	}
	if count < max {
		return nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE users SET stop_traffic = 1, updated_at = ? WHERE id = ?`, s.now(), traderID)
	return err
}

func (s *Service) guardRejected(ctx context.Context, tx *sql.Tx, traderID int64) error {
	if traderID == 0 {
		return nil
	}
	var raw sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE `key` = 'max_rejected_disputes'").Scan(&raw)
	if err == sql.ErrNoRows || !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	if err != nil {
		return err
	}
	var limit struct {
		Count  int `json:"count"`
		Period int `json:"period"`
	}
	if json.Unmarshal([]byte(raw.String), &limit) != nil || limit.Count <= 0 || limit.Period <= 0 {
		return nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM disputes
		WHERE trader_id = ? AND status = 'canceled'
		  AND created_at >= GREATEST(
		    DATE_SUB(NOW(), INTERVAL ? MINUTE),
		    COALESCE((SELECT traffic_enabled_at FROM users WHERE id = ?), DATE_SUB(NOW(), INTERVAL ? MINUTE))
		  )`, traderID, limit.Period, traderID, limit.Period).Scan(&count); err != nil {
		return err
	}
	if count < limit.Count {
		return nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE users SET stop_traffic = 1, updated_at = ? WHERE id = ?`, s.now(), traderID)
	return err
}

func emptyAsNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}
