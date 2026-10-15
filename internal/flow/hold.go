package flow

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"finance-platform/go/internal/money"
)

const holdablePayout = `App\Models\Payout`

// ExecuteDue releases payout holds whose timer is due. A completed row is skipped.
func (s *Service) ExecuteDue(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id FROM funds_on_holds
		WHERE status = 'pending_for_execution' AND hold_until IS NOT NULL AND hold_until <= NOW()
		ORDER BY id
		LIMIT ?`, limit)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
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
	done := 0
	for _, id := range ids {
		err := s.tx(ctx, func(tx *sql.Tx) error {
			return s.executeHold(ctx, tx, id)
		})
		if err != nil {
			continue
		}
		done++
	}
	return done, nil
}

func (s *Service) executeHold(ctx context.Context, tx *sql.Tx, id int64) error {
	var (
		units   string
		status  string
		until   sql.NullTime
		destID  sql.NullInt64
		destBal sql.NullString
	)
	err := tx.QueryRowContext(ctx, `
		SELECT amount, status, hold_until, destination_wallet_id, destination_wallet_balance_type
		FROM funds_on_holds WHERE id = ? FOR UPDATE`, id).Scan(&units, &status, &until, &destID, &destBal)
	if err != nil {
		return err
	}
	if status != "pending_for_execution" {
		return fail(400, "Действие не возможно для текущего статуса.")
	}
	if !until.Valid || until.Time.After(s.now()) {
		return fail(400, "Таймер еще не истек.")
	}
	if !destID.Valid || !destBal.Valid {
		return fail(400, "Действие не возможно для текущего статуса.")
	}
	if err := giveBalance(ctx, tx, destID.Int64, units, "income_from_a_successful_payout", destBal.String, s.now()); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE funds_on_holds SET status = 'completed', updated_at = ? WHERE id = ?`, s.now(), id)
	return err
}

