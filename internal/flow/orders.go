package flow

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"finance-platform/go/internal/market"
	"finance-platform/go/internal/money"
	"finance-platform/go/internal/profits"

	"github.com/shopspring/decimal"
)

type gatewayRow struct {
	ID          int64
	Code        string
	Currency    string
	Min         string
	Max         string
	TotalRate   float64
	TraderRate  float64
	Reservation int
	DetailTypes []string
}

// CreateOrderInput is a merchant POST /api/v1/order.
type CreateOrderInput struct {
	UserID      int64
	ExternalID  string
	MerchantID  string
	Amount      string
	Currency    string
	GatewayCode string
	DetailType  string
	Callback    string
	MaxWait     time.Duration
}

type orderDraft struct {
	ExternalID   string
	MerchantDB   int64
	MerchantUUID string
	Callback     string
	DetailType   string
	Fiat         money.Money
	Gateway      gatewayRow
	Rate         money.Money
	RateText     string
	Market       string
}

// CreateOrder assigns a trader requisite and reserves trust.
func (s *Service) CreateOrder(ctx context.Context, in CreateOrderInput) (string, error) {
	if in.ExternalID == "" || in.MerchantID == "" || in.Amount == "" {
		return "", invalid(map[string][]string{
			"amount":      {"The amount field is required."},
			"external_id": {"The external id field is required."},
			"merchant_id": {"The merchant id field is required."},
		})
	}
	if in.GatewayCode != "" && in.Currency != "" {
		return "", invalid(map[string][]string{
			"currency":        {"The currency field prohibits payment gateway."},
			"payment_gateway": {"The payment gateway field prohibits currency."},
		})
	}
	if in.GatewayCode == "" && in.Currency == "" {
		return "", invalid(map[string][]string{
			"currency":        {"The currency field is required when payment gateway is not present."},
			"payment_gateway": {"The payment gateway field is required when currency is not present."},
		})
	}
	var merchantDB int64
	var owner int64
	var active bool
	var validated, banned sql.NullTime
	var marketName, gatewaySettings sql.NullString
	var waitMS sql.NullInt64
	var stop int
	err := s.DB.QueryRowContext(ctx, `
		SELECT m.id, m.user_id, m.active, m.validated_at, m.banned_at, m.market, m.max_order_wait_time, m.gateway_settings,
		       COALESCE(u.stop_traffic, 0)
		FROM merchants m
		LEFT JOIN users u ON u.id = m.user_id
		WHERE m.uuid = ?`, in.MerchantID).
		Scan(&merchantDB, &owner, &active, &validated, &banned, &marketName, &waitMS, &gatewaySettings, &stop)
	if err == sql.ErrNoRows {
		return "", fail(400, "Проект не найден по UUID: "+in.MerchantID)
	}
	if err != nil {
		return "", err
	}
	if owner != in.UserID {
		return "", plain(403, "This action is unauthorized.")
	}
	if !validated.Valid {
		return "", fail(400, "Проект находится на модерации.")
	}
	if banned.Valid {
		return "", fail(400, "Проект заблокирован.")
	}
	if stop == 1 {
		return "", fail(400, "Трафик проекта остановлен.")
	}
	if !active {
		return "", fail(400, "Проект отключен.")
	}
	exists, err := s.externalExists(ctx, merchantDB, in.ExternalID)
	if err != nil {
		return "", err
	}
	if exists {
		return "", duplicateExternal()
	}
	gateway, err := s.pickGateway(ctx, in, parseGatewayPrefs(gatewaySettings.String))
	if err != nil {
		return "", err
	}
	fiat, err := money.FromPrecision(in.Amount, gateway.Currency)
	if err != nil {
		return "", invalid(map[string][]string{"amount": {"The amount field must be an integer."}})
	}
	if !withinLimits(in.Amount, gateway.Min, gateway.Max) {
		return "", fail(400, "Подходящие платежные реквизиты не найдены.")
	}
	if s.Quotes == nil {
		return "", fail(400, "Курс валюты не задан.")
	}
	buyUnits, err := market.BuyUnits(ctx, s.Quotes, gateway.Currency, marketOr(marketName.String))
	if err != nil {
		return "", err
	}
	if buyUnits == "" || buyUnits == "0" {
		return "", fail(400, "Курс валюты не задан.")
	}
	rate, err := money.FromUnits(buyUnits, gateway.Currency)
	if err != nil {
		return "", err
	}
	ratePrecision, err := rate.Precision()
	if err != nil {
		return "", err
	}
	draft := orderDraft{
		ExternalID:   in.ExternalID,
		MerchantDB:   merchantDB,
		MerchantUUID: in.MerchantID,
		Callback:     in.Callback,
		DetailType:   in.DetailType,
		Fiat:         fiat,
		Gateway:      gateway,
		Rate:         rate,
		RateText:     ratePrecision,
		Market:       marketOr(marketName.String),
	}
	orderUUID, err := s.assignOrder(ctx, draft)
	if err == nil || !retryable(err) {
		return orderUUID, err
	}
	budget := waitBudget(int(waitMS.Int64), in.MaxWait, s.MaxWait)
	if budget <= 0 {
		return "", err
	}
	return runWait(ctx, time.Now, s.Gate, s.Holds, in.MerchantID+":"+in.ExternalID, time.Now().Add(budget), s.pollInterval(), s.sleep,
		func(ctx context.Context) (string, error) { return s.assignOrder(ctx, draft) },
		func(ctx context.Context) (bool, error) { return s.externalExists(ctx, merchantDB, in.ExternalID) },
	)
}

