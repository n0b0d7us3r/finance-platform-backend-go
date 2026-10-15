package flow

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"finance-platform/go/internal/money"
)

type walletRow struct {
	ID         int64
	Trust      string
	Reserve    string
	Merchant   string
	Teamleader string
	Target     int
}

func lockWallet(ctx context.Context, tx *sql.Tx, userID int64) (walletRow, error) {
	var row walletRow
	err := tx.QueryRowContext(ctx, `
		SELECT id, trust_balance, reserve_balance, merchant_balance,
		       COALESCE(teamleader_balance, '0'), target_reserve_amount
		FROM wallets WHERE user_id = ? FOR UPDATE`, userID).
		Scan(&row.ID, &row.Trust, &row.Reserve, &row.Merchant, &row.Teamleader, &row.Target)
	if err == sql.ErrNoRows {
		return row, fail(400, "Кошелёк не найден.")
	}
	return row, err
}

func saveBalance(ctx context.Context, tx *sql.Tx, id int64, trust, reserve, merchant string, now interface{}) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE wallets
		SET trust_balance = ?, reserve_balance = ?, merchant_balance = ?, updated_at = ?
		WHERE id = ?`, trust, reserve, merchant, now, id)
	return err
}

func creditTrust(ctx context.Context, tx *sql.Tx, userID int64, units string, now interface{}) error {
	return creditTrustKind(ctx, tx, userID, units, "deposit_by_admin", now)
}

func creditTrustKind(ctx context.Context, tx *sql.Tx, userID int64, units, kind string, now interface{}) error {
	row, err := lockWallet(ctx, tx, userID)
	if err != nil {
		return err
	}
	add, err := money.FromUnits(zero(units), "usdt")
	if err != nil {
		return err
	}
	trust, err := money.FromUnits(zero(row.Trust), "usdt")
	if err != nil {
		return err
	}
	reserve, err := money.FromUnits(zero(row.Reserve), "usdt")
	if err != nil {
		return err
	}
	target, err := money.FromPrecision(strconv.Itoa(row.Target), "usdt")
	if err != nil {
		return err
	}
	short, err := reserve.LessThan(target)
	if err != nil {
		return err
	}
	if short {
		deficit, err := target.SubMoney(reserve)
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
			reserve = target
		}
	} else {
		trust, err = trust.AddMoney(add)
		if err != nil {
			return err
		}
	}
	if err := saveBalance(ctx, tx, row.ID, trust.Units(), reserve.Units(), zero(row.Merchant), now); err != nil {
		return err
	}
	return postEntry(ctx, tx, row.ID, zero(units), "in", kind, "trust", now)
}

func creditMerchant(ctx context.Context, tx *sql.Tx, userID int64, units, kind string, now interface{}) error {
	row, err := lockWallet(ctx, tx, userID)
	if err != nil {
		return err
	}
	current, err := money.FromUnits(zero(row.Merchant), "usdt")
	if err != nil {
		return err
	}
	add, err := money.FromUnits(zero(units), "usdt")
	if err != nil {
		return err
	}
	next, err := current.AddMoney(add)
	if err != nil {
		return err
	}
	if err := saveBalance(ctx, tx, row.ID, zero(row.Trust), zero(row.Reserve), next.Units(), now); err != nil {
		return err
	}
	return postEntry(ctx, tx, row.ID, zero(units), "in", kind, "merchant", now)
}

func takeMerchant(ctx context.Context, tx *sql.Tx, userID int64, units, kind string, now interface{}) error {
	row, err := lockWallet(ctx, tx, userID)
	if err != nil {
		return err
	}
	current, err := money.FromUnits(zero(row.Merchant), "usdt")
	if err != nil {
		return err
	}
	sub, err := money.FromUnits(zero(units), "usdt")
	if err != nil {
		return err
	}
	next, err := current.SubMoney(sub)
	if err != nil {
		return err
	}
	negative, err := next.LessThanZero()
	if err != nil {
		return err
	}
	if negative {
		return fail(400, "Недостаточно средств на балансе мерчанта.")
	}
	if err := saveBalance(ctx, tx, row.ID, zero(row.Trust), zero(row.Reserve), next.Units(), now); err != nil {
		return err
	}
	return postEntry(ctx, tx, row.ID, zero(units), "out", kind, "merchant", now)
}

func takeTrust(ctx context.Context, tx *sql.Tx, userID int64, units string, now interface{}) error {
	row, err := lockWallet(ctx, tx, userID)
	if err != nil {
		return err
	}
	trust, err := money.FromUnits(zero(row.Trust), "usdt")
	if err != nil {
		return err
	}
	reserve, err := money.FromUnits(zero(row.Reserve), "usdt")
	if err != nil {
		return err
	}
	sub, err := money.FromUnits(zero(units), "usdt")
	if err != nil {
		return err
	}
	next, err := trust.SubMoney(sub)
	if err != nil {
		return err
	}
	negative, err := next.LessThanZero()
	if err != nil {
		return err
	}
	if negative {
		abs, err := next.Abs()
		if err != nil {
			return err
		}
		reserve, err = reserve.SubMoney(abs)
		if err != nil {
			return err
		}
		below, err := reserve.LessThanZero()
		if err != nil {
			return err
		}
		if below {
			return fail(400, "Недостаточно трастового баланса.")
		}
		next, err = money.Zero("usdt")
		if err != nil {
			return err
		}
	}
	if err := saveBalance(ctx, tx, row.ID, next.Units(), reserve.Units(), zero(row.Merchant), now); err != nil {
		return err
	}
	return postEntry(ctx, tx, row.ID, zero(units), "out", "payment_for_opened_order", "trust", now)
}

func refundTrust(ctx context.Context, tx *sql.Tx, userID int64, units string, now interface{}) error {
	row, err := lockWallet(ctx, tx, userID)
	if err != nil {
		return err
	}
	trust, err := money.FromUnits(zero(row.Trust), "usdt")
	if err != nil {
		return err
	}
	add, err := money.FromUnits(zero(units), "usdt")
	if err != nil {
		return err
	}
	next, err := trust.AddMoney(add)
	if err != nil {
		return err
	}
	if err := saveBalance(ctx, tx, row.ID, next.Units(), zero(row.Reserve), zero(row.Merchant), now); err != nil {
		return err
	}
	return postEntry(ctx, tx, row.ID, zero(units), "in", "refund_for_canceled_order", "trust", now)
}

func moveTeamleader(ctx context.Context, tx *sql.Tx, userID int64, units string, add bool, now interface{}) error {
	row, err := lockWallet(ctx, tx, userID)
	if err != nil {
		var client *Error
		if add && errors.As(err, &client) && client.Message == "Кошелёк не найден." {
			return nil
		}
		return err
	}
	current, err := money.FromUnits(zero(row.Teamleader), "usdt")
	if err != nil {
		return err
	}
	delta, err := money.FromUnits(zero(units), "usdt")
	if err != nil {
		return err
	}
	var next money.Money
	direction := "out"
	kind := "rollback_income_from_referrals_successful_order"
	if add {
		next, err = current.AddMoney(delta)
		direction = "in"
		kind = "income_from_referrals_successful_order"
	} else {
		next, err = current.SubMoney(delta)
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE wallets SET teamleader_balance = ?, updated_at = ? WHERE id = ?`, next.Units(), now, row.ID); err != nil {
		return err
	}
	return postEntry(ctx, tx, row.ID, zero(units), direction, kind, "teamleader", now)
}

func postEntry(ctx context.Context, tx *sql.Tx, walletID int64, units, direction, kind, balance string, now interface{}) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO transactions (amount, currency, direction, type, balance_type, wallet_id, created_at, updated_at)
		VALUES (?, 'usdt', ?, ?, ?, ?, ?, ?)`,
		zero(units), direction, kind, balance, walletID, now, now)
	return err
}
