package flow

import (
	"context"
	"database/sql"
)

// SettleDeposit marks a pending trust deposit successful and credits its wallet once.
func (s *Service) SettleDeposit(ctx context.Context, invoiceID int64, transactionID, units, balanceType string, walletID int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		var status string
		err := tx.QueryRowContext(ctx, `SELECT status FROM invoices WHERE id = ? FOR UPDATE`, invoiceID).Scan(&status)
		if err != nil {
			return err
		}
		if status != "pending" {
			return nil
		}
		var taken int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM invoices WHERE transaction_id = ? AND id <> ?`, transactionID, invoiceID).Scan(&taken); err != nil {
			return err
		}
		if taken > 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE invoices SET status = 'success', transaction_id = ?, updated_at = ? WHERE id = ?`, transactionID, s.now(), invoiceID); err != nil {
			return err
		}
		return giveBalance(ctx, tx, walletID, units, "deposit_by_user", balanceType, s.now())
	})
}
