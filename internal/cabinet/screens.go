package cabinet

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"finance-platform/go/internal/market"
	"finance-platform/go/internal/money"
)

// Payouts is the admin and trader payout table.
func (r *Reader) Payouts(ctx context.Context, page Page, traderID, merchantUser int64, status string) (map[string]any, error) {
	where := "1=1"
	var args []any
	if traderID > 0 {
		where += " AND p.trader_id = ?"
		args = append(args, traderID)
	}
	if merchantUser > 0 {
		where += " AND p.owner_id IN (SELECT id FROM merchants WHERE user_id = ?)"
		args = append(args, merchantUser)
	}
	if status != "" {
		where += " AND p.status = ?"
		args = append(args, status)
	}
	from := `FROM payouts p LEFT JOIN users u ON u.id = p.trader_id WHERE ` + where
	total, err := r.count(ctx, `SELECT COUNT(*) `+from, args...)
	if err != nil {
		return nil, err
	}
	listArgs := append(append([]any{}, args...), page.Size, page.offset())
	rows, err := r.DB.QueryContext(ctx, `
		SELECT p.id, p.uuid, COALESCE(p.external_id, ''), COALESCE(p.payout_amount, '0'), COALESCE(p.currency, 'rub'),
		       COALESCE(p.status, ''), COALESCE(p.detail, ''), COALESCE(p.detail_type, ''),
		       COALESCE(p.trader_profit_amount, '0'), p.created_at, u.email
		`+from+` ORDER BY p.id DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id int64
		var uuid, external, amount, currency, status, detail, detailType, profit string
		var created sql.NullTime
		var email sql.NullString
		if err := rows.Scan(&id, &uuid, &external, &amount, &currency, &status, &detail, &detailType, &profit, &created, &email); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "uuid": uuid, "external_id": external,
			"amount": beauty(amount, currency), "currency": currency, "status": status,
			"detail": detail, "detail_type": detailType,
			"trader_profit": beauty(profit, "usdt"),
			"created_at":    r.when(created),
			"trader_email":  email.String,
		})
	}
	return Block(page, total, items), rows.Err()
}

// Merchants is the admin merchant table. owner limits the list to one user's projects.
func (r *Reader) Merchants(ctx context.Context, page Page, search string, owner int64) (map[string]any, error) {
	where := "1=1"
	var args []any
	if owner > 0 {
		where += " AND m.user_id = ?"
		args = append(args, owner)
	}
	if search != "" {
		where += " AND (m.name LIKE ? OR u.email LIKE ?)"
		args = append(args, "%"+search+"%", "%"+search+"%")
	}
	from := `FROM merchants m LEFT JOIN users u ON u.id = m.user_id WHERE ` + where
	total, err := r.count(ctx, `SELECT COUNT(*) `+from, args...)
	if err != nil {
		return nil, err
	}
	listArgs := append(append([]any{}, args...), page.Size, page.offset())
	rows, err := r.DB.QueryContext(ctx, `
		SELECT m.id, m.uuid, COALESCE(m.name, ''), COALESCE(m.description, ''), COALESCE(m.domain, ''), COALESCE(m.active, 0), m.banned_at, m.validated_at, m.created_at, u.email,
		       (SELECT COUNT(*) FROM merchant_trader_category_priorities p WHERE p.merchant_id = m.id),
		       (SELECT COUNT(*) FROM orders o WHERE o.merchant_id = m.id AND o.status = 'success' AND o.created_at >= DATE_SUB(NOW(), INTERVAL 1 DAY)),
		       (SELECT COALESCE(CAST(SUM(CAST(NULLIF(o.service_profit, '') AS DECIMAL(50,0))) AS CHAR), '0') FROM orders o WHERE o.merchant_id = m.id AND o.status = 'success' AND o.created_at >= DATE_SUB(NOW(), INTERVAL 1 DAY)),
		       (SELECT COUNT(*) FROM orders o WHERE o.merchant_id = m.id)
		`+from+` ORDER BY m.id DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id int64
		var uuid, name, description, domain, profit string
		var active, successDay, opened int
		var banned, validated, created sql.NullTime
		var email sql.NullString
		var priorities int
		if err := rows.Scan(&id, &uuid, &name, &description, &domain, &active, &banned, &validated, &created, &email, &priorities, &successDay, &profit, &opened); err != nil {
			return nil, err
		}
		owner := map[string]any{"email": email.String}
		items = append(items, map[string]any{
			"id": id, "uuid": uuid, "name": name, "description": description, "domain": domain, "active": active == 1,
			"banned_at": r.date(banned), "validated_at": r.date(validated), "created_at": r.when(created),
			"user": owner, "owner": owner,
			"priorities_count":              priorities,
			"orders_success_24h_count":      successDay,
			"orders_service_profit_24h_sum": beauty(profit, "usdt"),
			"orders_opened_total_count":     opened,
		})
	}
	return Block(page, total, items), rows.Err()
}