func (s *Service) assignOrder(ctx context.Context, draft orderDraft) (string, error) {
	exists, err := s.externalExists(ctx, draft.MerchantDB, draft.ExternalID)
	if err != nil {
		return "", err
	}
	if exists {
		return "", duplicateExternal()
	}
	orderUUID, err := newUUID()
	if err != nil {
		return "", err
	}
	rules, err := s.loadPaySettings(ctx)
	if err != nil {
		return "", err
	}
	total, err := draft.Fiat.Convert(draft.Rate, "usdt")
	if err != nil {
		return "", err
	}
	needTrust := trustFloor(total.Units())
	err = s.tx(ctx, func(tx *sql.Tx) error {
		detail, traderRate, err := s.lockDetail(ctx, tx, draft, rules.MaxDisputes, needTrust)
		if err != nil {
			return err
		}
		if primeActive(s.now(), rules.PrimeStart, rules.PrimeEnd) {
			traderRate = addPrime(traderRate, rules.PrimeRate)
		}
		share, err := profits.Calculate(draft.Fiat, draft.Rate, draft.Gateway.TotalRate, traderRate)
		if err != nil {
			return fail(400, err.Error())
		}
		service, cuts, err := s.applyLeaders(ctx, tx, detail.UserID, draft.MerchantDB, share.Total, share.Service)
		if err != nil {
			return err
		}
		now := s.now()
		expires := now.Add(time.Duration(draft.Gateway.Reservation) * time.Minute)
		var callback any
		if draft.Callback != "" {
			callback = draft.Callback
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO orders (
				uuid, external_id, merchant_id, base_amount, amount,
				total_profit, trader_profit, merchant_profit, service_profit, trader_paid_for_order,
				currency, market, base_conversion_price, conversion_price,
				trader_commission_rate, total_service_commission_rate,
				status, sub_status, callback_url, payment_gateway_id, payment_detail_id,
				trader_id, expires_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 'waiting_for_payment', ?, ?, ?, ?, ?, ?, ?)`,
			orderUUID, draft.ExternalID, draft.MerchantDB, draft.Fiat.Units(), draft.Fiat.Units(),
			share.Total.Units(), share.Trader.Units(), share.Merchant.Units(), service.Units(), share.Paid.Units(),
			draft.Gateway.Currency, draft.Market, draft.RateText, draft.Rate.Units(),
			traderRate, draft.Gateway.TotalRate,
			callback, draft.Gateway.ID, detail.ID, detail.UserID, expires, now, now)
		if err != nil {
			return err
		}
		orderID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if err := insertCuts(ctx, tx, orderID, cuts, now); err != nil {
			return err
		}
		if err := addDaily(ctx, tx, detail.ID, detail.Current, draft.Fiat, draft.Gateway.Currency, now); err != nil {
			return err
		}
		if err := takeTrust(ctx, tx, detail.UserID, share.Paid.Units(), now); err != nil {
			return err
		}
		return s.enqueueOrder(ctx, tx, orderID)
	})
	if err != nil {
		if s.Cascade && retryable(err) {
			return s.assignCascade(ctx, draft, orderUUID)
		}
		return "", err
	}
	return orderUUID, nil
}

func duplicateExternal() *Error {
	return invalid(map[string][]string{
		"external_id": {"Заказ с таким external_id уже существует для данного проекта."},
	})
}

func (s *Service) externalExists(ctx context.Context, merchantDB int64, externalID string) (bool, error) {
	var dup int
	err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM orders WHERE merchant_id = ? AND external_id = ? LIMIT 1`, merchantDB, externalID).Scan(&dup)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// CancelOrder fails a pending order and returns trust.
func (s *Service) CancelOrder(ctx context.Context, userID int64, orderUUID string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		row, err := lockOrder(ctx, tx, orderUUID)
		if err != nil {
			return err
		}
		if row.Owner != userID {
			return plain(403, "This action is unauthorized.")
		}
		if err := guard(ctx, tx, row, "It is not possible to cancel a completed order.", "Unable to cancel an order in dispute."); err != nil {
			return err
		}
		return s.markFailed(ctx, tx, row, "cancelled")
	})
}

// FinishOrder closes a pending order as success. Local stands only.
func (s *Service) FinishOrder(ctx context.Context, userID int64, orderUUID, subStatus string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		row, err := lockOrder(ctx, tx, orderUUID)
		if err != nil {
			return err
		}
		if row.Owner != userID && row.TraderID != userID {
			super, err := hasRole(ctx, s.DB, userID, "Admin")
			if err != nil {
				return err
			}
			if !super {
				return plain(403, "This action is unauthorized.")
			}
		}
		if err := guard(ctx, tx, row, "It is not possible to finish a completed order.", "Unable to finish an order in dispute."); err != nil {
			return err
		}
		return s.markSuccess(ctx, tx, row, subStatus)
	})
}

