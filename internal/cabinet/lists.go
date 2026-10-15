package cabinet

import (
	"context"
	"database/sql"
)

// Users is the admin and support user table.
func (r *Reader) Users(ctx context.Context, page Page, search, role string, online, traffic string) (map[string]any, error) {
	where := "1=1"
	var args []any
	if search != "" {
		where += " AND (u.name LIKE ? OR u.email LIKE ?)"
		args = append(args, "%"+search+"%", "%"+search+"%")
	}
	if role != "" {
		where += " AND r.name = ?"
		args = append(args, role)
	}
	switch online {
	case "online":
		where += " AND u.is_online = 1"
	case "offline":
		where += " AND u.is_online = 0"
	}
	switch traffic {
	case "active":
		where += " AND u.stop_traffic = 0 AND u.banned_at IS NULL"
	case "stopped":
		where += " AND u.stop_traffic = 1 AND u.banned_at IS NULL"
	case "banned":
		where += " AND u.banned_at IS NOT NULL"
	}
	from := `FROM users u
		LEFT JOIN user_roles ur ON ur.user_id = u.id
		LEFT JOIN roles r ON r.id = ur.role_id
		LEFT JOIN wallets w ON w.user_id = u.id
		WHERE ` + where
	total, err := r.count(ctx, `SELECT COUNT(DISTINCT u.id) `+from, args...)
	if err != nil {
		return nil, err
	}
	listArgs := append(append([]any{}, args...), page.Size, page.offset())
	rows, err := r.DB.QueryContext(ctx, `
		SELECT u.id, u.name, u.email, u.banned_at, u.created_at, u.stop_traffic, u.is_online, u.is_payout_online,
		       MAX(r.id), MAX(r.name),
		       MAX(CASE r.name
		         WHEN 'Merchant' THEN COALESCE(w.merchant_balance, '0')
		         WHEN 'Team Leader' THEN COALESCE(w.teamleader_balance, '0')
		         ELSE COALESCE(w.trust_balance, '0')
		       END)
		`+from+` GROUP BY u.id, u.name, u.email, u.banned_at, u.created_at, u.stop_traffic, u.is_online, u.is_payout_online
		ORDER BY u.id DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var (
			id                             int64
			name, email, balance           string
			banned, created                sql.NullTime
			stop, onlineFlag, payoutOnline int
			roleID                         sql.NullInt64
			roleName                       sql.NullString
		)
		if err := rows.Scan(&id, &name, &email, &banned, &created, &stop, &onlineFlag, &payoutOnline, &roleID, &roleName, &balance); err != nil {
			return nil, err
		}
		var role any
		if roleID.Valid {
			role = map[string]any{"id": roleID.Int64, "name": roleName.String}
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "email": email,
			"banned_at": r.date(banned), "created_at": r.date(created),
			"stop_traffic": stop == 1, "is_online": onlineFlag == 1, "is_payout_online": payoutOnline == 1,
			"can_be_impersonated": canImpersonate(roleName.String, banned.Valid),
			"role":                role, "balance": beauty(balance, "usdt"),
		})
	}
	return Block(page, total, items), rows.Err()
}

func canImpersonate(role string, banned bool) bool {
	if banned {
		return false
	}
	switch role {
	case "Merchant", "Trader", "Support", "Merchant Support", "Team Leader":
		return true
	default:
		return false
	}
}

// Disputes is the staff dispute table.
func (r *Reader) Disputes(ctx context.Context, page Page, status string, traderID, merchantUser int64) (map[string]any, error) {
	where := "1=1"
	var args []any
	if status != "" {
		where += " AND d.status = ?"
		args = append(args, status)
	}
	if traderID > 0 {
		where += " AND d.trader_id = ?"
		args = append(args, traderID)
	}
	if merchantUser > 0 {
		where += " AND o.merchant_id IN (SELECT id FROM merchants WHERE user_id = ?)"
		args = append(args, merchantUser)
	}
	from := `FROM disputes d
		LEFT JOIN orders o ON o.id = d.order_id
		LEFT JOIN payment_gateways pg ON pg.id = o.payment_gateway_id
		LEFT JOIN payment_details pd ON pd.id = o.payment_detail_id
		LEFT JOIN users trader ON trader.id = COALESCE(d.trader_id, o.trader_id)
		WHERE ` + where
	total, err := r.count(ctx, `SELECT COUNT(*) `+from, args...)
	if err != nil {
		return nil, err
	}
	listArgs := append(append([]any{}, args...), page.Size, page.offset())
	rows, err := r.DB.QueryContext(ctx, `
		SELECT d.id, d.status, d.reason, d.created_at, o.uuid, o.amount, o.currency,
		       pg.name, pd.detail, trader.name
		`+from+` ORDER BY d.id DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id int64
		var status string
		var reason sql.NullString
		var created sql.NullTime
		var orderUUID, amount, currency sql.NullString
		var gateway, detail, traderName sql.NullString
		if err := rows.Scan(&id, &status, &reason, &created, &orderUUID, &amount, &currency, &gateway, &detail, &traderName); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "status": status, "reason": nullText(reason),
			"created_at": r.when(created),
			"order": map[string]any{
				"uuid":     nullText(orderUUID),
				"amount":   beauty(amount.String, currency.String),
				"currency": currency.String,
			},
			"payment_gateway": map[string]any{"name": nullText(gateway)},
			"payment_detail":  map[string]any{"detail": nullText(detail)},
			"trader":          map[string]any{"name": nullText(traderName)},
		})
	}
	return Block(page, total, items), rows.Err()
}