// MerchantCard is the merchant edit modal: owner, market, categories, and the option lists.
func (r *Reader) MerchantCard(ctx context.Context, id int64) (map[string]any, error) {
	var name, domain, callback, marketName, email string
	var wait sql.NullInt64
	var banned sql.NullTime
	err := r.DB.QueryRowContext(ctx, `
		SELECT COALESCE(m.name, ''), COALESCE(m.domain, ''), COALESCE(m.callback_url, ''),
		       COALESCE(m.market, ''), m.max_order_wait_time, m.banned_at, COALESCE(u.email, '')
		FROM merchants m
		LEFT JOIN users u ON u.id = m.user_id
		WHERE m.id = ?`, id).Scan(&name, &domain, &callback, &marketName, &wait, &banned, &email)
	if err != nil {
		return nil, err
	}
	rows, err := r.DB.QueryContext(ctx, `SELECT category_id FROM category_merchant WHERE merchant_id = ? ORDER BY category_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	categories := []int64{}
	for rows.Next() {
		var categoryID int64
		if err := rows.Scan(&categoryID); err != nil {
			return nil, err
		}
		categories = append(categories, categoryID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	options, err := r.CategoryOptions(ctx)
	if err != nil {
		return nil, err
	}
	var waitText any
	if wait.Valid {
		waitText = wait.Int64
	} else {
		waitText = ""
	}
	markets := make([]map[string]any, 0, len(market.Sources))
	for _, source := range market.Sources {
		markets = append(markets, map[string]any{"value": source, "label": source})
	}
	return map[string]any{
		"merchant": map[string]any{
			"id": id, "name": name, "domain": domain, "callback_url": callback,
			"market": marketName, "max_order_wait_time": waitText,
			"categories": categories, "banned_at": r.date(banned),
			"owner": map[string]any{"email": email},
		},
		"markets":    markets,
		"categories": options,
	}, nil
}

// Details is the payment-detail table.
func (r *Reader) Details(ctx context.Context, page Page, traderID int64) (map[string]any, error) {
	where := "1=1"
	var args []any
	if traderID > 0 {
		where += " AND pd.user_id = ?"
		args = append(args, traderID)
	}
	from := `FROM payment_details pd LEFT JOIN users u ON u.id = pd.user_id WHERE ` + where
	total, err := r.count(ctx, `SELECT COUNT(*) `+from, args...)
	if err != nil {
		return nil, err
	}
	listArgs := append(append([]any{}, args...), page.Size, page.offset())
	rows, err := r.DB.QueryContext(ctx, `
		SELECT pd.id, COALESCE(pd.name, ''), COALESCE(pd.detail, ''), COALESCE(pd.detail_type, ''), COALESCE(pd.initials, ''),
		       pd.is_active, COALESCE(pd.daily_limit, '0'), COALESCE(pd.current_daily_limit, '0'),
		       COALESCE(pd.min_order_amount, ''), COALESCE(pd.max_order_amount, ''),
		       pd.max_pending_orders_quantity, COALESCE(pd.unique_amount_percentage, 0), COALESCE(pd.unique_amount_seconds, 0),
		       COALESCE(pd.currency, ''), pd.created_at, COALESCE(u.name, ''), COALESCE(u.email, ''),
		       (SELECT pg.name FROM payment_detail_payment_gateway link
		         INNER JOIN payment_gateways pg ON pg.id = link.payment_gateway_id
		         WHERE link.payment_detail_id = pd.id ORDER BY pg.id LIMIT 1)
		`+from+` ORDER BY pd.id DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id int64
		var name, detail, detailType, initials, limit, used, minAmount, maxAmount, currency, ownerName, ownerEmail string
		var active, pendingMax, uniqueSeconds int
		var uniquePercent float64
		var created sql.NullTime
		var gateway sql.NullString
		if err := rows.Scan(
			&id, &name, &detail, &detailType, &initials, &active, &limit, &used,
			&minAmount, &maxAmount, &pendingMax, &uniquePercent, &uniqueSeconds,
			&currency, &created, &ownerName, &ownerEmail, &gateway,
		); err != nil {
			return nil, err
		}
		code := strings.ToLower(strings.TrimSpace(currency))
		if !money.IsKnown(code) {
			code = "rub"
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "detail": detail, "detail_type": detailType, "initials": initials,
			"is_active":   active == 1,
			"daily_limit": beauty(limit, code), "current_daily_limit": beauty(used, code),
			"min_order_amount": beauty(minAmount, code), "min_order_amount_raw": minAmount,
			"max_order_amount": beauty(maxAmount, code), "max_order_amount_raw": maxAmount,
			"max_pending_orders_quantity": pendingMax,
			"unique_amount_percentage":    uniquePercent,
			"unique_amount_seconds":       uniqueSeconds,
			"created_at":                  r.when(created),
			"owner_name":                  ownerName,
			"owner_email":                 ownerEmail,
			"trader_email":                ownerEmail,
			"payment_gateway_name":        gateway.String,
		})
	}
	return Block(page, total, items), rows.Err()
}

const gatewaySelect = `SELECT id, COALESCE(name, ''), COALESCE(code, ''), COALESCE(nspk_schema, ''), COALESCE(currency, ''),
		COALESCE(min_limit, '0'), COALESCE(max_limit, '0'), COALESCE(sms_senders, '[]'),
		trader_commission_rate_for_orders, trader_commission_rate_for_payouts,
		total_service_commission_rate_for_orders, total_service_commission_rate_for_payouts,
		is_active, is_intrabank, COALESCE(detail_types, '[]'),
		reservation_time_for_orders, reservation_time_for_payouts
	FROM payment_gateways`

// Gateways lists payment methods with the fields the edit form reads.
func (r *Reader) Gateways(ctx context.Context, page Page) (map[string]any, error) {
	return r.named(ctx, page,
		`SELECT COUNT(*) FROM payment_gateways`,
		gatewaySelect+` ORDER BY id LIMIT ? OFFSET ?`,
		scanGateway)
}

// Gateway is one payment method for the edit modal.
func (r *Reader) Gateway(ctx context.Context, id int64) (map[string]any, error) {
	row := r.DB.QueryRowContext(ctx, gatewaySelect+` WHERE id = ?`, id)
	item, err := scanGatewayRow(row)
	if err != nil {
		return nil, err
	}
	return item, nil
}

func scanGateway(rows *sql.Rows) (map[string]any, error) {
	return scanGatewayRow(rows)
}

type gatewayScanner interface {
	Scan(dest ...any) error
}