// AcceptByTrader closes the trader's pending order by hand.
func (s *Service) AcceptByTrader(ctx context.Context, traderID int64, orderUUID string) error {
	if err := s.requireRole(ctx, traderID, "Trader"); err != nil {
		return err
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		row, err := lockOrder(ctx, tx, orderUUID)
		if err != nil {
			return err
		}
		if row.TraderID != traderID {
			return plain(403, "This action is unauthorized.")
		}
		if err := guard(ctx, tx, row, "It is not possible to finish a completed order.", "Unable to finish an order in dispute."); err != nil {
			return err
		}
		return s.markSuccess(ctx, tx, row, "accepted")
	})
}

type orderLock struct {
	ID        int64
	UUID      string
	Status    string
	Owner     int64
	TraderID  int64
	DetailID  sql.NullInt64
	MerchantP string
	Paid      string
	Amount    string
	Currency  string
	Created   time.Time
}

func lockOrder(ctx context.Context, tx *sql.Tx, orderUUID string) (orderLock, error) {
	var row orderLock
	var trader sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT o.id, o.uuid, o.status, m.user_id, o.trader_id, o.payment_detail_id,
		       o.merchant_profit, o.trader_paid_for_order, o.amount, o.currency, o.created_at
		FROM orders o
		INNER JOIN merchants m ON m.id = o.merchant_id
		WHERE o.uuid = ? FOR UPDATE`, orderUUID).
		Scan(&row.ID, &row.UUID, &row.Status, &row.Owner, &trader, &row.DetailID,
			&row.MerchantP, &row.Paid, &row.Amount, &row.Currency, &row.Created)
	if err == sql.ErrNoRows {
		return row, plain(404, "No query results for model [App\\Models\\Order].")
	}
	if err != nil {
		return row, err
	}
	if trader.Valid {
		row.TraderID = trader.Int64
	}
	return row, nil
}

func guard(ctx context.Context, tx *sql.Tx, row orderLock, done, disputeMsg string) error {
	var disputeID int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM disputes WHERE order_id = ? LIMIT 1`, row.ID).Scan(&disputeID)
	if err == nil {
		return fail(400, disputeMsg)
	}
	if err != sql.ErrNoRows {
		return err
	}
	if row.Status != "pending" {
		return fail(400, done)
	}
	return nil
}

