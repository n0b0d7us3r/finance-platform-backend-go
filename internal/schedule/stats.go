package schedule

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"finance-platform/go/internal/money"

	"github.com/shopspring/decimal"
)

func (j Jobs) updateStatistics(ctx context.Context, now time.Time) (int, error) {
	var rows int
	if err := j.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM merchant_api_statistics`).Scan(&rows); err != nil {
		return 0, err
	}
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -1)
	to := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())
	if rows == 0 {
		from = now.AddDate(-2, 0, 0)
	}
	gateways, err := j.gatewayCurrencies(ctx)
	if err != nil {
		return 0, err
	}
	listed, err := j.DB.QueryContext(ctx, `
		SELECT CAST(DATE(created_at) AS CHAR), is_successful, COALESCE(NULLIF(currency, ''), payment_gateway), COUNT(*), CAST(COALESCE(SUM(amount), 0) AS CHAR)
		FROM merchant_api_request_logs
		WHERE created_at >= ? AND created_at <= ?
		GROUP BY CAST(DATE(created_at) AS CHAR), is_successful, COALESCE(NULLIF(currency, ''), payment_gateway)`, from, to)
	if err != nil {
		return 0, err
	}
	defer listed.Close()
	type bucket struct {
		date     string
		success  int
		currency string
		count    int
		sum      decimal.Decimal
	}
	grouped := map[string]*bucket{}
	for listed.Next() {
		var (
			date    string
			success int
			key     sql.NullString
			count   int
			sum     string
		)
		if err := listed.Scan(&date, &success, &key, &count, &sum); err != nil {
			return 0, err
		}
		currency := key.String
		if !money.IsKnown(currency) {
			if mapped, ok := gateways[currency]; ok {
				currency = mapped
			}
		}
		id := date + "|" + strconv.Itoa(success) + "|" + currency
		amount, err := decimal.NewFromString(sum)
		if err != nil {
			amount = decimal.Zero
		}
		item := grouped[id]
		if item == nil {
			grouped[id] = &bucket{date: date, success: success, currency: currency, count: count, sum: amount}
			continue
		}
		item.count += count
		item.sum = item.sum.Add(amount)
	}
	if err := listed.Err(); err != nil {
		return 0, err
	}
	written := 0
	for _, item := range grouped {
		if _, err := j.DB.ExecContext(ctx, `
			INSERT INTO merchant_api_statistics (date, is_successful, currency, count, sum_amount, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE count = VALUES(count), sum_amount = VALUES(sum_amount), updated_at = VALUES(updated_at)`,
			item.date, item.success, item.currency, item.count, item.sum.String(), now, now); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

func (j Jobs) gatewayCurrencies(ctx context.Context) (map[string]string, error) {
	rows, err := j.DB.QueryContext(ctx, `SELECT code, currency FROM payment_gateways WHERE code IS NOT NULL AND code <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var code, currency sql.NullString
		if err := rows.Scan(&code, &currency); err != nil {
			return nil, err
		}
		out[code.String] = currency.String
	}
	return out, rows.Err()
}
