package cabinet

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"finance-platform/go/internal/market"
)

// Reader serves the cabinet lists. SQL is explicit and scoped to the caller.
type Reader struct {
	DB       *sql.DB
	Location *time.Location
	Quotes   market.Book
	Cascade  bool
}

func (r *Reader) when(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}
	return clock(value.Time, r.Location)
}

func (r *Reader) date(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}
	return day(value.Time, r.Location)
}

func (r *Reader) count(ctx context.Context, query string, args ...any) (int, error) {
	var total int
	err := r.DB.QueryRowContext(ctx, query, args...).Scan(&total)
	return total, err
}

// Orders is the admin and support deal table.
func (r *Reader) Orders(ctx context.Context, page Page, where string, args []any) (map[string]any, error) {
	total, err := r.count(ctx, `SELECT COUNT(*) FROM orders o LEFT JOIN users trader ON trader.id = o.trader_id LEFT JOIN payment_details pd ON pd.id = o.payment_detail_id WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	listArgs := append(append([]any{}, args...), page.Size, page.offset())
	rows, err := r.DB.QueryContext(ctx, `
		SELECT o.id, o.uuid, COALESCE(o.external_id, ''), COALESCE(o.amount, '0'), COALESCE(o.total_profit, '0'),
		       COALESCE(o.trader_profit, '0'), COALESCE(o.merchant_profit, '0'), COALESCE(o.service_profit, '0'),
		       COALESCE(o.trader_paid_for_order, '0'), COALESCE(o.currency, 'rub'),
		       COALESCE(o.conversion_price, ''), COALESCE(o.base_conversion_price, ''),
		       o.trader_commission_rate, o.total_service_commission_rate, o.status, o.created_at, o.expires_at,
		       pg.name, pd.detail, pd.detail_type, pd.name, ud.name, trader.email, trader.name,
		       shop.id, shop.name,
		       EXISTS(SELECT 1 FROM disputes d WHERE d.order_id = o.id)
		FROM orders o
		LEFT JOIN payment_gateways pg ON pg.id = o.payment_gateway_id
		LEFT JOIN payment_details pd ON pd.id = o.payment_detail_id
		LEFT JOIN user_devices ud ON ud.id = pd.user_device_id
		LEFT JOIN users trader ON trader.id = o.trader_id
		LEFT JOIN merchants shop ON shop.id = o.merchant_id
		WHERE `+where+` ORDER BY o.id DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var (
			id, dispute                                             int64
			uuid, externalID, amount, profit, currency, status      string
			traderProfit, merchantProfit, serviceProfit, traderPaid string
			conversion, baseConversion                              string
			traderRate, serviceRate                                 sql.NullFloat64
			created                                                 time.Time
			expires                                                 sql.NullTime
			gateway, detail, detailType, detailName, device         sql.NullString
			email, name, shopName                                   sql.NullString
			shopID                                                  sql.NullInt64
		)
		if err := rows.Scan(
			&id, &uuid, &externalID, &amount, &profit, &traderProfit, &merchantProfit, &serviceProfit, &traderPaid,
			&currency, &conversion, &baseConversion, &traderRate, &serviceRate, &status, &created, &expires,
			&gateway, &detail, &detailType, &detailName, &device, &email, &name, &shopID, &shopName, &dispute,
		); err != nil {
			return nil, err
		}
		gatewayName := "Партнерский шлюз"
		if gateway.Valid && gateway.String != "" {
			gatewayName = gateway.String
		}
		detailText := "Партнерские реквизиты"
		if detail.Valid && detail.String != "" {
			detailText = detail.String
		}
		traderEmail := "Партнерский ордер"
		traderName := "Партнер"
		if email.Valid && email.String != "" {
			traderEmail = email.String
		}
		if name.Valid && name.String != "" {
			traderName = name.String
		}
		var projectID any
		projectName := ""
		if shopID.Valid {
			projectID = shopID.Int64
			projectName = shopName.String
		}
		items = append(items, map[string]any{
			"id":                        id,
			"uuid":                      uuid,
			"external_id":               externalID,
			"amount":                    beauty(amount, currency),
			"total_profit":              beauty(profit, "usdt"),
			"trader_profit":             beauty(traderProfit, "usdt"),
			"merchant_profit":           beauty(merchantProfit, "usdt"),
			"service_profit":            beauty(serviceProfit, "usdt"),
			"trader_paid":               beauty(traderPaid, "usdt"),
			"rate":                      showRate(conversion, baseConversion, currency),
			"trader_commission":         commissionPercent(traderRate),
			"service_commission":        commissionPercent(serviceRate),
			"currency":                  currency,
			"base_currency":             "usdt",
			"status":                    status,
			"status_name":               status,
			"has_dispute":               dispute == 1,
			"payment_gateway_name":      gatewayName,
			"payment_gateway_logo_path": nil,
			"payment_detail":            detailText,
			"payment_detail_type":       valueOr(detailType.String, "phone"),
			"payment_detail_name":       detailName.String,
			"device_name":               valueOr(device.String, "Партнерское устройство"),
			"trader_email":              traderEmail,
			"trader_name":               traderName,
			"merchant_id":               projectID,
			"merchant_name":             projectName,
			"created_at":                clock(created, r.Location),
			"expires_at":                r.when(expires),
		})
	}
	return Block(page, total, items), rows.Err()
}