func (s *Service) markSuccess(ctx context.Context, tx *sql.Tx, row orderLock, sub string) error {
	now := s.now()
	if _, err := tx.ExecContext(ctx, `
		UPDATE orders SET status = 'success', sub_status = ?, finished_at = ?, updated_at = ? WHERE id = ?`,
		sub, now, now, row.ID); err != nil {
		return err
	}
	if err := creditMerchant(ctx, tx, row.Owner, zero(row.MerchantP), "income_from_a_successful_order", now); err != nil {
		return err
	}
	if err := s.moveCuts(ctx, tx, row.ID, true, now); err != nil {
		return err
	}
	return s.enqueueOrder(ctx, tx, row.ID)
}

func (s *Service) markFailed(ctx context.Context, tx *sql.Tx, row orderLock, sub string) error {
	now := s.now()
	if _, err := tx.ExecContext(ctx, `
		UPDATE orders SET status = 'fail', sub_status = ?, finished_at = ?, updated_at = ? WHERE id = ?`,
		sub, now, now, row.ID); err != nil {
		return err
	}
	if row.TraderID != 0 {
		if err := refundTrust(ctx, tx, row.TraderID, zero(row.Paid), now); err != nil {
			return err
		}
	}
	if err := subDaily(ctx, tx, row); err != nil {
		return err
	}
	if row.TraderID != 0 {
		if err := s.stopFailedStreak(ctx, tx, row.TraderID, now); err != nil {
			return err
		}
	}
	return s.enqueueOrder(ctx, tx, row.ID)
}

func (s *Service) stopFailedStreak(ctx context.Context, tx *sql.Tx, traderID int64, now time.Time) error {
	rules, err := s.loadPaySettings(ctx)
	if err != nil || rules.FailCount <= 0 {
		return err
	}
	query := `
		SELECT o.status FROM orders o
		INNER JOIN payment_details pd ON pd.id = o.payment_detail_id
		WHERE pd.user_id = ?`
	args := []any{traderID}
	if rules.FailMinutes > 0 {
		query += ` AND o.created_at >= ?`
		args = append(args, now.Add(-time.Duration(rules.FailMinutes)*time.Minute))
	}
	query += ` ORDER BY o.id DESC LIMIT ?`
	args = append(args, rules.FailCount)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	var statuses []string
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			rows.Close()
			return err
		}
		statuses = append(statuses, status)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !failedStreak(statuses, rules.FailCount) {
		return nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE users SET stop_traffic = 1, traffic_enabled_at = NULL, updated_at = ? WHERE id = ?`, now, traderID)
	return err
}

func (s *Service) reopen(ctx context.Context, tx *sql.Tx, row orderLock, sub string) error {
	now := s.now()
	switch row.Status {
	case "success":
		if err := takeMerchant(ctx, tx, row.Owner, zero(row.MerchantP), "rollback_income_from_a_successful_order", now); err != nil {
			return err
		}
		if err := s.moveCuts(ctx, tx, row.ID, false, now); err != nil {
			return err
		}
	case "fail":
		if err := takeTrust(ctx, tx, row.TraderID, zero(row.Paid), now); err != nil {
			return err
		}
		if err := addDailyRaw(ctx, tx, row); err != nil {
			return err
		}
	default:
		return fail(400, "Сделка уже открыта.")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE orders SET status = 'pending', sub_status = ?, finished_at = NULL, updated_at = ? WHERE id = ?`,
		sub, now, row.ID); err != nil {
		return err
	}
	return s.enqueueOrder(ctx, tx, row.ID)
}

