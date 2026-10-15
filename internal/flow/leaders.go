package flow

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"

	"finance-platform/go/internal/money"
	"finance-platform/go/internal/profits"
)

type leaderCut struct {
	UserID int64
	Rate   string
	Source string
	Units  string
}

func (s *Service) applyLeaders(ctx context.Context, tx *sql.Tx, traderID, merchantID int64, total, service money.Money) (money.Money, []leaderCut, error) {
	rates, err := leaderRates(ctx, tx, traderID, merchantID)
	if err != nil {
		return money.Money{}, nil, err
	}
	cuts := make([]leaderCut, 0, len(rates))
	parts := make([]money.Money, 0, len(rates))
	for _, rate := range rates {
		part, err := profits.LeaderProfit(total, rate.Rate)
		if err != nil {
			return money.Money{}, nil, err
		}
		positive, err := part.GreaterThanZero()
		if err != nil {
			return money.Money{}, nil, err
		}
		if !positive {
			continue
		}
		cuts = append(cuts, leaderCut{UserID: rate.UserID, Rate: rate.Rate, Source: rate.Source, Units: part.Units()})
		parts = append(parts, part)
	}
	net, err := profits.NetService(service, parts)
	if err != nil {
		return money.Money{}, nil, err
	}
	return net, cuts, nil
}

type leaderRate struct {
	UserID int64
	Rate   string
	Source string
}

func leaderRates(ctx context.Context, tx *sql.Tx, traderID, merchantID int64) ([]leaderRate, error) {
	trader, err := traderLeaderRates(ctx, tx, traderID)
	if err != nil {
		return nil, err
	}
	merchant, err := merchantLeaderRates(ctx, tx, merchantID)
	if err != nil {
		return nil, err
	}
	return append(trader, merchant...), nil
}

func traderLeaderRates(ctx context.Context, tx *sql.Tx, traderID int64) ([]leaderRate, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT team_leader_id, CAST(commission_percentage AS CHAR)
		FROM trader_team_leader_relations
		WHERE trader_id = ?
		ORDER BY id`, traderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []leaderRate
	seen := map[int64]struct{}{}
	for rows.Next() {
		var item leaderRate
		if err := rows.Scan(&item.UserID, &item.Rate); err != nil {
			return nil, err
		}
		item.Source = "trader_relation_new_system"
		if _, ok := seen[item.UserID]; ok {
			continue
		}
		seen[item.UserID] = struct{}{}
		found = append(found, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(found) > 0 {
		return found, nil
	}
	var raw sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT additional_team_leader_ids FROM users WHERE id = ?`, traderID).Scan(&raw)
	if err == sql.ErrNoRows || !raw.Valid || raw.String == "" || raw.String == "null" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []int64
	if err := json.Unmarshal([]byte(raw.String), &ids); err != nil {
		return nil, nil
	}
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		var rate string
		err := tx.QueryRowContext(ctx, `SELECT CAST(referral_commission_percentage AS CHAR) FROM users WHERE id = ?`, id).Scan(&rate)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		seen[id] = struct{}{}
		found = append(found, leaderRate{UserID: id, Rate: rate, Source: "trader_additional_old_system"})
	}
	return found, nil
}

func merchantLeaderRates(ctx context.Context, tx *sql.Tx, merchantID int64) ([]leaderRate, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT team_leader_id, CAST(commission_percentage AS CHAR)
		FROM merchant_team_leader_relations
		WHERE merchant_id = ?
		ORDER BY id`, merchantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []leaderRate
	for rows.Next() {
		var item leaderRate
		if err := rows.Scan(&item.UserID, &item.Rate); err != nil {
			return nil, err
		}
		item.Source = "merchant"
		found = append(found, item)
	}
	return found, rows.Err()
}

func insertCuts(ctx context.Context, tx *sql.Tx, orderID int64, cuts []leaderCut, now interface{}) error {
	for _, cut := range cuts {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO order_additional_profits (
				order_id, team_leader_id, commission_rate, profit_amount, source, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			orderID, cut.UserID, cut.Rate, cut.Units, cut.Source, now, now)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) moveCuts(ctx context.Context, tx *sql.Tx, orderID int64, add bool, now interface{}) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT team_leader_id, CAST(profit_amount AS CHAR)
		FROM order_additional_profits
		WHERE order_id = ?
		ORDER BY team_leader_id, id`, orderID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type item struct {
		user  int64
		units string
	}
	var found []item
	for rows.Next() {
		var one item
		if err := rows.Scan(&one.user, &one.units); err != nil {
			return err
		}
		found = append(found, one)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	sort.Slice(found, func(i, j int) bool { return found[i].user < found[j].user })
	for _, one := range found {
		if err := moveTeamleader(ctx, tx, one.user, one.units, add, now); err != nil {
			return err
		}
	}
	return nil
}
