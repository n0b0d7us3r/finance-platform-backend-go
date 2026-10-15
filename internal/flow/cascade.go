package flow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"finance-platform/go/internal/cascade"
	"finance-platform/go/internal/cascade/cashout"
	"finance-platform/go/internal/event"
	"finance-platform/go/internal/outbox"
	"finance-platform/go/internal/profits"
	"finance-platform/go/internal/read"
)

func (s *Service) assignCascade(ctx context.Context, draft orderDraft, orderUUID string) (string, error) {
	if s.CascadeDetailID <= 0 {
		return "", requisiteMiss()
	}
	amount, err := draft.Fiat.Precision()
	if err != nil {
		return "", err
	}
	terminals, err := s.loadTerminals(ctx, draft.MerchantDB)
	if err != nil {
		return "", err
	}
	var opened cascade.Quote
	var terminal cascade.Terminal
	matched := false
	for _, candidate := range cascade.Select(true, terminals, amount, draft.DetailType) {
		if !cashout.Handles(candidate.Integration) {
			continue
		}
		quote, openErr := cashout.Open(ctx, s.HTTP, candidate, cascade.Request{
			OrderUUID:  orderUUID,
			Amount:     amount,
			Currency:   draft.Gateway.Currency,
			DetailType: draft.DetailType,
		})
		params := providerParams(amount, draft.Gateway.Currency, orderUUID)
		s.logProviderCall(ctx, candidate, draft.MerchantDB, params, quote, openErr)
		if openErr != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			continue
		}
		opened = quote
		terminal = candidate
		matched = true
		break
	}
	if !matched {
		return "", requisiteMiss()
	}
	if opened.DetailType == "" {
		opened.DetailType = draft.DetailType
	}
	if opened.DetailType == "" {
		opened.DetailType = "card"
	}
	orderID, err := s.insertCascadeOrder(ctx, draft, orderUUID, terminal, opened, providerParams(amount, draft.Gateway.Currency, orderUUID))
	if err != nil {
		return "", err
	}
	if s.Requisites != nil {
		if err := s.Requisites.Save(ctx, orderID, read.External{
			Detail:   opened.Detail,
			Type:     opened.DetailType,
			Initials: opened.Initials,
		}); err != nil {
			return "", err
		}
	}
	return orderUUID, nil
}