func (s *Service) pickGateway(ctx context.Context, in CreateOrderInput, prefs gatewayPrefs) (gatewayRow, error) {
	if in.GatewayCode != "" {
		row, err := s.gatewayByCode(ctx, in.GatewayCode)
		if err != nil {
			return gatewayRow{}, err
		}
		if in.DetailType != "" && !contains(row.DetailTypes, in.DetailType) {
			return gatewayRow{}, fail(400, "Подходящие платежные реквизиты не найдены.")
		}
		row, ok := applyGatewayPrefs(row, prefs)
		if !ok {
			return gatewayRow{}, fail(400, "Подходящие платежные реквизиты не найдены.")
		}
		return row, nil
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, code, currency, min_limit, max_limit, total_service_commission_rate_for_orders,
		       trader_commission_rate_for_orders, reservation_time_for_orders, detail_types
		FROM payment_gateways
		WHERE is_active = 1 AND currency = ?
		ORDER BY id`, strings.ToLower(in.Currency))
	if err != nil {
		return gatewayRow{}, err
	}
	defer rows.Close()
	for rows.Next() {
		row, err := scanGateway(rows)
		if err != nil {
			return gatewayRow{}, err
		}
		if in.DetailType != "" && !contains(row.DetailTypes, in.DetailType) {
			continue
		}
		if !withinLimits(in.Amount, row.Min, row.Max) {
			continue
		}
		row, ok := applyGatewayPrefs(row, prefs)
		if !ok {
			continue
		}
		return row, nil
	}
	return gatewayRow{}, fail(400, "Подходящие платежные реквизиты не найдены.")
}

func (s *Service) gatewayByCode(ctx context.Context, code string) (gatewayRow, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, code, currency, min_limit, max_limit, total_service_commission_rate_for_orders,
		       trader_commission_rate_for_orders, reservation_time_for_orders, detail_types
		FROM payment_gateways WHERE code = ?`, code)
	got, err := scanGateway(row)
	if err == sql.ErrNoRows {
		return gatewayRow{}, invalid(map[string][]string{"payment_gateway": {"Выбранный платежный шлюз не существует."}})
	}
	return got, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanGateway(row scanner) (gatewayRow, error) {
	var got gatewayRow
	var minLimit, maxLimit, types sql.NullString
	err := row.Scan(&got.ID, &got.Code, &got.Currency, &minLimit, &maxLimit, &got.TotalRate, &got.TraderRate, &got.Reservation, &types)
	if err != nil {
		return got, err
	}
	got.Min = minLimit.String
	got.Max = maxLimit.String
	if types.Valid && types.String != "" {
		_ = json.Unmarshal([]byte(types.String), &got.DetailTypes)
	}
	return got, nil
}

type detailRow struct {
	ID      int64
	UserID  int64
	Current string
	Rate    sql.NullFloat64
}

func (s *Service) lockDetail(ctx context.Context, tx *sql.Tx, draft orderDraft, maxDisputes int, needTrust string) (detailRow, float64, error) {
	priorities, err := categoryOrder(ctx, tx, draft.MerchantDB)
	if err != nil {
		return detailRow{}, 0, err
	}
	query := `
		SELECT pd.id, pd.user_id, pd.current_daily_limit, pd.daily_limit, pd.max_pending_orders_quantity,
		       pd.min_order_amount, pd.max_order_amount, pd.order_interval_minutes, pd.last_used_at,
		       CAST(pd.unique_amount_percentage AS CHAR), pd.unique_amount_seconds, u.trader_commission_rate, u.trader_category_id
		FROM payment_details pd
		INNER JOIN users u ON u.id = pd.user_id
		INNER JOIN payment_detail_payment_gateway link ON link.payment_detail_id = pd.id
		WHERE pd.is_active = 1 AND pd.archived_at IS NULL AND pd.is_external = 0
		  AND u.is_online = 1 AND u.stop_traffic = 0 AND u.banned_at IS NULL
		  AND link.payment_gateway_id = ? AND pd.currency = ?`
	args := []any{draft.Gateway.ID, draft.Gateway.Currency}
	if draft.DetailType != "" {
		query += ` AND pd.detail_type = ?`
		args = append(args, draft.DetailType)
	}
	query += ` ORDER BY pd.id FOR UPDATE`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return detailRow{}, 0, err
	}
	type candidate struct {
		detail     detailRow
		daily      string
		maxPending int
		minAmount  sql.NullString
		maxAmount  sql.NullString
		interval   sql.NullInt64
		lastUsed   sql.NullTime
		uniquePct  sql.NullString
		uniqueSecs sql.NullInt64
		category   sql.NullInt64
		rate       float64
	}
	var found []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(
			&item.detail.ID, &item.detail.UserID, &item.detail.Current, &item.daily, &item.maxPending,
			&item.minAmount, &item.maxAmount, &item.interval, &item.lastUsed,
			&item.uniquePct, &item.uniqueSecs, &item.detail.Rate, &item.category,
		); err != nil {
			rows.Close()
			return detailRow{}, 0, err
		}
		found = append(found, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return detailRow{}, 0, err
	}
	rows.Close()
	var ready []candidate
	for _, item := range found {
		var pending int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE payment_detail_id = ? AND status = 'pending'`, item.detail.ID).Scan(&pending); err != nil {
			return detailRow{}, 0, err
		}
		if item.maxPending > 0 && pending >= item.maxPending {
			continue
		}
		if !dailyFits(item.daily, item.detail.Current, draft.Fiat) {
			continue
		}
		if !boundFits(item.minAmount.String, item.maxAmount.String, draft.Fiat) {
			continue
		}
		minutes := 0
		if item.interval.Valid {
			minutes = int(item.interval.Int64)
		}
		if !intervalOpen(minutes, item.lastUsed, s.now()) {
			continue
		}
		taken, err := amountTaken(ctx, tx, item.detail.ID, draft.Fiat.Units(), item.uniquePct, item.uniqueSecs, s.now())
		if err != nil {
			return detailRow{}, 0, err
		}
		if taken {
			continue
		}
		if maxDisputes > 0 {
			var disputes int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM disputes WHERE trader_id = ? AND status = 'pending'`, item.detail.UserID).Scan(&disputes); err != nil {
				return detailRow{}, 0, err
			}
			if disputes >= maxDisputes {
				continue
			}
		}
		rank := categoryRank(item.category, priorities)
		if rank < 0 {
			continue
		}
		item.rate = draft.Gateway.TraderRate
		if item.detail.Rate.Valid {
			item.rate = item.detail.Rate.Float64
		}
		ready = append(ready, item)
	}
	if len(ready) == 0 {
		return detailRow{}, 0, requisiteMiss()
	}
	sort.SliceStable(ready, func(i, j int) bool {
		return lessDetail(rankedDetail{
			rank: categoryRank(ready[i].category, priorities), lastUsed: ready[i].lastUsed, id: ready[i].detail.ID,
		}, rankedDetail{
			rank: categoryRank(ready[j].category, priorities), lastUsed: ready[j].lastUsed, id: ready[j].detail.ID,
		})
	})
	for _, item := range ready {
		ok, err := trustCovers(ctx, tx, item.detail.UserID, needTrust)
		if err != nil {
			return detailRow{}, 0, err
		}
		if ok {
			return item.detail, item.rate, nil
		}
	}
	return detailRow{}, 0, requisiteMiss()
}