func (s *Service) cancelHold(ctx context.Context, tx *sql.Tx, id int64) error {
	var (
		units  string
		status string
		srcID  int64
		srcBal string
	)
	err := tx.QueryRowContext(ctx, `
		SELECT amount, status, source_wallet_id, source_wallet_balance_type
		FROM funds_on_holds WHERE id = ? FOR UPDATE`, id).Scan(&units, &status, &srcID, &srcBal)
	if err != nil {
		return err
	}
	if status != "timer_not_set" && status != "pending_for_execution" {
		return fail(400, "Действие не возможно для текущего статуса.")
	}
	if err := giveBalance(ctx, tx, srcID, units, "refund_for_canceled_payout", srcBal, s.now()); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE funds_on_holds SET status = 'canceled', updated_at = ? WHERE id = ?`, s.now(), id)
	return err
}

func (s *Service) openHold(ctx context.Context, tx *sql.Tx, units string, sourceID int64, sourceBal string, destID int64, destBal string, payoutID int64, until *time.Time) error {
	if err := takeBalance(ctx, tx, sourceID, units, "payment_for_opened_payout", sourceBal, s.now()); err != nil {
		return err
	}
	status := "timer_not_set"
	var timer any
	if until != nil {
		status = "pending_for_execution"
		timer = *until
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO funds_on_holds (
			amount, currency, source_wallet_id, source_wallet_balance_type,
			destination_wallet_id, destination_wallet_balance_type,
			holdable_id, holdable_type, status, hold_until, created_at, updated_at
		) VALUES (?, 'usdt', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		units, sourceID, sourceBal, destID, destBal, payoutID, holdablePayout, status, timer, s.now(), s.now())
	return err
}

func giveBalance(ctx context.Context, tx *sql.Tx, walletID int64, units, kind, balanceType string, now interface{}) error {
	switch balanceType {
	case "trust":
		return giveTrustWallet(ctx, tx, walletID, units, kind, now)
	case "merchant":
		return addColumn(ctx, tx, walletID, "merchant_balance", units, true, kind, "merchant", now)
	case "commission":
		return addColumn(ctx, tx, walletID, "commission_balance", units, true, kind, "commission", now)
	case "teamleader":
		return addColumn(ctx, tx, walletID, "teamleader_balance", units, true, kind, "teamleader", now)
	default:
		return fail(400, "Действие не возможно для текущего статуса.")
	}
}

func takeBalance(ctx context.Context, tx *sql.Tx, walletID int64, units, kind, balanceType string, now interface{}) error {
	switch balanceType {
	case "merchant":
		return addColumn(ctx, tx, walletID, "merchant_balance", units, false, kind, "merchant", now)
	case "trust":
		return addColumn(ctx, tx, walletID, "trust_balance", units, false, kind, "trust", now)
	case "commission":
		return addColumn(ctx, tx, walletID, "commission_balance", units, false, kind, "commission", now)
	default:
		return fail(400, "Действие не возможно для текущего статуса.")
	}
}

func addColumn(ctx context.Context, tx *sql.Tx, walletID int64, column, units string, add bool, kind, balanceType string, now interface{}) error {
	var current sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT `+column+` FROM wallets WHERE id = ? FOR UPDATE`, walletID).Scan(&current)
	if err == sql.ErrNoRows {
		return fail(400, "Кошелёк не найден.")
	}
	if err != nil {
		return err
	}
	have, err := money.FromUnits(zero(current.String), "usdt")
	if err != nil {
		return err
	}
	delta, err := money.FromUnits(zero(units), "usdt")
	if err != nil {
		return err
	}
	direction := "out"
	next := have
	if add {
		next, err = have.AddMoney(delta)
		direction = "in"
	} else {
		next, err = have.SubMoney(delta)
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE wallets SET `+column+` = ?, updated_at = ? WHERE id = ?`, next.Units(), now, walletID); err != nil {
		return err
	}
	return postEntry(ctx, tx, walletID, zero(units), direction, kind, balanceType, now)
}

func giveTrustWallet(ctx context.Context, tx *sql.Tx, walletID int64, units, kind string, now interface{}) error {
	var trustUnits, reserveUnits, merchantUnits string
	var target int
	err := tx.QueryRowContext(ctx, `
		SELECT trust_balance, reserve_balance, merchant_balance, target_reserve_amount
		FROM wallets WHERE id = ? FOR UPDATE`, walletID).Scan(&trustUnits, &reserveUnits, &merchantUnits, &target)
	if err == sql.ErrNoRows {
		return fail(400, "Кошелёк не найден.")
	}
	if err != nil {
		return err
	}
	add, err := money.FromUnits(zero(units), "usdt")
	if err != nil {
		return err
	}
	trust, err := money.FromUnits(zero(trustUnits), "usdt")
	if err != nil {
		return err
	}
	reserve, err := money.FromUnits(zero(reserveUnits), "usdt")
	if err != nil {
		return err
	}
	goal, err := money.FromPrecision(itoaTarget(target), "usdt")
	if err != nil {
		return err
	}
	short, err := reserve.LessThan(goal)
	if err != nil {
		return err
	}
	if short {
		deficit, err := goal.SubMoney(reserve)
		if err != nil {
			return err
		}
		fits, err := add.LessOrEqual(deficit)
		if err != nil {
			return err
		}
		if fits {
			reserve, err = reserve.AddMoney(add)
			if err != nil {
				return err
			}
		} else {
			part, err := add.SubMoney(deficit)
			if err != nil {
				return err
			}
			trust, err = trust.AddMoney(part)
			if err != nil {
				return err
			}
			reserve = goal
		}
	} else {
		trust, err = trust.AddMoney(add)
		if err != nil {
			return err
		}
	}
	if err := saveBalance(ctx, tx, walletID, trust.Units(), reserve.Units(), zero(merchantUnits), now); err != nil {
		return err
	}
	return postEntry(ctx, tx, walletID, zero(units), "in", kind, "trust", now)
}

func itoaTarget(value int) string {
	if value < 0 {
		value = 0
	}
	return strconv.Itoa(value)
}