func (s *Service) loadTerminals(ctx context.Context, merchantID int64) ([]cascade.Terminal, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT t.id, t.uuid, CAST(t.min_sum AS CHAR), CAST(t.max_sum AS CHAR), t.rate,
		       t.max_response_time_ms, t.number_of_retries, t.additional_settings, t.enabled_detail_types,
		       p.id, p.uuid, p.name, p.integration, p.trader_id, p.webhook_mode, CAST(w.trust_balance AS CHAR)
		FROM provider_terminals t
		INNER JOIN providers p ON p.id = t.provider_id AND p.is_active = 1
		INNER JOIN provider_terminal_merchant bind
		        ON bind.provider_terminal_id = t.id AND bind.merchant_id = ? AND bind.is_active = 1
		LEFT JOIN wallets w ON w.user_id = p.trader_id
		WHERE t.is_active = 1`, merchantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cascade.Terminal
	for rows.Next() {
		var (
			item        cascade.Terminal
			minSum      sql.NullString
			maxSum      sql.NullString
			rate        sql.NullFloat64
			timeoutMS   sql.NullInt64
			retries     sql.NullInt64
			settings    []byte
			typesRaw    []byte
			trader      sql.NullInt64
			trust       sql.NullString
			webhook     sql.NullString
			name        sql.NullString
			integration sql.NullString
		)
		if err := rows.Scan(
			&item.ID, &item.UUID, &minSum, &maxSum, &rate, &timeoutMS, &retries, &settings, &typesRaw,
			&item.ProviderID, &item.ProviderUUID, &name, &integration, &trader, &webhook, &trust,
		); err != nil {
			return nil, err
		}
		item.Min = minSum.String
		item.Max = maxSum.String
		item.Rate = rate.Float64
		item.TimeoutMS = int(timeoutMS.Int64)
		item.Retries = int(retries.Int64)
		item.Settings = settings
		item.ProviderName = name.String
		item.Integration = integration.String
		item.WebhookMode = webhook.String
		item.Trust = trust.String
		if trader.Valid {
			item.TraderID = trader.Int64
		}
		_ = json.Unmarshal(typesRaw, &item.DetailTypes)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) insertCascadeOrder(ctx context.Context, draft orderDraft, orderUUID string, terminal cascade.Terminal, quote cascade.Quote, params []byte) (int64, error) {
	rules, err := s.loadPaySettings(ctx)
	if err != nil {
		return 0, err
	}
	traderRate := draft.Gateway.TraderRate
	if primeActive(s.now(), rules.PrimeStart, rules.PrimeEnd) {
		traderRate = addPrime(traderRate, rules.PrimeRate)
	}
	var orderID int64
	err = s.tx(ctx, func(tx *sql.Tx) error {
		var detail detailRow
		var current sql.NullString
		err := tx.QueryRowContext(ctx, `
			SELECT id, user_id, current_daily_limit
			FROM payment_details WHERE id = ? FOR UPDATE`, s.CascadeDetailID).
			Scan(&detail.ID, &detail.UserID, &current)
		if err == sql.ErrNoRows {
			return requisiteMiss()
		}
		if err != nil {
			return err
		}
		detail.Current = current.String
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
		var providerOrder any
		if quote.ProviderOrderID != "" {
			providerOrder = quote.ProviderOrderID
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO orders (
				uuid, external_id, merchant_id, base_amount, amount,
				total_profit, trader_profit, merchant_profit, service_profit, trader_paid_for_order,
				currency, market, base_conversion_price, conversion_price,
				trader_commission_rate, total_service_commission_rate,
				status, sub_status, callback_url, payment_gateway_id, payment_detail_id,
				trader_id, provider_id, provider_terminal_id, provider_order_id,
				expires_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 'waiting_for_payment', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			orderUUID, draft.ExternalID, draft.MerchantDB, draft.Fiat.Units(), draft.Fiat.Units(),
			share.Total.Units(), share.Trader.Units(), share.Merchant.Units(), service.Units(), share.Paid.Units(),
			draft.Gateway.Currency, draft.Market, draft.RateText, draft.Rate.Units(),
			traderRate, draft.Gateway.TotalRate,
			callback, draft.Gateway.ID, detail.ID, detail.UserID, terminal.ProviderID, terminal.ID, providerOrder,
			expires, now, now)
		if err != nil {
			return err
		}
		orderID, err = res.LastInsertId()
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
		if err := s.logProviderCallTx(ctx, tx, terminal, draft.MerchantDB, orderID, params, quote, nil); err != nil {
			return err
		}
		return s.enqueueOrder(ctx, tx, orderID)
	})
	if err != nil {
		return 0, err
	}
	return orderID, nil
}

func providerParams(amount, currency, orderUUID string) []byte {
	raw, _ := json.Marshal(map[string]string{
		"amount":                amount,
		"currency":              currency,
		"merchantTransactionId": orderUUID,
	})
	return raw
}

func (s *Service) logProviderCall(ctx context.Context, terminal cascade.Terminal, merchantID int64, params []byte, quote cascade.Quote, openErr error) {
	if openErr == nil {
		return
	}
	_ = s.logProviderCallTx(ctx, nil, terminal, merchantID, 0, params, quote, openErr)
}

func (s *Service) logProviderCallTx(ctx context.Context, tx *sql.Tx, terminal cascade.Terminal, merchantID, orderID int64, params []byte, quote cascade.Quote, openErr error) error {
	response := jsonValue(quote.Response)
	success := 0
	var message any
	if openErr == nil {
		success = 1
	} else {
		message = trimText(openErr.Error(), 500)
	}
	var order any
	if orderID > 0 {
		order = orderID
	}
	detailID := ""
	if orderID > 0 {
		detailID = itoa(s.CascadeDetailID)
	}
	now := s.now()
	query := `
		INSERT INTO requisite_provider_logs (
			provider_name, provider_id, provider_terminal_id, merchant_id, order_id,
			request_type, request_params, response_data, success, error_message,
			response_time_ms, retry_attempt, detail_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 'create_order', ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	args := []any{
		fallbackName(terminal.ProviderName), terminal.ProviderID, terminal.ID, merchantID, order,
		params, response, success, message, quote.Millis, attemptOf(quote.Attempt), emptyAsNil(detailID), now, now,
	}
	if tx != nil {
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	}
	_, err := s.DB.ExecContext(ctx, query, args...)
	return err
}

func jsonValue(raw []byte) []byte {
	if json.Valid(raw) {
		return raw
	}
	encoded, _ := json.Marshal(string(raw))
	return encoded
}

func attemptOf(attempt int) int {
	if attempt < 1 {
		return 1
	}
	return attempt
}

func fallbackName(name string) string {
	if name == "" {
		return "cashout"
	}
	return name
}

func trimText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}

// ApplyProviderCallback mirrors a provider status onto the order once.
// A second event for an order that is no longer pending does not move money
// and does not enqueue another merchant callback.
func (s *Service) ApplyProviderCallback(ctx context.Context, body event.ProviderCallback) error {
	if body.OrderUUID == "" || (body.Action != "success" && body.Action != "fail") {
		return nil
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		row, err := lockOrder(ctx, tx, body.OrderUUID)
		if err != nil {
			var client *Error
			if errors.As(err, &client) && client.Status == 404 {
				return nil
			}
			return err
		}
		if row.Status != "pending" {
			return nil
		}
		var disputeID int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM disputes WHERE order_id = ? AND status = 'pending' LIMIT 1`, row.ID).Scan(&disputeID)
		if err == nil {
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		if body.Action == "success" {
			return s.markSuccess(ctx, tx, row, body.Sub)
		}
		return s.markFailed(ctx, tx, row, body.Sub)
	})
}

// QueueProviderCallback stores the parsed webhook for the worker.
func (s *Service) QueueProviderCallback(ctx context.Context, orderID int64, body event.ProviderCallback) error {
	if body.Action != "success" && body.Action != "fail" {
		return nil
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		id, err := newUUID()
		if err != nil {
			return err
		}
		return outbox.Insert(ctx, tx, id, event.TopicProviderCallback, strconv.FormatInt(orderID, 10), body, s.now())
	})
}
