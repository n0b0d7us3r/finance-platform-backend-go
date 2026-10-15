package worker

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"finance-platform/go/internal/event"
	"finance-platform/go/internal/flow"

	"github.com/segmentio/kafka-go"
)

// callbackBackoff matches SendOrderCallbackJob: eight attempts.
var callbackBackoff = []time.Duration{
	10 * time.Second,
	60 * time.Second,
	2 * time.Minute,
	4 * time.Minute,
	8 * time.Minute,
	30 * time.Minute,
	time.Hour,
	2 * time.Hour,
}

// Worker consumes order, SMS and merchant-log topics.
type Worker struct {
	DB       *sql.DB
	Orders   *flow.Service
	Log      *slog.Logger
	Brokers  []string
	Insecure bool
	client   *http.Client
}

// Run reads the three topics until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	w.client = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: w.Insecure},
		},
	}
	topics := event.Topics()
	errc := make(chan error, len(topics)+1)
	for _, topic := range topics {
		topic := topic
		go func() { errc <- w.consume(ctx, topic) }()
	}
	go func() { errc <- w.retryLoop(ctx) }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-errc:
		if err == nil || ctx.Err() != nil {
			return nil
		}
		return err
	}
}

func (w *Worker) consume(ctx context.Context, topic string) error {
	w.Log.Info("consume", "topic", topic)
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        w.Brokers,
		GroupID:        "finance-worker-" + strings.ReplaceAll(topic, ".", "-"),
		Topic:          topic,
		MinBytes:       1,
		MaxBytes:       1e6,
		MaxWait:        500 * time.Millisecond,
		StartOffset:    kafka.FirstOffset,
		CommitInterval: 0,
	})
	defer reader.Close()
	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.Log.Error("fetch", "topic", topic, "err", err.Error())
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
			continue
		}
		for {
			err = w.handle(ctx, message.Value)
			if err == nil || ctx.Err() != nil {
				break
			}
			w.Log.Error("event", "topic", topic, "err", err.Error())
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
		}
		if err := reader.CommitMessages(ctx, message); err != nil && ctx.Err() == nil {
			return err
		}
	}
}

func (w *Worker) handle(ctx context.Context, payload []byte) error {
	var message event.Message
	if err := json.Unmarshal(payload, &message); err != nil {
		w.Log.Error("event decode", "err", err.Error())
		return nil
	}
	switch message.Topic {
	case event.TopicSMSInbound:
		var body event.SMS
		if err := json.Unmarshal(message.Body, &body); err != nil {
			return nil
		}
		return w.Orders.ApplyInboundSMS(ctx, body.LogID)
	case event.TopicOrderStatus, event.TopicPayoutStatus:
		return w.acceptCallback(ctx, message)
	case event.TopicProviderCallback:
		var body event.ProviderCallback
		if err := json.Unmarshal(message.Body, &body); err != nil {
			return nil
		}
		return w.Orders.ApplyProviderCallback(ctx, body)
	case event.TopicMerchantLog:
		var body event.MerchantLog
		if err := json.Unmarshal(message.Body, &body); err != nil {
			return nil
		}
		return w.insertMerchantLog(ctx, body)
	default:
		return nil
	}
}

func (w *Worker) acceptCallback(ctx context.Context, message event.Message) error {
	var body event.OrderStatus
	if err := json.Unmarshal(message.Body, &body); err != nil {
		return nil
	}
	kind := "order"
	model := `App\Models\Order`
	if message.Topic == event.TopicPayoutStatus {
		kind = "payout"
		model = `App\Models\Payout`
	}
	meta, err := json.Marshal(map[string]any{
		"url":      body.CallbackURL,
		"token":    body.AccessToken,
		"order_id": body.OrderID,
		"kind":     kind,
		"model":    model,
	})
	if err != nil {
		return err
	}
	now := time.Now()
	res, err := w.DB.ExecContext(ctx, `
		INSERT IGNORE INTO event_deliveries
			(event_id, topic, body, meta, status, attempts, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'pending', 0, ?, ?)`,
		message.EventID, message.Topic, body.Resource, meta, now, now)
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
	w.sendCallback(ctx, message.EventID, body.CallbackURL, body.AccessToken, body.OrderID, body.Resource, 0, kind, model)
	return nil
}

func (w *Worker) sendCallback(ctx context.Context, eventID, url, token string, orderID int64, resource []byte, attempts int, kind, model string) {
	status, response, ok := w.post(ctx, url, token, resource)
	w.writeCallbackLog(ctx, orderID, url, resource, status, response, ok, kind, model)
	now := time.Now()
	if ok {
		_, _ = w.DB.ExecContext(ctx, `
			UPDATE event_deliveries
			SET status = 'sent', attempts = ?, next_attempt_at = NULL, updated_at = ?
			WHERE event_id = ?`, attempts+1, now, eventID)
		return
	}
	nextAttempts := attempts + 1
	state := "failed"
	var next any
	if nextAttempts >= len(callbackBackoff) {
		state = "gave_up"
	} else {
		next = now.Add(callbackBackoff[nextAttempts-1])
	}
	_, _ = w.DB.ExecContext(ctx, `
		UPDATE event_deliveries
		SET status = ?, attempts = ?, next_attempt_at = ?, updated_at = ?
		WHERE event_id = ?`, state, nextAttempts, next, now, eventID)
	w.Log.Info("callback", "order_id", orderID, "status", status, "attempt", nextAttempts)
}