// MerchantOrders is the merchant deal table, scoped to the owner's projects.
func (r *Reader) MerchantOrders(ctx context.Context, userID int64, page Page, uuid, external, status string) (map[string]any, error) {
	where, args := orderWhere(uuid, external, "", "", status)
	where = "o.merchant_id IN (SELECT id FROM merchants WHERE user_id = ?) AND " + where
	args = append([]any{userID}, args...)
	total, err := r.count(ctx, `SELECT COUNT(*) FROM orders o WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	listArgs := append(append([]any{}, args...), page.Size, page.offset())
	rows, err := r.DB.QueryContext(ctx, `
		SELECT o.id, o.uuid, COALESCE(o.external_id, ''), COALESCE(o.amount, '0'), COALESCE(o.currency, 'rub'), o.status, o.created_at,
		       COALESCE(o.merchant_profit, '0'), COALESCE(o.total_profit, '0'), COALESCE(o.base_conversion_price, '0'), pg.name
		FROM orders o
		LEFT JOIN payment_gateways pg ON pg.id = o.payment_gateway_id
		WHERE `+where+` ORDER BY o.id DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id int64
		var uuid, externalID, amount, currency, status string
		var created time.Time
		var merchantProfit, total, rate string
		var gateway sql.NullString
		if err := rows.Scan(&id, &uuid, &externalID, &amount, &currency, &status, &created, &merchantProfit, &total, &rate, &gateway); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "uuid": uuid, "external_id": externalID,
			"amount": beauty(amount, currency), "currency": currency, "status": status,
			"created_at":           clock(created, r.Location),
			"profit":               beauty(merchantProfit, "usdt"),
			"total_profit":         beauty(total, "usdt"),
			"conversion_price":     rate,
			"payment_gateway_name": valueOr(gateway.String, "Партнерский шлюз"),
		})
	}
	return Block(page, total, items), rows.Err()
}

func showRate(units, plain, currency string) string {
	units = strings.TrimSpace(units)
	if units != "" && units != "0" {
		return beauty(units, currency) + " " + strings.ToUpper(currency)
	}
	plain = strings.TrimSpace(plain)
	plain = strings.TrimRight(strings.TrimRight(plain, "0"), ".")
	if plain == "" {
		return "—"
	}
	return plain + " " + strings.ToUpper(currency)
}

func commissionPercent(value sql.NullFloat64) string {
	if !value.Valid {
		return "—"
	}
	return strconv.FormatFloat(value.Float64, 'f', -1, 64) + " %"
}

func orderWhere(uuid, external, user, detail, status string) (string, []any) {
	parts := []string{"1=1"}
	var args []any
	if uuid != "" {
		parts = append(parts, "o.uuid LIKE ?")
		args = append(args, "%"+uuid+"%")
	}
	if external != "" {
		parts = append(parts, "o.external_id LIKE ?")
		args = append(args, "%"+external+"%")
	}
	if user != "" {
		parts = append(parts, "EXISTS (SELECT 1 FROM users u WHERE u.id = o.trader_id AND (u.email LIKE ? OR u.name LIKE ?))")
		args = append(args, "%"+user+"%", "%"+user+"%")
	}
	if detail != "" {
		parts = append(parts, "EXISTS (SELECT 1 FROM payment_details pd WHERE pd.id = o.payment_detail_id AND pd.detail LIKE ?)")
		args = append(args, "%"+detail+"%")
	}
	if status != "" {
		parts = append(parts, "o.status = ?")
		args = append(args, status)
	}
	return strings.Join(parts, " AND "), args
}

// OrderFilter is the admin order table.
func OrderFilter(uuid, external, user, detail, status string, traderID int64) (string, []any) {
	where, args := orderWhere(uuid, external, user, detail, status)
	if traderID > 0 {
		where += " AND o.trader_id = ?"
		args = append(args, traderID)
	}
	return where, args
}

// TraderOrders is the trader deal table.
func (r *Reader) TraderOrders(ctx context.Context, traderID int64, page Page) (map[string]any, error) {
	where := "o.trader_id = ?"
	args := []any{traderID}
	total, err := r.count(ctx, `SELECT COUNT(*) FROM orders o WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	listArgs := append(append([]any{}, args...), page.Size, page.offset())
	rows, err := r.DB.QueryContext(ctx, `
		SELECT o.id, o.uuid, COALESCE(o.external_id, ''), COALESCE(o.amount, '0'), COALESCE(o.currency, 'rub'),
		       COALESCE(o.status, ''), o.created_at, COALESCE(o.trader_profit, '0'), COALESCE(o.total_profit, '0'),
		       COALESCE(o.base_conversion_price, '0'), COALESCE(o.trader_commission_rate, 0), pg.name
		FROM orders o
		LEFT JOIN payment_gateways pg ON pg.id = o.payment_gateway_id
		WHERE `+where+` ORDER BY o.id DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []map[string]any
	for rows.Next() {
		var id int64
		var uuid, externalID, amount, currency, status string
		var created time.Time
		var profit, total, rate string
		var commission float64
		var gateway sql.NullString
		if err := rows.Scan(&id, &uuid, &externalID, &amount, &currency, &status, &created, &profit, &total, &rate, &commission, &gateway); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "uuid": uuid, "external_id": externalID,
			"amount": beauty(amount, currency), "currency": currency, "status": status,
			"created_at": clock(created, r.Location),
			"profit":     beauty(profit, "usdt"), "total_profit": beauty(total, "usdt"),
			"rate": rate, "commission": commission,
			"method": valueOr(gateway.String, "Партнерский шлюз"),
		})
	}
	return Block(page, total, items), rows.Err()
}
