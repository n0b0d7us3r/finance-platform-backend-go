package flow

import (
	"context"
	"database/sql"
	"net/mail"
	"regexp"
	"strconv"
	"strings"

	"finance-platform/go/internal/money"
)

var (
	tronAddress = regexp.MustCompile(`^T.{33}$`)
	evmAddress  = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
)

// DepositWebhook credits trust once for a provider transaction id.
func (s *Service) DepositWebhook(ctx context.Context, email, amount, transactionID, txHash string) error {
	fields := map[string][]string{}
	email = strings.TrimSpace(email)
	amount = strings.TrimSpace(amount)
	transactionID = strings.TrimSpace(transactionID)
	if email == "" {
		fields["email"] = []string{"The email field is required."}
	} else if _, err := mail.ParseAddress(email); err != nil {
		fields["email"] = []string{"The email field must be a valid email address."}
	}
	if transactionID == "" {
		fields["transaction_id"] = []string{"The transaction id field is required."}
	}
	value, amountErr := money.FromPrecision(amount, "usdt")
	if amount == "" {
		fields["amount"] = []string{"The amount field is required."}
	} else if amountErr != nil {
		fields["amount"] = []string{"The amount field must be a number."}
	} else {
		min, _ := money.FromPrecision("1", "usdt")
		ok, err := value.GreaterOrEqual(min)
		if err != nil || !ok {
			fields["amount"] = []string{"The amount field must be at least 1."}
		}
	}
	if len(fields) > 0 {
		return invalid(fields)
	}
	var userID int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, email).Scan(&userID)
	if err == sql.ErrNoRows {
		return invalid(map[string][]string{"email": {"The selected email is invalid."}})
	}
	if err != nil {
		return err
	}
	now := s.now()
	return s.tx(ctx, func(tx *sql.Tx) error {
		var walletID int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM wallets WHERE user_id = ? FOR UPDATE`, userID).Scan(&walletID)
		if err == sql.ErrNoRows {
			return fail(400, "Кошелёк не найден.")
		}
		if err != nil {
			return err
		}
		var existing int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM invoices WHERE transaction_id = ? LIMIT 1`, transactionID).Scan(&existing)
		if err == nil {
			return fail(400, "Инвойс с таким transaction id уже существует.")
		}
		if err != sql.ErrNoRows {
			return err
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO invoices (
				amount, currency, type, balance_type, status, transaction_id, tx_hash, wallet_id, created_at, updated_at
			) VALUES (?, 'usdt', 'deposit', 'trust', 'success', ?, ?, ?, ?, ?)`,
			value.Units(), transactionID, emptyAsNil(txHash), walletID, now, now); err != nil {
			return err
		}
		return creditTrustKind(ctx, tx, userID, value.Units(), "deposit_by_user", now)
	})
}

// WithdrawWebhook closes an automatic withdrawal. Success keeps the debit. Fail returns it once.
func (s *Service) WithdrawWebhook(ctx context.Context, paymentID int64, status, txHash string) (map[string]any, error) {
	if status != "success" && status != "fail" {
		return nil, invalid(map[string][]string{"status": {"The selected status is invalid."}})
	}
	now := s.now()
	var view map[string]any
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var (
			invoiceID   int64
			external    sql.NullString
			kind        string
			state       string
			balanceType string
			units       string
			walletID    int64
			storedHash  sql.NullString
		)
		err := tx.QueryRowContext(ctx, `
			SELECT id, external_id, type, status, balance_type, amount, wallet_id, tx_hash
			FROM invoices WHERE id = ? FOR UPDATE`, paymentID).Scan(
			&invoiceID, &external, &kind, &state, &balanceType, &units, &walletID, &storedHash)
		if err == sql.ErrNoRows {
			return invalid(map[string][]string{"payment_id": {"The selected payment id is invalid."}})
		}
		if err != nil {
			return err
		}
		if !external.Valid || external.String == "" {
			return fail(400, "Данный метод поддерживает только инвойсы для автовывода.")
		}
		if kind != "withdrawal" {
			return fail(400, "Неверный тип инвойса.")
		}
		if state != "pending" {
			return fail(400, "Инвойс уже завершен.")
		}
		next := "fail"
		if status == "success" {
			next = "success"
		}
		if _, err = tx.ExecContext(ctx, `
			UPDATE invoices SET status = ?, tx_hash = ?, updated_at = ? WHERE id = ?`,
			next, emptyAsNil(txHash), now, invoiceID); err != nil {
			return err
		}
		if status == "fail" {
			var userID int64
			if err = tx.QueryRowContext(ctx, `SELECT user_id FROM wallets WHERE id = ?`, walletID).Scan(&userID); err != nil {
				return err
			}
			if err = giveBalance(ctx, tx, walletID, units, "rollback_for_user_withdrawal", balanceType, now); err != nil {
				return err
			}
		}
		hash := txHash
		if hash == "" && storedHash.Valid {
			hash = storedHash.String
		}
		view = map[string]any{"payment_id": invoiceID, "tx_hash": emptyString(hash), "status": next}
		return nil
	})
	return view, err
}

// CreateAutoWithdrawal debits the merchant balance and leaves a pending invoice.
// A local stand without a withdrawal provider records its own external id.
func (s *Service) CreateAutoWithdrawal(ctx context.Context, userID int64, amount, address, network string) (int64, string, error) {
	fields := map[string][]string{}
	amount = strings.TrimSpace(amount)
	address = strings.TrimSpace(address)
	network = strings.ToLower(strings.TrimSpace(network))
	if amount == "" {
		fields["amount"] = []string{"The amount field is required."}
	}
	if address == "" {
		fields["address"] = []string{"The address field is required."}
	}
	if network == "" {
		fields["network"] = []string{"The network field is required."}
	} else if !knownNetwork(network) {
		fields["network"] = []string{"The selected network is invalid."}
	}
	value, err := money.FromPrecision(amount, "usdt")
	if amount != "" && err != nil {
		fields["amount"] = []string{"The amount field must be an integer."}
	} else if err == nil && strings.Contains(amount, ".") {
		fields["amount"] = []string{"The amount field must be an integer."}
	} else if err == nil {
		min, _ := money.FromPrecision("5000", "usdt")
		ok, cmpErr := value.GreaterOrEqual(min)
		if cmpErr != nil || !ok {
			fields["amount"] = []string{"The amount field must be at least 5000."}
		}
	}
	if address != "" && network != "" && knownNetwork(network) && !validUSDTAddress(address, network) {
		fields["address"] = []string{"Неверный USDT адрес: " + address + " в сети: " + network + "."}
	}
	if len(fields) > 0 {
		return 0, "", invalid(fields)
	}
	if !s.LocalWithdraw {
		return 0, "", fail(400, "Неизвестная ошибка. Обратитесь к администратору.")
	}
	now := s.now()
	var invoiceID int64
	err = s.tx(ctx, func(tx *sql.Tx) error {
		row, err := lockWallet(ctx, tx, userID)
		if err != nil {
			return err
		}
		have, err := money.FromUnits(zero(row.Merchant), "usdt")
		if err != nil {
			return err
		}
		short, err := have.LessThan(value)
		if err != nil {
			return err
		}
		if short {
			return fail(400, "Недостаточно средств на балансе.")
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO invoices (
				amount, currency, address, network, type, balance_type, status, wallet_id, created_at, updated_at
			) VALUES (?, 'usdt', ?, ?, 'withdrawal', 'merchant', 'pending', ?, ?, ?)`,
			value.Units(), address, network, row.ID, now, now)
		if err != nil {
			return err
		}
		invoiceID, err = res.LastInsertId()
		if err != nil {
			return err
		}
		external := "local-" + strconv.FormatInt(invoiceID, 10)
		if _, err = tx.ExecContext(ctx, `UPDATE invoices SET external_id = ? WHERE id = ?`, external, invoiceID); err != nil {
			return err
		}
		return takeMerchant(ctx, tx, userID, value.Units(), "withdrawal_by_user", now)
	})
	return invoiceID, "pending", err
}

func knownNetwork(network string) bool {
	switch network {
	case "eth", "bsc", "arb", "avax", "matic", "trx":
		return true
	default:
		return false
	}
}

func validUSDTAddress(address, network string) bool {
	switch network {
	case "bsc", "arb":
		return evmAddress.MatchString(address)
	case "trx":
		return len(address) == 34 && strings.HasPrefix(address, "T") && tronAddress.MatchString(address)
	default:
		return false
	}
}

func emptyString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