func (w *Worker) post(ctx context.Context, url, token string, resource []byte) (int, []byte, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(resource))
	if err != nil {
		encoded, _ := json.Marshal(err.Error())
		return 0, encoded, false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Access-Token", token)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		encoded, _ := json.Marshal(err.Error())
		return 0, encoded, false
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	return resp.StatusCode, jsonValue(raw), ok
}

func (w *Worker) writeCallbackLog(ctx context.Context, orderID int64, url string, request []byte, status int, response []byte, ok bool, kind, model string) {
	now := time.Now()
	success := 0
	if ok {
		success = 1
	}
	if kind == "" {
		kind = "order"
	}
	if model == "" {
		model = `App\Models\Order`
	}
	_, err := w.DB.ExecContext(ctx, `
		INSERT INTO callback_logs
			(type, callbackable_type, callbackable_id, url, request_data, response_data, status_code, is_success, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		kind, model, orderID, url, request, response, status, success, now, now)
	if err != nil {
		w.Log.Error("callback log", "err", err.Error(), "order_id", orderID)
	}
}

func (w *Worker) retryLoop(ctx context.Context) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := w.retryDue(ctx); err != nil && ctx.Err() == nil {
				w.Log.Error("callback retry", "err", err.Error())
			}
		}
	}
}

func (w *Worker) retryDue(ctx context.Context) error {
	now := time.Now()
	rows, err := w.DB.QueryContext(ctx, `
		SELECT event_id, body, meta, attempts
		FROM event_deliveries
		WHERE topic IN (?, ?) AND attempts < ?
		  AND (
		    (status = 'failed' AND next_attempt_at IS NOT NULL AND next_attempt_at <= ?)
		    OR (status = 'pending' AND updated_at <= ?)
		  )
		ORDER BY updated_at
		LIMIT 20`, event.TopicOrderStatus, event.TopicPayoutStatus, len(callbackBackoff), now, now.Add(-20*time.Second))
	if err != nil {
		return err
	}
	defer rows.Close()
	type due struct {
		id       string
		body     []byte
		meta     []byte
		attempts int
	}
	var batch []due
	for rows.Next() {
		var item due
		if err := rows.Scan(&item.id, &item.body, &item.meta, &item.attempts); err != nil {
			return err
		}
		batch = append(batch, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range batch {
		var meta struct {
			URL     string `json:"url"`
			Token   string `json:"token"`
			OrderID int64  `json:"order_id"`
			Kind    string `json:"kind"`
			Model   string `json:"model"`
		}
		if err := json.Unmarshal(item.meta, &meta); err != nil {
			continue
		}
		w.sendCallback(ctx, item.id, meta.URL, meta.Token, meta.OrderID, item.body, item.attempts, meta.Kind, meta.Model)
	}
	return nil
}

func (w *Worker) insertMerchantLog(ctx context.Context, body event.MerchantLog) error {
	var existing int64
	err := w.DB.QueryRowContext(ctx, `SELECT id FROM merchant_api_request_logs WHERE request_id = ? LIMIT 1`, body.RequestID).Scan(&existing)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	now := time.Now()
	success := 0
	if body.Successful {
		success = 1
	}
	var orderID any
	if body.OrderID != nil {
		orderID = *body.OrderID
	}
	_, err = w.DB.ExecContext(ctx, `
		INSERT INTO merchant_api_request_logs (
			request_id, external_id, amount, currency, payment_gateway, payment_detail_type,
			request_data, response_data, ip_address, user_agent, execution_time, is_successful,
			error_message, merchant_id, order_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cut(body.RequestID, 36), cut(body.ExternalID, 255), cut(body.Amount, 255), cut(body.Currency, 10),
		cut(body.Gateway, 30), cut(body.DetailType, 20), body.RequestData, body.ResponseData,
		cut(body.IP, 45), cut(body.UserAgent, 255), body.ExecutionMS, success, cut(body.ErrorMessage, 255),
		body.MerchantID, orderID, now, now)
	return err
}

func jsonValue(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte("null")
	}
	if json.Valid(raw) {
		return raw
	}
	encoded, err := json.Marshal(string(raw))
	if err != nil {
		return []byte("null")
	}
	return encoded
}

func cut(value string, max int) string {
	if len(value) <= max {
		return value
	}
	end := max
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}