func scanGatewayRow(row gatewayScanner) (map[string]any, error) {
	var id int64
	var name, code, nspk, currency, minLimit, maxLimit, senders, details string
	var traderOrders, traderPayouts, serviceOrders, servicePayouts float64
	var active, intrabank, reserveOrders, reservePayouts int
	if err := row.Scan(
		&id, &name, &code, &nspk, &currency, &minLimit, &maxLimit, &senders,
		&traderOrders, &traderPayouts, &serviceOrders, &servicePayouts,
		&active, &intrabank, &details, &reserveOrders, &reservePayouts,
	); err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "name": name, "original_name": name, "code": code, "nspk_schema": nspk,
		"currency": currency, "min_limit": wholeNumber(minLimit), "max_limit": wholeNumber(maxLimit),
		"sms_senders":                               stringList(senders),
		"trader_commission_rate_for_orders":         traderOrders,
		"trader_commission_rate_for_payouts":        traderPayouts,
		"total_service_commission_rate_for_orders":  serviceOrders,
		"total_service_commission_rate_for_payouts": servicePayouts,
		"is_active": active == 1, "is_intrabank": intrabank == 1,
		"detail_types":                 stringList(details),
		"reservation_time_for_orders":  reserveOrders,
		"reservation_time_for_payouts": reservePayouts,
	}, nil
}

func stringList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return []string{}
	}
	var items []string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return []string{}
	}
	if items == nil {
		return []string{}
	}
	return items
}

func wholeNumber(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if value, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return value
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0
	}
	return int64(parsed)
}

// Devices lists trader devices. The device token stays on the row and is not returned.
func (r *Reader) Devices(ctx context.Context, page Page, traderID int64) (map[string]any, error) {
	where := "1=1"
	var args []any
	if traderID > 0 {
		where += " AND d.user_id = ?"
		args = append(args, traderID)
	}
	from := `FROM user_devices d LEFT JOIN users u ON u.id = d.user_id WHERE ` + where
	total, err := r.count(ctx, `SELECT COUNT(*) `+from, args...)
	if err != nil {
		return nil, err
	}
	listArgs := append(append([]any{}, args...), page.Size, page.offset())
	rows, err := r.DB.QueryContext(ctx, `
		SELECT d.id, d.name, d.device_model, d.android_version, d.connected_at, d.created_at, u.email
		`+from+` ORDER BY d.id DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id int64
		var name string
		var model, version, email sql.NullString
		var connected, created sql.NullTime
		if err := rows.Scan(&id, &name, &model, &version, &connected, &created, &email); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "device_model": nullText(model), "android_version": nullText(version),
			"connected_at": r.when(connected), "created_at": r.when(created),
			"user": map[string]any{"email": email.String},
		})
	}
	return Block(page, total, items), rows.Err()
}

func (r *Reader) named(ctx context.Context, page Page, countSQL, listSQL string, scan func(*sql.Rows) (map[string]any, error)) (map[string]any, error) {
	return r.rows(ctx, page, countSQL, listSQL, nil, scan)
}

// Settings is the admin settings card.
func (r *Reader) Settings(ctx context.Context) (map[string]any, error) {
	rows, err := r.DB.QueryContext(ctx, "SELECT `key`, `value` FROM settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{
		"primeTimeBonus": map[string]any{
			"starts": values["prime_time_bonus_starts"],
			"ends":   values["prime_time_bonus_ends"],
			"rate":   values["prime_time_bonus_rate"],
		},
		"supportLink":                values["support_link"],
		"fundsOnHoldTime":            values["funds_on_hold_time"],
		"maxPendingDisputes":         values["max_pending_disputes"],
		"depositLink":                values["deposit_link"],
		"maxConsecutiveFailedOrders": values["max_consecutive_failed_orders"],
	}, nil
}

// AdminDashboard is the staff overview. period is hour, day, week, or all.
// merchantID filters orders by merchants.id; 0 means every project.
func (r *Reader) AdminDashboard(ctx context.Context, period string, merchantID int64) (map[string]any, error) {
	if merchantID < 0 {
		merchantID = 0
	}
	orderWhere, orderArgs := dashboardOrderWhere(period, merchantID)
	var total, success, failed, pending int
	var profit, turnover sql.NullString
	err := r.DB.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(o.status = 'success'), 0),
		       COALESCE(SUM(o.status = 'fail'), 0),
		       COALESCE(SUM(o.status = 'pending'), 0),
		       COALESCE(CAST(SUM(CASE WHEN o.status = 'success' THEN CAST(NULLIF(o.service_profit, '') AS DECIMAL(50,0)) ELSE 0 END) AS CHAR), '0'),
		       COALESCE(CAST(SUM(CASE WHEN o.status = 'success' THEN CAST(NULLIF(o.amount, '') AS DECIMAL(50,0)) ELSE 0 END) AS CHAR), '0')
		FROM orders o WHERE `+orderWhere, orderArgs...).Scan(&total, &success, &failed, &pending, &profit, &turnover)
	if err != nil {
		return nil, err
	}
	conversion := "0%"
	if total > 0 {
		conversion = itoa((success*100)/total) + "%"
	}
	projects, err := r.dashboardBars(ctx, `
		SELECT COALESCE(NULLIF(m.name, ''), CONCAT('merchant-', m.id)),
		       COALESCE(CAST(SUM(CAST(NULLIF(o.amount, '') AS DECIMAL(50,0))) AS CHAR), '0')
		FROM orders o
		INNER JOIN merchants m ON m.id = o.merchant_id
		WHERE o.status = 'success' AND `+orderWhere+`
		GROUP BY m.id, m.name
		ORDER BY SUM(CAST(NULLIF(o.amount, '') AS DECIMAL(50,0))) DESC, m.id
		LIMIT 10`, orderArgs...)
	if err != nil {
		return nil, err
	}
	traders, err := r.dashboardBars(ctx, `
		SELECT COALESCE(NULLIF(u.name, ''), NULLIF(u.email, ''), CONCAT('trader-', u.id)),
		       COALESCE(CAST(SUM(CAST(NULLIF(o.amount, '') AS DECIMAL(50,0))) AS CHAR), '0')
		FROM orders o
		INNER JOIN users u ON u.id = o.trader_id
		WHERE o.status = 'success' AND `+orderWhere+`
		GROUP BY u.id, u.name, u.email
		ORDER BY SUM(CAST(NULLIF(o.amount, '') AS DECIMAL(50,0))) DESC, u.id
		LIMIT 10`, orderArgs...)
	if err != nil {
		return nil, err
	}
	disputeWhere, disputeArgs := dashboardDisputeWhere(period, merchantID)
	disputes, err := r.disputeShare(ctx, disputeWhere, disputeArgs)
	if err != nil {
		return nil, err
	}
	merchants, err := r.dashboardMerchants(ctx)
	if err != nil {
		return nil, err
	}
	liveOrders := r.scalar(ctx, `SELECT COUNT(*) FROM orders WHERE status = 'pending'`+merchantClause(merchantID), merchantArgs(merchantID)...)
	liveDisputes := r.scalar(ctx, `SELECT COUNT(*) FROM disputes WHERE status = 'pending'`)
	if merchantID > 0 {
		liveDisputes = r.scalar(ctx, `SELECT COUNT(*) FROM disputes d INNER JOIN orders o ON o.id = d.order_id WHERE d.status = 'pending' AND o.merchant_id = ?`, merchantID)
	}
	return map[string]any{
		"statistics": map[string]any{
			"totalTurnover": beauty(turnover.String, "usdt"), "totalProfit": beauty(profit.String, "usdt"),
			"totalOrderCount": total, "successOrderCount": success, "failedOrderCount": failed, "pendingOrderCount": pending,
			"conversionRate": conversion, "avgCheck": averageCheck(turnover.String, success),
		},
		"statusShare":  map[string]any{"data": []int{success, failed, pending}, "labels": []string{"success", "fail", "pending"}},
		"disputeShare": disputes,
		"topMerchants": projects,
		"topTraders":   traders,
		"merchants":    merchants,
		"live":         map[string]any{"pendingOrders": liveOrders, "pendingDisputes": liveDisputes, "onlineUsers": r.scalar(ctx, `SELECT COUNT(*) FROM users WHERE is_online = 1`)},
	}, nil
}

