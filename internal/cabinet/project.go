package cabinet

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"

	"finance-platform/go/internal/market"
)

// ProjectCard is one merchant-cabinet project: owner, limits, methods and counters.
func (r *Reader) ProjectCard(ctx context.Context, projectID, ownerID int64) (map[string]any, error) {
	var name, description, uuid, callback, marketName, email, minRaw, maxRaw, settings string
	var active int
	var validated, banned, created sql.NullTime
	err := r.DB.QueryRowContext(ctx, `
		SELECT COALESCE(m.name, ''), COALESCE(m.description, ''), m.uuid, COALESCE(m.callback_url, ''),
		       COALESCE(m.market, ''), COALESCE(m.active, 0), m.validated_at, m.banned_at, m.created_at,
		       COALESCE(m.min_order_amounts, ''), COALESCE(m.max_order_amounts, ''), COALESCE(m.gateway_settings, ''),
		       COALESCE(u.email, '')
		FROM merchants m
		LEFT JOIN users u ON u.id = m.user_id
		WHERE m.id = ? AND m.user_id = ?`, projectID, ownerID).
		Scan(&name, &description, &uuid, &callback, &marketName, &active, &validated, &banned, &created, &minRaw, &maxRaw, &settings, &email)
	if err != nil {
		return nil, err
	}
	todayCount, todayProfit := r.projectProfit(ctx, projectID, true)
	totalCount, totalProfit := r.projectProfit(ctx, projectID, false)
	gates, err := r.projectGateways(ctx, settings)
	if err != nil {
		return nil, err
	}
	markets := make([]map[string]any, 0, len(market.Sources))
	for _, source := range market.Sources {
		markets = append(markets, map[string]any{"value": source, "name": source, "label": source})
	}
	createdAt := ""
	if created.Valid {
		createdAt = created.Time.Format("2006-01-02T15:04:05Z07:00")
	}
	return map[string]any{
		"merchant": map[string]any{
			"id": projectID, "uuid": uuid, "name": name, "description": description,
			"callback_url": callback, "market": marketName, "active": active == 1,
			"validated_at": r.date(validated), "banned_at": r.date(banned), "created_at": createdAt,
			"owner":             map[string]any{"email": email},
			"min_order_amounts": jsonObject(minRaw),
			"max_order_amounts": jsonObject(maxRaw),
		},
		"statistics": map[string]any{
			"today_profit": todayProfit, "total_profit": totalProfit, "currency": "USDT",
			"today_orders_count": todayCount, "total_orders_count": totalCount,
			"requests_count": r.scalar(ctx, `SELECT COUNT(*) FROM merchant_api_request_logs WHERE merchant_id = ?`, projectID),
			"disputes_count": r.scalar(ctx, `SELECT COUNT(*) FROM disputes d INNER JOIN orders o ON o.id = d.order_id WHERE o.merchant_id = ?`, projectID),
		},
		"paymentGateways": gates,
		"markets":         markets,
	}, nil
}

func (r *Reader) projectProfit(ctx context.Context, projectID int64, today bool) (int, string) {
	query := `
		SELECT COUNT(*), COALESCE(CAST(SUM(CAST(NULLIF(merchant_profit, '') AS DECIMAL(50,0))) AS CHAR), '0')
		FROM orders WHERE merchant_id = ? AND status = 'success'`
	args := []any{projectID}
	if today {
		query += ` AND created_at >= DATE_SUB(NOW(), INTERVAL 1 DAY)`
	}
	var count int
	var profit string
	if err := r.DB.QueryRowContext(ctx, query, args...).Scan(&count, &profit); err != nil {
		return 0, "0"
	}
	return count, beauty(profit, "usdt")
}

func (r *Reader) projectGateways(ctx context.Context, settings string) ([]map[string]any, error) {
	prefs := map[string]map[string]any{}
	if settings != "" {
		_ = json.Unmarshal([]byte(settings), &prefs)
	}
	rows, err := r.DB.QueryContext(ctx, `
		SELECT id, COALESCE(name, ''), COALESCE(currency, ''), total_service_commission_rate_for_orders
		FROM payment_gateways WHERE is_active = 1 ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, currency string
		var rate float64
		if err := rows.Scan(&id, &name, &currency, &rate); err != nil {
			return nil, err
		}
		enabled := true
		if pref, ok := prefs[strconv.FormatInt(id, 10)]; ok {
			if value, ok := pref["active"].(bool); ok {
				enabled = value
			}
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "currency": currency,
			"total_service_commission_rate_for_orders": rate,
			"is_active_for_merchant":                   enabled,
		})
	}
	return items, rows.Err()
}

func jsonObject(raw string) map[string]any {
	out := map[string]any{}
	if raw == "" || raw == "null" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{}
	}
	if out == nil {
		return map[string]any{}
	}
	return out
}