func categoryOrder(ctx context.Context, tx *sql.Tx, merchantID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT trader_category_id FROM merchant_trader_category_priorities
		WHERE merchant_id = ? ORDER BY priority, id`, merchantID)
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
	return ids, rows.Err()
}

func amountTaken(ctx context.Context, tx *sql.Tx, detailID int64, units string, percent sql.NullString, seconds sql.NullInt64, now time.Time) (bool, error) {
	var pending int
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM orders WHERE payment_detail_id = ? AND status = 'pending' AND amount = ? LIMIT 1`,
		detailID, units).Scan(&pending)
	if err == nil {
		return true, nil
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	pct := "3"
	if percent.Valid && percent.String != "" {
		pct = percent.String
	}
	window := 600
	if seconds.Valid && seconds.Int64 > 0 {
		window = int(seconds.Int64)
	}
	low, high, ok := uniqueBounds(units, pct)
	if !ok {
		return false, nil
	}
	var hit int
	err = tx.QueryRowContext(ctx, `
		SELECT 1 FROM orders
		WHERE payment_detail_id = ? AND status = 'success' AND finished_at >= ?
		  AND CAST(amount AS DECIMAL(65,0)) BETWEEN ? AND ?
		LIMIT 1`, detailID, now.Add(-time.Duration(window)*time.Second), low, high).Scan(&hit)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func trustCovers(ctx context.Context, tx *sql.Tx, userID int64, units string) (bool, error) {
	// The trader filter uses trust_balance. Reserve is spent only inside takeTrust
	// when that locked trust row does not cover trader_paid_for_order.
	var trust string
	err := tx.QueryRowContext(ctx, `SELECT trust_balance FROM wallets WHERE user_id = ? FOR UPDATE`, userID).Scan(&trust)
	if err != nil {
		return false, err
	}
	have, err := money.FromUnits(zero(trust), "usdt")
	if err != nil {
		return false, err
	}
	need, err := money.FromUnits(zero(units), "usdt")
	if err != nil {
		return false, err
	}
	return have.GreaterOrEqual(need)
}

func withinLimits(amount, minLimit, maxLimit string) bool {
	value, err := decimal.NewFromString(amount)
	if err != nil {
		return false
	}
	if minLimit != "" && minLimit != "0" {
		floor, err := decimal.NewFromString(minLimit)
		if err == nil && value.LessThan(floor) {
			return false
		}
	}
	if maxLimit != "" && maxLimit != "0" {
		ceiling, err := decimal.NewFromString(maxLimit)
		if err == nil && value.GreaterThan(ceiling) {
			return false
		}
	}
	return true
}

func dailyFits(limit, current string, amount money.Money) bool {
	if limit == "" {
		return false
	}
	cap, err := decimal.NewFromString(limit)
	if err != nil {
		return false
	}
	used, err := decimal.NewFromString(zero(current))
	if err != nil {
		return false
	}
	need, err := decimal.NewFromString(amount.Units())
	if err != nil {
		return false
	}
	return cap.Sub(used).GreaterThanOrEqual(need)
}

func boundFits(minAmount, maxAmount string, amount money.Money) bool {
	units, err := decimal.NewFromString(amount.Units())
	if err != nil {
		return false
	}
	if minAmount != "" && minAmount != "0" {
		floor, err := decimal.NewFromString(minAmount)
		if err != nil || units.LessThan(floor) {
			return false
		}
	}
	if maxAmount != "" && maxAmount != "0" {
		ceiling, err := decimal.NewFromString(maxAmount)
		if err != nil || units.GreaterThan(ceiling) {
			return false
		}
	}
	return true
}

func intervalOpen(minutes int, last sql.NullTime, now time.Time) bool {
	if minutes <= 0 || !last.Valid {
		return true
	}
	return !now.Before(last.Time.Add(time.Duration(minutes) * time.Minute))
}

func addDaily(ctx context.Context, tx *sql.Tx, id int64, current string, amount money.Money, currency string, now interface{}) error {
	used, err := money.FromUnits(zero(current), currency)
	if err != nil {
		return err
	}
	next, err := used.AddMoney(amount)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE payment_details SET current_daily_limit = ?, last_used_at = ?, updated_at = ? WHERE id = ?`, next.Units(), now, now, id)
	return err
}

func addDailyRaw(ctx context.Context, tx *sql.Tx, row orderLock) error {
	if !row.DetailID.Valid {
		return nil
	}
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT current_daily_limit FROM payment_details WHERE id = ? FOR UPDATE`, row.DetailID.Int64).Scan(&current); err != nil {
		return err
	}
	amount, err := money.FromUnits(zero(row.Amount), row.Currency)
	if err != nil {
		return err
	}
	return addDaily(ctx, tx, row.DetailID.Int64, current, amount, row.Currency, row.Created)
}

func subDaily(ctx context.Context, tx *sql.Tx, row orderLock) error {
	if !row.DetailID.Valid {
		return nil
	}
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT current_daily_limit FROM payment_details WHERE id = ? FOR UPDATE`, row.DetailID.Int64).Scan(&current); err != nil {
		return err
	}
	used, err := money.FromUnits(zero(current), row.Currency)
	if err != nil {
		return err
	}
	amount, err := money.FromUnits(zero(row.Amount), row.Currency)
	if err != nil {
		return err
	}
	next, err := used.SubMoney(amount)
	if err != nil {
		return err
	}
	negative, err := next.LessThanZero()
	if err != nil {
		return err
	}
	if negative {
		next, err = money.Zero(row.Currency)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE payment_details SET current_daily_limit = ? WHERE id = ?`, next.Units(), row.DetailID.Int64)
	return err
}

func marketOr(value string) string {
	if value == "" {
		return "bybit"
	}
	return value
}