func merchantClause(merchantID int64) string {
	if merchantID > 0 {
		return " AND merchant_id = ?"
	}
	return ""
}

func merchantArgs(merchantID int64) []any {
	if merchantID > 0 {
		return []any{merchantID}
	}
	return nil
}

func dashboardOrderWhere(period string, merchantID int64) (string, []any) {
	where := "1=1"
	var args []any
	if merchantID > 0 {
		where += " AND o.merchant_id = ?"
		args = append(args, merchantID)
	}
	if since := periodSince(period); since != "" {
		where += " AND o.created_at >= " + since
	}
	return where, args
}

func dashboardDisputeWhere(period string, merchantID int64) (string, []any) {
	where := "1=1"
	var args []any
	if merchantID > 0 {
		where += " AND o.merchant_id = ?"
		args = append(args, merchantID)
	}
	if since := periodSince(period); since != "" {
		where += " AND d.created_at >= " + since
	}
	return where, args
}

func periodSince(period string) string {
	switch period {
	case "hour":
		return "DATE_SUB(NOW(), INTERVAL 1 HOUR)"
	case "day":
		return "DATE_SUB(NOW(), INTERVAL 1 DAY)"
	case "week":
		return "DATE_SUB(NOW(), INTERVAL 7 DAY)"
	default:
		return ""
	}
}

func (r *Reader) dashboardBars(ctx context.Context, query string, args ...any) (map[string]any, error) {
	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	labels := []string{}
	turnover := []float64{}
	for rows.Next() {
		var label, units string
		if err := rows.Scan(&label, &units); err != nil {
			return nil, err
		}
		labels = append(labels, label)
		turnover = append(turnover, majorFloat(units))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"labels": labels, "turnover": turnover}, nil
}

