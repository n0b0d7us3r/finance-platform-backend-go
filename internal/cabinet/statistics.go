package cabinet

import (
	"context"

	"github.com/shopspring/decimal"
)

// MerchantStatistics is the admin merchant report: one row per merchant for the period.
func (r *Reader) MerchantStatistics(ctx context.Context, period string) (map[string]any, error) {
	orderSince, logSince := periodFilters(period)
	rows, err := r.DB.QueryContext(ctx, `
		SELECT m.id, COALESCE(NULLIF(m.name, ''), CONCAT('merchant-', m.id)), COALESCE(m.domain, ''),
		       COALESCE(m.active, 0), CASE WHEN m.banned_at IS NULL THEN 0 ELSE 1 END,
		       COUNT(o.id),
		       CAST(COALESCE(SUM(o.status = 'success'), 0) AS SIGNED),
		       CAST(COALESCE(SUM(o.status = 'fail'), 0) AS SIGNED),
		       CAST(COALESCE(SUM(o.status = 'pending'), 0) AS SIGNED),
		       COALESCE(CAST(SUM(CASE WHEN o.status = 'success' THEN CAST(NULLIF(o.amount, '') AS DECIMAL(50,0)) ELSE 0 END) AS CHAR), '0'),
		       (SELECT COUNT(*) FROM merchant_api_request_logs l WHERE l.merchant_id = m.id`+logSince+`)
		FROM merchants m
		LEFT JOIN orders o ON o.merchant_id = m.id`+orderSince+`
		GROUP BY m.id, m.name, m.domain, m.active, m.banned_at
		ORDER BY COUNT(o.id) DESC, m.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPartyStats(rows, true)
}

// TraderStatistics is the admin trader report: one row per trader for the period.
func (r *Reader) TraderStatistics(ctx context.Context, period string) (map[string]any, error) {
	orderSince, disputeSince := periodFilters(period)
	rows, err := r.DB.QueryContext(ctx, `
		SELECT u.id, COALESCE(NULLIF(u.name, ''), u.email), COALESCE(u.email, ''),
		       CASE WHEN u.banned_at IS NULL THEN 0 ELSE 1 END, u.stop_traffic, u.is_online,
		       COUNT(o.id),
		       CAST(COALESCE(SUM(o.status = 'success'), 0) AS SIGNED),
		       CAST(COALESCE(SUM(o.status = 'fail'), 0) AS SIGNED),
		       CAST(COALESCE(SUM(o.status = 'pending'), 0) AS SIGNED),
		       COALESCE(CAST(SUM(CASE WHEN o.status = 'success' THEN CAST(NULLIF(o.amount, '') AS DECIMAL(50,0)) ELSE 0 END) AS CHAR), '0'),
		       (SELECT COUNT(*) FROM disputes d WHERE d.trader_id = u.id`+disputeSince+`)
		FROM users u
		INNER JOIN user_roles ur ON ur.user_id = u.id
		INNER JOIN roles role ON role.id = ur.role_id AND role.name = 'Trader'
		LEFT JOIN orders o ON o.trader_id = u.id`+orderSince+`
		GROUP BY u.id, u.name, u.email, u.banned_at, u.stop_traffic, u.is_online
		ORDER BY COUNT(o.id) DESC, u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPartyStats(rows, false)
}

func periodFilters(period string) (string, string) {
	var since string
	switch period {
	case "hour":
		since = "DATE_SUB(NOW(), INTERVAL 1 HOUR)"
	case "day":
		since = "DATE_SUB(NOW(), INTERVAL 1 DAY)"
	case "week":
		since = "DATE_SUB(NOW(), INTERVAL 7 DAY)"
	default:
		return "", ""
	}
	return " AND o.created_at >= " + since, " AND created_at >= " + since
}

type statRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanPartyStats(rows statRows, merchant bool) (map[string]any, error) {
	items := []map[string]any{}
	var requests, accepted, apiOrDisputes, withTraffic int
	turnover := decimal.Zero
	for rows.Next() {
		var id int64
		var name, second, amount string
		var activeOrStop, banned, extra int
		var total, okCount, failed, pending, side int
		var err error
		if merchant {
			err = rows.Scan(&id, &name, &second, &activeOrStop, &banned, &total, &okCount, &failed, &pending, &amount, &side)
		} else {
			err = rows.Scan(&id, &name, &second, &banned, &activeOrStop, &extra, &total, &okCount, &failed, &pending, &amount, &side)
		}
		if err != nil {
			return nil, err
		}
		part, convErr := decimal.NewFromString(amount)
		if convErr != nil {
			part = decimal.Zero
		}
		turnover = turnover.Add(part)
		requests += total
		accepted += okCount
		apiOrDisputes += side
		if total > 0 || side > 0 {
			withTraffic++
		}
		row := map[string]any{
			"id": id, "name": name,
			"requests": total, "accepted": okCount, "failed": failed, "pending": pending,
			"conversion": percent(okCount, total),
			"turnover":   beauty(part.String(), "usdt"),
		}
		if merchant {
			row["domain"] = second
			row["active"] = activeOrStop == 1
			row["banned"] = banned == 1
			row["api_requests"] = side
		} else {
			row["email"] = second
			row["banned"] = banned == 1
			row["stop_traffic"] = activeOrStop == 1
			row["online"] = extra == 1
			row["disputes"] = side
		}
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	totals := map[string]any{
		"requests":   requests,
		"accepted":   accepted,
		"conversion": percent(accepted, requests),
		"turnover":   beauty(turnover.String(), "usdt"),
	}
	key := "traders"
	if merchant {
		totals["api_requests"] = apiOrDisputes
		totals["merchants_with_traffic"] = withTraffic
		key = "merchants"
	} else {
		totals["disputes"] = apiOrDisputes
		totals["traders_with_traffic"] = withTraffic
	}
	return map[string]any{key: items, "totals": totals}, nil
}

func percent(part, total int) int {
	if total == 0 {
		return 0
	}
	return part * 100 / total
}