func nullText(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// Invoices lists deposits or withdrawals.
func (r *Reader) Invoices(ctx context.Context, page Page, kind, status, user string, walletUser int64) (map[string]any, error) {
	where := "i.type = ?"
	args := []any{kind}
	if status != "" {
		where += " AND i.status = ?"
		args = append(args, status)
	}
	if user != "" {
		where += " AND (u.email LIKE ? OR u.name LIKE ?)"
		args = append(args, "%"+user+"%", "%"+user+"%")
	}
	if walletUser > 0 {
		where += " AND w.user_id = ?"
		args = append(args, walletUser)
	}
	from := `FROM invoices i LEFT JOIN wallets w ON w.id = i.wallet_id LEFT JOIN users u ON u.id = w.user_id WHERE ` + where
	total, err := r.count(ctx, `SELECT COUNT(*) `+from, args...)
	if err != nil {
		return nil, err
	}
	listArgs := append(append([]any{}, args...), page.Size, page.offset())
	rows, err := r.DB.QueryContext(ctx, `
		SELECT i.id, i.amount, i.currency, i.status, i.type, i.address, i.created_at, u.email
		`+from+` ORDER BY i.id DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id int64
		var amount, currency, status, kind string
		var address sql.NullString
		var created sql.NullTime
		var email sql.NullString
		if err := rows.Scan(&id, &amount, &currency, &status, &kind, &address, &created, &email); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "amount": beauty(amount, currency), "currency": currency,
			"status": status, "type": kind, "address": nullText(address),
			"created_at": r.when(created),
			"user":       map[string]any{"email": email.String},
		})
	}
	return Block(page, total, items), rows.Err()
}

// Transactions is one wallet ledger page.
func (r *Reader) Transactions(ctx context.Context, page Page, userID int64) (map[string]any, error) {
	where := "w.user_id = ?"
	args := []any{userID}
	from := `FROM transactions t INNER JOIN wallets w ON w.id = t.wallet_id WHERE ` + where
	total, err := r.count(ctx, `SELECT COUNT(*) `+from, args...)
	if err != nil {
		return nil, err
	}
	listArgs := append(append([]any{}, args...), page.Size, page.offset())
	rows, err := r.DB.QueryContext(ctx, `
		SELECT t.id, t.amount, t.currency, t.direction, t.type, t.created_at
		`+from+` ORDER BY t.id DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id int64
		var amount, currency, direction, kind string
		var created sql.NullTime
		if err := rows.Scan(&id, &amount, &currency, &direction, &kind, &created); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "amount": beauty(amount, currency), "currency": currency,
			"direction": direction, "type": kind, "type_name": kind,
			"created_at": r.when(created),
		})
	}
	return Block(page, total, items), rows.Err()
}

// SimpleList pages a query that already selects the display columns.
func (r *Reader) rows(ctx context.Context, page Page, countSQL, listSQL string, args []any, scan func(*sql.Rows) (map[string]any, error)) (map[string]any, error) {
	total, err := r.count(ctx, countSQL, args...)
	if err != nil {
		return nil, err
	}
	listArgs := append(append([]any{}, args...), page.Size, page.offset())
	rows, err := r.DB.QueryContext(ctx, listSQL, listArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return Block(page, total, items), rows.Err()
}