func (r *Reader) disputeShare(ctx context.Context, where string, args []any) (map[string]any, error) {
	rows, err := r.DB.QueryContext(ctx, `
		SELECT COALESCE(d.status, ''), COUNT(*)
		FROM disputes d
		INNER JOIN orders o ON o.id = d.order_id
		WHERE `+where+`
		GROUP BY d.status`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		if status != "" {
			counts[status] = count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	labels := []string{"accepted", "canceled", "pending"}
	seen := map[string]bool{"accepted": true, "canceled": true, "pending": true}
	extra := make([]string, 0)
	for status := range counts {
		if !seen[status] {
			extra = append(extra, status)
		}
	}
	sort.Strings(extra)
	labels = append(labels, extra...)
	data := make([]int, len(labels))
	for i, label := range labels {
		data[i] = counts[label]
	}
	return map[string]any{"data": data, "labels": labels}, nil
}

func (r *Reader) dashboardMerchants(ctx context.Context) ([]map[string]any, error) {
	rows, err := r.DB.QueryContext(ctx, `
		SELECT id, COALESCE(NULLIF(name, ''), CONCAT('merchant-', id))
		FROM merchants
		ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "name": name})
	}
	return items, rows.Err()
}

func averageCheck(turnoverUnits string, success int) string {
	if success <= 0 {
		return "0"
	}
	amount, err := money.FromUnits(turnoverUnits, "usdt")
	if err != nil {
		return "0"
	}
	per, err := amount.Div(itoa(success))
	if err != nil {
		return "0"
	}
	text, err := per.Beauty()
	if err != nil {
		return "0"
	}
	return text
}

func majorFloat(units string) float64 {
	value, err := strconv.ParseFloat(beauty(units, "usdt"), 64)
	if err != nil {
		return 0
	}
	return value
}

// Menu is the sidebar counters.
func (r *Reader) Menu(ctx context.Context) map[string]any {
	return map[string]any{
		"pendingOrdersCount":   r.scalar(ctx, `SELECT COUNT(*) FROM orders WHERE status = 'pending'`),
		"pendingDisputesCount": r.scalar(ctx, `SELECT COUNT(*) FROM disputes WHERE status = 'pending'`),
		"onlineUsers":          r.scalar(ctx, `SELECT COUNT(*) FROM users WHERE is_online = 1`),
		"activeDetails":        r.scalar(ctx, `SELECT COUNT(*) FROM payment_details WHERE is_active = 1`),
		"pendingWithdrawals":   r.scalar(ctx, `SELECT COUNT(*) FROM invoices WHERE type = 'withdrawal' AND status = 'pending'`),
		"cascade_enabled":      r.Cascade,
	}
}

func (r *Reader) scalar(ctx context.Context, query string, args ...any) int {
	var value int
	_ = r.DB.QueryRowContext(ctx, query, args...).Scan(&value)
	return value
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [12]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[i:])
}

// Rates is the cabinet rate table.
func (r *Reader) Rates(ctx context.Context) map[string]any {
	exchanges := make([]map[string]any, 0, len(market.Sources))
	markets := map[string]any{}
	for _, source := range market.Sources {
		exchanges = append(exchanges, map[string]any{"id": source, "name": source})
		var quotes []map[string]any
		for _, code := range money.All() {
			buy, sell := "0", "0"
			if r.Quotes != nil {
				if quote, err := r.Quotes.Get(ctx, code.Code(), source); err == nil {
					buy = precision(quote.BuyUnits, code.Code())
					sell = precision(quote.SellUnits, code.Code())
				}
			}
			quotes = append(quotes, map[string]any{
				"code": code.Code(), "symbol": code.Symbol(), "name": code.Name(),
				"buy_price": buy, "sell_price": sell,
			})
		}
		markets[source] = quotes
	}
	return map[string]any{"exchanges": exchanges, "markets": markets}
}

func precision(units, code string) string {
	amount, err := money.FromUnits(units, code)
	if err != nil {
		return "0"
	}
	text, err := amount.Precision()
	if err != nil {
		return "0"
	}
	return text
}

// WalletStats is the balance card on the finance screen.
func (r *Reader) WalletStats(ctx context.Context, userID int64, column string) map[string]any {
	var balance string
	err := r.DB.QueryRowContext(ctx, `SELECT COALESCE(`+column+`, '0') FROM wallets WHERE user_id = ?`, userID).Scan(&balance)
	if err != nil {
		balance = "0"
	}
	return map[string]any{"balance": beauty(balance, "usdt")}
}

// WalletBalances is the role balance shown in finance management.
// Trader balance is the free trust plus the insurance deposit.
func (r *Reader) WalletBalances(ctx context.Context, userID int64) map[string]any {
	var trust, reserve, merchant, team string
	var target int
	err := r.DB.QueryRowContext(ctx, `
		SELECT COALESCE(trust_balance, '0'), COALESCE(reserve_balance, '0'),
		       COALESCE(merchant_balance, '0'), COALESCE(teamleader_balance, '0'),
		       COALESCE(target_reserve_amount, 0)
		FROM wallets WHERE user_id = ?`, userID).Scan(&trust, &reserve, &merchant, &team, &target)
	if err != nil {
		trust, reserve, merchant, team = "0", "0", "0", "0"
	}
	goal, err := money.FromPrecision(strconv.Itoa(target), "usdt")
	targetText := "0"
	open := false
	if err == nil {
		if text, precErr := goal.Precision(); precErr == nil {
			targetText = text
		}
		held, heldErr := money.FromUnits(reserve, "usdt")
		if heldErr == nil {
			open, _ = held.LessThan(goal)
		}
	}
	return map[string]any{
		"trust":          beauty(trust, "usdt"),
		"reserve":        beauty(reserve, "usdt"),
		"reserve_target": targetText,
		"reserve_open":   open,
		"trader":         addBeauty(trust, reserve),
		"merchant":       beauty(merchant, "usdt"),
		"teamleader":     beauty(team, "usdt"),
	}
}

func addBeauty(left, right string) string {
	if left == "" {
		left = "0"
	}
	if right == "" {
		right = "0"
	}
	a, err := money.FromUnits(left, "usdt")
	if err != nil {
		return "0"
	}
	b, err := money.FromUnits(right, "usdt")
	if err != nil {
		return "0"
	}
	sum, err := a.AddMoney(b)
	if err != nil {
		return "0"
	}
	text, err := sum.Precision()
	if err != nil {
		return "0"
	}
	return text
}

// Referrals lists traders and merchants attached to a team leader.
func (r *Reader) Referrals(ctx context.Context, leaderID int64, page Page) (map[string]any, error) {
	query := `
		SELECT u.id, u.name, u.email, rel.commission_percentage
		FROM (
			SELECT trader_id AS user_id, commission_percentage FROM trader_team_leader_relations WHERE team_leader_id = ?
			UNION ALL
			SELECT merchant_id, commission_percentage FROM merchant_team_leader_relations WHERE team_leader_id = ?
		) rel
		INNER JOIN users u ON u.id = rel.user_id`
	total, err := r.count(ctx, `SELECT COUNT(*) FROM (`+query+`) counted`, leaderID, leaderID)
	if err != nil {
		return nil, err
	}
	rows, err := r.DB.QueryContext(ctx, query+` ORDER BY u.id LIMIT ? OFFSET ?`, leaderID, leaderID, page.Size, page.offset())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id int64
		var name, email string
		var rate float64
		if err := rows.Scan(&id, &name, &email, &rate); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "email": email,
			"commission_percentage": rate,
			"total_profit":          "0", "turnover": "0", "trader_profit": "0", "orders_count": 0,
		})
	}
	return Block(page, total, items), rows.Err()
}

// LeaderDashboard is the team-leader overview.
func (r *Reader) LeaderDashboard(ctx context.Context, leaderID int64) map[string]any {
	return map[string]any{
		"statistics": map[string]any{
			"totalProfit":       r.WalletStats(ctx, leaderID, "teamleader_balance")["balance"],
			"referralRate":      "0",
			"balance":           r.WalletStats(ctx, leaderID, "teamleader_balance")["balance"],
			"referralsCount":    r.scalar(ctx, `SELECT (SELECT COUNT(*) FROM trader_team_leader_relations WHERE team_leader_id = ?) + (SELECT COUNT(*) FROM merchant_team_leader_relations WHERE team_leader_id = ?)`, leaderID, leaderID),
			"successOrderCount": 0,
		},
		"chart": map[string]any{"labels": []string{}, "data": []string{}},
	}
}

// MerchantDashboard is the merchant overview.
func (r *Reader) MerchantDashboard(ctx context.Context, userID int64) map[string]any {
	var success int
	var profit sql.NullString
	_ = r.DB.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(CAST(merchant_profit AS DECIMAL(50,0))), 0)
		FROM orders WHERE status = 'success' AND merchant_id IN (SELECT id FROM merchants WHERE user_id = ?)`, userID).Scan(&success, &profit)
	return map[string]any{
		"statistics": map[string]any{
			"balance":           r.WalletStats(ctx, userID, "merchant_balance")["balance"],
			"successOrderCount": success, "totalProfit": beauty(profit.String, "usdt"),
			"totalWithdrawalAmount": "0", "disputesCount": r.scalar(ctx, `SELECT COUNT(*) FROM disputes d INNER JOIN orders o ON o.id = d.order_id WHERE o.merchant_id IN (SELECT id FROM merchants WHERE user_id = ?)`, userID),
		},
	}
}

// TraderDashboard is the trader overview.
func (r *Reader) TraderDashboard(ctx context.Context, userID int64) map[string]any {
	var success int
	var profit sql.NullString
	_ = r.DB.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(CAST(trader_profit AS DECIMAL(50,0))), 0)
		FROM orders WHERE status = 'success' AND trader_id = ?`, userID).Scan(&success, &profit)
	return map[string]any{
		"statistics": map[string]any{
			"balance":           r.WalletStats(ctx, userID, "trust_balance")["balance"],
			"successOrderCount": success, "totalProfit": beauty(profit.String, "usdt"),
			"totalWithdrawalAmount": "0", "disputesCount": r.scalar(ctx, `SELECT COUNT(*) FROM disputes WHERE trader_id = ?`, userID),
		},
	}
}

// Me is the profile card.
func (r *Reader) Me(ctx context.Context, userID int64) (map[string]any, error) {
	var name, email string
	var created sql.NullTime
	err := r.DB.QueryRowContext(ctx, `SELECT name, email, created_at FROM users WHERE id = ?`, userID).Scan(&name, &email, &created)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": userID, "name": name, "email": email, "created_at": r.date(created)}, nil
}

// Categories lists both category tables the admin screens open.
func (r *Reader) Categories(ctx context.Context, page Page, table string) (map[string]any, error) {
	return r.named(ctx, page,
		`SELECT COUNT(*) FROM `+table,
		`SELECT id, name, slug FROM `+table+` ORDER BY id LIMIT ? OFFSET ?`,
		func(rows *sql.Rows) (map[string]any, error) {
			var id int64
			var name, slug string
			if err := rows.Scan(&id, &name, &slug); err != nil {
				return nil, err
			}
			return map[string]any{"id": id, "name": name, "slug": slug}, nil
		})
}

// Providers lists cascade providers.
func (r *Reader) Providers(ctx context.Context, page Page) (map[string]any, error) {
	return r.named(ctx, page,
		`SELECT COUNT(*) FROM providers`,
		`SELECT id, name, integration, is_active FROM providers ORDER BY id LIMIT ? OFFSET ?`,
		func(rows *sql.Rows) (map[string]any, error) {
			var id int64
			var name string
			var integration sql.NullString
			var active int
			if err := rows.Scan(&id, &name, &integration, &active); err != nil {
				return nil, err
			}
			return map[string]any{"id": id, "name": name, "integration": nullText(integration), "is_active": active == 1}, nil
		})
}

// Logs pages a log table by created_at.
func (r *Reader) Logs(ctx context.Context, page Page, table string, columns string) (map[string]any, error) {
	return r.named(ctx, page,
		`SELECT COUNT(*) FROM `+table,
		`SELECT `+columns+` FROM `+table+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		scanRow)
}

func scanRow(rows *sql.Rows) (map[string]any, error) {
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	raw := make([]any, len(names))
	dest := make([]any, len(names))
	for i := range raw {
		dest[i] = &raw[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	item := map[string]any{}
	for i, name := range names {
		item[name] = cell(raw[i])
	}
	return item, nil
}

func cell(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []byte:
		return string(typed)
	case time.Time:
		return typed.Format("2006-01-02 15:04:05")
	default:
		return fmt.Sprint(typed)
	}
}

// RoleOptions fills the user filter.
func (r *Reader) RoleOptions(ctx context.Context) ([]map[string]any, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id, name FROM roles ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "name": name, "value": name})
	}
	if items == nil {
		items = []map[string]any{}
	}
	return items, rows.Err()
}

// TraderCategoryOptions fills the user form select.
func (r *Reader) TraderCategoryOptions(ctx context.Context) ([]map[string]any, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id, name FROM trader_categories ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "name": name})
	}
	if items == nil {
		items = []map[string]any{}
	}
	return items, rows.Err()
}

// CategoryOptions fills the merchant edit form.
func (r *Reader) CategoryOptions(ctx context.Context) ([]map[string]any, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id, name FROM categories ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "name": name})
	}
	if items == nil {
		items = []map[string]any{}
	}
	return items, rows.Err()
}

// UserCard is the admin edit form payload for one user.
func (r *Reader) UserCard(ctx context.Context, id int64) (map[string]any, error) {
	var (
		name, email, referral           string
		category, roleID                sql.NullInt64
		reserve                         int
		banned                          sql.NullTime
		payouts, stop, has2FA           int
		traderRate, roleName, promoCode sql.NullString
	)
	err := r.DB.QueryRowContext(ctx, `
		SELECT u.name, u.email, u.trader_category_id, u.banned_at, u.payouts_enabled, u.stop_traffic,
		       CAST(u.referral_commission_percentage AS CHAR), CAST(u.trader_commission_rate AS CHAR),
		       u.google2fa_secret IS NOT NULL, r.id, r.name, p.code,
		       COALESCE((SELECT w.target_reserve_amount FROM wallets w WHERE w.user_id = u.id ORDER BY w.id LIMIT 1), 1000)
		FROM users u
		LEFT JOIN user_roles ur ON ur.user_id = u.id
		LEFT JOIN roles r ON r.id = ur.role_id
		LEFT JOIN promo_codes p ON p.id = u.promo_code_id
		WHERE u.id = ?
		ORDER BY r.id
		LIMIT 1`, id).Scan(
		&name, &email, &category, &banned, &payouts, &stop, &referral, &traderRate, &has2FA, &roleID, &roleName, &promoCode, &reserve)
	if err != nil {
		return nil, err
	}
	var role any
	if roleID.Valid {
		role = map[string]any{"id": roleID.Int64, "name": roleName.String}
	}
	var promo any
	if promoCode.Valid {
		promo = map[string]any{"code": promoCode.String}
	}
	var categoryID any
	if category.Valid {
		categoryID = category.Int64
	}
	rate := ""
	if traderRate.Valid {
		rate = traderRate.String
	}
	merchantCategories, err := r.merchantCategoryIDs(ctx, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "name": name, "email": email,
		"role": role, "promo_code": promo, "trader_category_id": categoryID,
		"target_reserve_amount": reserve,
		"merchant_category_ids": merchantCategories,
		"banned_at":             r.date(banned), "payouts_enabled": payouts == 1, "stop_traffic": stop == 1,
		"referral_commission_percentage": referral, "trader_commission_rate": rate,
		"has_2fa": has2FA == 1,
	}, nil
}

func (r *Reader) merchantCategoryIDs(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := r.DB.QueryContext(ctx, `
		SELECT DISTINCT cm.category_id
		FROM category_merchant cm
		INNER JOIN merchants m ON m.id = cm.merchant_id
		WHERE m.user_id = ?
		ORDER BY cm.category_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if ids == nil {
		ids = []int64{}
	}
	return ids, rows.Err()
}

// Notes is the user note list.
func (r *Reader) Notes(ctx context.Context, userID int64) ([]map[string]any, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id, content, created_at FROM user_notes WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id int64
		var content string
		var created sql.NullTime
		if err := rows.Scan(&id, &content, &created); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "content": content, "created_at": r.when(created)})
	}
	if items == nil {
		items = []map[string]any{}
	}
	return items, rows.Err()
}

