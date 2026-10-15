package flow

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"

	"finance-platform/go/internal/event"
	"finance-platform/go/internal/money"
	"finance-platform/go/internal/outbox"
	"finance-platform/go/internal/sms"
)

// HandleSMS stores the message and leaves matching to the worker.
// A sender on the stop list is acknowledged and not stored.
func (s *Service) HandleSMS(ctx context.Context, deviceToken, sender, message, kind, idemKey string) error {
	var deviceID, userID int64
	var android sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id, user_id, android_id FROM user_devices WHERE token = ?`, deviceToken).
		Scan(&deviceID, &userID, &android)
	if err == sql.ErrNoRows {
		return plain(400, "Invalid Access Token.")
	}
	if err != nil {
		return err
	}
	if !android.Valid || android.String == "" {
		return fail(401, "Устройство не подключено")
	}
	sender = sms.Normalize(sender)
	message = sms.Normalize(message)
	if kind == "" {
		kind = "sms"
	}
	var stopped int
	err = s.DB.QueryRowContext(ctx, `SELECT 1 FROM sender_stop_lists WHERE sender = ? LIMIT 1`, sender).Scan(&stopped)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		if idemKey != "" {
			res, err := tx.ExecContext(ctx, `
				INSERT IGNORE INTO app_request_keys (scope_key, idem_key, created_at)
				VALUES (?, ?, ?)`, "device-"+strconv.FormatInt(deviceID, 10), idemKey, now)
			if err != nil {
				return err
			}
			inserted, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if inserted == 0 {
				return nil
			}
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO sms_logs (sender, message, timestamp, type, user_id, user_device_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			sender, message, strconv.FormatInt(now.Unix(), 10), kind, userID, deviceID, now, now)
		if err != nil {
			return err
		}
		logID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		id, err := newUUID()
		if err != nil {
			return err
		}
		return outbox.Insert(ctx, tx, id, event.TopicSMSInbound, strconv.FormatInt(logID, 10), event.SMS{LogID: logID}, now)
	})
}

// ApplyInboundSMS parses one stored message and closes a matching pending order once.
func (s *Service) ApplyInboundSMS(ctx context.Context, logID int64) error {
	stopWords, err := s.wordList(ctx, `SELECT word FROM sms_stop_words`)
	if err != nil {
		return err
	}
	blockWords, err := s.wordList(ctx, `SELECT word FROM sms_block_words`)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		var sender, message string
		var userID, deviceID int64
		var linked sql.NullInt64
		err := tx.QueryRowContext(ctx, `
			SELECT sender, message, user_id, user_device_id, order_id
			FROM sms_logs WHERE id = ? FOR UPDATE`, logID).
			Scan(&sender, &message, &userID, &deviceID, &linked)
		if err == sql.ErrNoRows || linked.Valid {
			return nil
		}
		if err != nil {
			return err
		}
		now := s.now()
		parsed, ok := sms.Parse(message, stopWords)
		parsing := parsingResult(parsed, ok)
		if sms.Blocked(message, blockWords) {
			if _, err := tx.ExecContext(ctx, `
				UPDATE payment_details SET is_active = 0, updated_at = ?
				WHERE user_device_id = ? AND is_active = 1`, now, deviceID); err != nil {
				return err
			}
			return touchSMS(ctx, tx, logID, parsing, 0, now)
		}
		if !ok {
			return touchSMS(ctx, tx, logID, nil, 0, now)
		}
		currency := parsed.Currency
		gatewayID, gatewayCurrency, restricted, err := gatewayBySender(ctx, tx, sender)
		if err != nil {
			return err
		}
		if currency == "" {
			currency = gatewayCurrency
		}
		if currency == "" {
			currency = "rub"
		}
		amount, err := money.FromPrecision(parsed.Amount, currency)
		if err != nil {
			return touchSMS(ctx, tx, logID, parsing, 0, now)
		}
		orderUUID, err := findSMSOrder(ctx, tx, amount.Units(), currency, userID, deviceID, gatewayID, restricted)
		if err != nil || orderUUID == "" {
			if err != nil {
				return err
			}
			return touchSMS(ctx, tx, logID, parsing, 0, now)
		}
		row, err := lockOrder(ctx, tx, orderUUID)
		if err != nil {
			return err
		}
		if err := guard(ctx, tx, row, "It is not possible to finish a completed order.", "Unable to finish an order in dispute."); err != nil {
			return touchSMS(ctx, tx, logID, parsing, 0, now)
		}
		if err := s.markSuccess(ctx, tx, row, "successfully_paid"); err != nil {
			return err
		}
		return touchSMS(ctx, tx, logID, parsing, row.ID, now)
	})
}

func (s *Service) wordList(ctx context.Context, query string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var words []string
	for rows.Next() {
		var word string
		if err := rows.Scan(&word); err != nil {
			return nil, err
		}
		words = append(words, word)
	}
	return words, rows.Err()
}

func gatewayBySender(ctx context.Context, tx *sql.Tx, sender string) (int64, string, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, currency, sms_senders FROM payment_gateways`)
	if err != nil {
		return 0, "", false, err
	}
	defer rows.Close()
	var id int64
	var currency string
	found := false
	for rows.Next() {
		var rowID int64
		var rowCurrency string
		var raw sql.NullString
		if err := rows.Scan(&rowID, &rowCurrency, &raw); err != nil {
			return 0, "", false, err
		}
		if !raw.Valid || raw.String == "" {
			continue
		}
		var senders []string
		if err := json.Unmarshal([]byte(raw.String), &senders); err != nil {
			continue
		}
		for _, item := range senders {
			if sms.Normalize(item) == sender {
				id = rowID
				currency = rowCurrency
				found = true
			}
		}
	}
	return id, currency, found, rows.Err()
}

func findSMSOrder(ctx context.Context, tx *sql.Tx, units, currency string, userID, deviceID, gatewayID int64, restricted bool) (string, error) {
	query := `
		SELECT o.uuid
		FROM orders o
		INNER JOIN payment_details pd ON pd.id = o.payment_detail_id
		WHERE o.status = 'pending' AND o.amount = ? AND o.currency = ?
		  AND o.trader_id = ? AND pd.user_device_id = ?
		  AND NOT EXISTS (SELECT 1 FROM disputes d WHERE d.order_id = o.id)`
	args := []any{units, currency, userID, deviceID}
	if restricted {
		query += ` AND o.payment_gateway_id = ?`
		args = append(args, gatewayID)
	}
	query += ` ORDER BY o.id LIMIT 1`
	var orderUUID string
	err := tx.QueryRowContext(ctx, query, args...).Scan(&orderUUID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return orderUUID, err
}

func parsingResult(parsed sms.Result, ok bool) any {
	if !ok {
		return nil
	}
	var card *string
	if parsed.Card != "" {
		card = &parsed.Card
	}
	raw, err := json.Marshal(struct {
		Amount string  `json:"amount"`
		Card   *string `json:"card"`
	}{Amount: parsed.Amount, Card: card})
	if err != nil {
		return nil
	}
	return raw
}

func touchSMS(ctx context.Context, tx *sql.Tx, logID int64, parsing any, orderID int64, now any) error {
	var order any
	if orderID > 0 {
		order = orderID
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE sms_logs SET parsing_result = ?, order_id = COALESCE(?, order_id), updated_at = ? WHERE id = ?`,
		parsing, order, now, logID)
	return err
}