// Terminals is the provider terminal table.
func (r *Reader) Terminals(ctx context.Context, page Page) (map[string]any, error) {
	return r.named(ctx, page,
		`SELECT COUNT(*) FROM provider_terminals`,
		`SELECT t.id, t.name, t.is_active, p.name FROM provider_terminals t LEFT JOIN providers p ON p.id = t.provider_id ORDER BY t.id DESC LIMIT ? OFFSET ?`,
		func(rows *sql.Rows) (map[string]any, error) {
			var id int64
			var name string
			var active int
			var provider sql.NullString
			if err := rows.Scan(&id, &name, &active, &provider); err != nil {
				return nil, err
			}
			return map[string]any{
				"id": id, "name": name, "is_active": active == 1,
				"provider": map[string]any{"name": provider.String},
			}, nil
		})
}

// TerminalCard is the terminal edit modal: provider, status, and every merchant link.
func (r *Reader) TerminalCard(ctx context.Context, id int64) (map[string]any, error) {
	var name, provider string
	var active int
	err := r.DB.QueryRowContext(ctx, `
		SELECT t.name, t.is_active, COALESCE(p.name, '')
		FROM provider_terminals t
		LEFT JOIN providers p ON p.id = t.provider_id
		WHERE t.id = ?`, id).Scan(&name, &active, &provider)
	if err != nil {
		return nil, err
	}
	rows, err := r.DB.QueryContext(ctx, `
		SELECT m.id, COALESCE(NULLIF(m.name, ''), u.email, ''),
		       CASE WHEN link.id IS NULL THEN 0 ELSE 1 END,
		       COALESCE(link.is_active, 0)
		FROM merchants m
		LEFT JOIN users u ON u.id = m.user_id
		LEFT JOIN provider_terminal_merchant link
		  ON link.merchant_id = m.id AND link.provider_terminal_id = ?
		ORDER BY m.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var merchants []map[string]any
	for rows.Next() {
		var merchantID int64
		var merchantName string
		var attached, pivot int
		if err := rows.Scan(&merchantID, &merchantName, &attached, &pivot); err != nil {
			return nil, err
		}
		merchants = append(merchants, map[string]any{
			"id": merchantID, "name": merchantName,
			"is_attached": attached == 1, "pivot_active": pivot == 1,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if merchants == nil {
		merchants = []map[string]any{}
	}
	return map[string]any{
		"terminal": map[string]any{
			"id": id, "name": name, "is_active": active == 1,
			"provider": map[string]any{"name": provider},
		},
		"merchants": merchants,
	}, nil
}

// PriorityCard is the merchant category-priority modal.
func (r *Reader) PriorityCard(ctx context.Context, merchantID int64) (map[string]any, error) {
	var exists int
	err := r.DB.QueryRowContext(ctx, `SELECT 1 FROM merchants WHERE id = ?`, merchantID).Scan(&exists)
	if err != nil {
		return nil, err
	}
	assignedRows, err := r.DB.QueryContext(ctx, `
		SELECT c.id, c.name, p.priority,
		       (SELECT COUNT(*) FROM users u WHERE u.trader_category_id = c.id AND u.banned_at IS NULL)
		FROM merchant_trader_category_priorities p
		INNER JOIN trader_categories c ON c.id = p.trader_category_id
		WHERE p.merchant_id = ?
		ORDER BY p.priority, c.id`, merchantID)
	if err != nil {
		return nil, err
	}
	defer assignedRows.Close()
	var assigned []map[string]any
	for assignedRows.Next() {
		var id int64
		var name string
		var priority, traders int
		if err := assignedRows.Scan(&id, &name, &priority, &traders); err != nil {
			return nil, err
		}
		assigned = append(assigned, map[string]any{
			"id": id, "name": name, "priority": priority, "active_traders_count": traders,
		})
	}
	if err := assignedRows.Err(); err != nil {
		return nil, err
	}
	availableRows, err := r.DB.QueryContext(ctx, `
		SELECT c.id, c.name
		FROM trader_categories c
		WHERE NOT EXISTS (
			SELECT 1 FROM merchant_trader_category_priorities p
			WHERE p.merchant_id = ? AND p.trader_category_id = c.id
		)
		ORDER BY c.name, c.id`, merchantID)
	if err != nil {
		return nil, err
	}
	defer availableRows.Close()
	var available []map[string]any
	for availableRows.Next() {
		var id int64
		var name string
		if err := availableRows.Scan(&id, &name); err != nil {
			return nil, err
		}
		available = append(available, map[string]any{"id": id, "name": name})
	}
	if err := availableRows.Err(); err != nil {
		return nil, err
	}
	if assigned == nil {
		assigned = []map[string]any{}
	}
	if available == nil {
		available = []map[string]any{}
	}
	return map[string]any{
		"assigned_categories":  assigned,
		"available_categories": available,
	}, nil
}

// Enabled is the requisite statistics card.
func (r *Reader) Enabled(ctx context.Context, detailType string, gatewayID, userID int64) map[string]any {
	where := `pd.is_active = 1 AND pd.archived_at IS NULL`
	var args []any
	if detailType != "" {
		where += ` AND pd.detail_type = ?`
		args = append(args, detailType)
	}
	if userID > 0 {
		where += ` AND pd.user_id = ?`
		args = append(args, userID)
	}
	if gatewayID > 0 {
		where += ` AND EXISTS (SELECT 1 FROM payment_detail_payment_gateway g WHERE g.payment_detail_id = pd.id AND g.payment_gateway_id = ?)`
		args = append(args, gatewayID)
	}
	total := r.scalar(ctx, `SELECT COUNT(*) FROM payment_details pd WHERE `+where, args...)
	balance := "0"
	_ = r.DB.QueryRowContext(ctx, `SELECT COALESCE(CAST(SUM(CAST(NULLIF(trust_balance, '') AS DECIMAL(50,0))) AS CHAR), '0') FROM wallets`).Scan(&balance)
	online := "0"
	_ = r.DB.QueryRowContext(ctx, `
		SELECT COALESCE(CAST(SUM(CAST(NULLIF(w.trust_balance, '') AS DECIMAL(50,0))) AS CHAR), '0')
		FROM wallets w INNER JOIN users u ON u.id = w.user_id WHERE u.is_online = 1`).Scan(&online)
	groups := map[string]map[string]any{}
	rows, err := r.DB.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(pd.currency, ''), 'rub'), COALESCE(NULLIF(pd.detail_type, ''), 'card'), COUNT(*),
		       COALESCE(CAST(SUM(CAST(NULLIF(pd.daily_limit, '') AS DECIMAL(50,0)) - CAST(COALESCE(NULLIF(pd.current_daily_limit, ''), '0') AS DECIMAL(50,0))) AS CHAR), '0'),
		       COALESCE(CAST(SUM(CAST(NULLIF(pd.daily_limit, '') AS DECIMAL(50,0))) AS CHAR), '0')
		FROM payment_details pd
		WHERE `+where+`
		GROUP BY 1, 2
		ORDER BY 1, 2`, args...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var currency, kind, free, potential string
			var count int
			if rows.Scan(&currency, &kind, &count, &free, &potential) != nil {
				continue
			}
			bucket, ok := groups[currency]
			if !ok {
				bucket = map[string]any{}
				groups[currency] = bucket
			}
			bucket[kind] = map[string]any{
				"title":           detailTitle(kind),
				"count":           count,
				"free_limit":      beauty(free, currency),
				"potential_limit": beauty(potential, currency),
			}
		}
	}
	stats := map[string]any{}
	for currency, bucket := range groups {
		stats[currency] = bucket
	}
	return map[string]any{
		"statistics": map[string]any{
			"totalPaymentDetails": total,
			"tradersBalance":      map[string]any{"total": beauty(balance, "usdt"), "online": beauty(online, "usdt")},
			"minAmountStats":      stats,
		},
	}
}

func detailTitle(kind string) string {
	switch kind {
	case "card":
		return "Карта"
	case "phone":
		return "Телефон"
	case "sim":
		return "SIM"
	case "sbp":
		return "СБП"
	default:
		return kind
	}
}

// PayoutGateways lists payout methods.
func (r *Reader) PayoutGateways(ctx context.Context, page Page) (map[string]any, error) {
	return r.named(ctx, page,
		`SELECT COUNT(*) FROM payout_gateways`,
		`SELECT id, COALESCE(uuid, ''), COALESCE(name, ''), COALESCE(enabled, 0) FROM payout_gateways ORDER BY id LIMIT ? OFFSET ?`,
		func(rows *sql.Rows) (map[string]any, error) {
			var id int64
			var uuid, name string
			var enabled int
			if err := rows.Scan(&id, &uuid, &name, &enabled); err != nil {
				return nil, err
			}
			return map[string]any{"id": id, "uuid": uuid, "name": name, "enabled": enabled == 1}, nil
		})
}

// PayoutOffers lists trader payout offers.
func (r *Reader) PayoutOffers(ctx context.Context, page Page) (map[string]any, error) {
	return r.named(ctx, page,
		`SELECT COUNT(*) FROM payout_offers`,
		`SELECT id, COALESCE(min_amount, '0'), COALESCE(max_amount, '0'), COALESCE(currency, 'rub'), COALESCE(active, 0), COALESCE(occupied, 0), owner_id FROM payout_offers ORDER BY id LIMIT ? OFFSET ?`,
		func(rows *sql.Rows) (map[string]any, error) {
			var id int64
			var minAmount, maxAmount, currency string
			var active, occupied int
			var owner sql.NullInt64
			if err := rows.Scan(&id, &minAmount, &maxAmount, &currency, &active, &occupied, &owner); err != nil {
				return nil, err
			}
			return map[string]any{
				"id": id, "min_amount": beauty(minAmount, currency), "max_amount": beauty(maxAmount, currency),
				"currency": currency, "active": active == 1, "occupied": occupied == 1, "owner_id": owner.Int64,
			}, nil
		})
}
