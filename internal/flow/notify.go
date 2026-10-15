package flow

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"finance-platform/go/internal/event"
	"finance-platform/go/internal/outbox"
	"finance-platform/go/internal/read"
	"finance-platform/go/internal/store"
)

func (s *Service) enqueueOrder(ctx context.Context, tx *sql.Tx, orderID int64) error {
	var orderURL, merchantURL, token sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT o.callback_url, m.callback_url, u.api_access_token
		FROM orders o
		INNER JOIN merchants m ON m.id = o.merchant_id
		INNER JOIN users u ON u.id = m.user_id
		WHERE o.id = ?`, orderID).Scan(&orderURL, &merchantURL, &token)
	if err != nil {
		return err
	}
	url := strings.TrimSpace(orderURL.String)
	if url == "" {
		url = strings.TrimSpace(merchantURL.String)
	}
	if url == "" {
		return nil
	}
	order, err := store.FindOrderByIDTx(ctx, tx, orderID)
	if err != nil {
		return err
	}
	var external *read.External
	if s.Requisites != nil {
		external, err = s.Requisites.Load(ctx, orderID)
		if err != nil {
			return err
		}
	}
	view, err := read.PresentOrder(order, external, s.now())
	if err != nil {
		return err
	}
	resource, err := marshalJSON(view)
	if err != nil {
		return err
	}
	id, err := newUUID()
	if err != nil {
		return err
	}
	body := event.OrderStatus{
		OrderID:     orderID,
		OrderUUID:   order.UUID,
		Status:      view.Status,
		Amount:      view.Amount,
		CallbackURL: url,
		AccessToken: token.String,
		Resource:    resource,
	}
	return outbox.Insert(ctx, tx, id, event.TopicOrderStatus, strconv.FormatInt(orderID, 10), body, s.now())
}

// MerchantLogInput is the POST /api/v1/order exchange recorded after the response is known.
type MerchantLogInput struct {
	MerchantUUID string
	ExternalID   string
	Amount       string
	Currency     string
	Gateway      string
	DetailType   string
	OrderUUID    string
	IP           string
	UserAgent    string
	Started      time.Time
	Err          error
	Request      any
}

// EnqueueMerchantLog writes one merchant.api_log event. A missing merchant is ignored.
func (s *Service) EnqueueMerchantLog(ctx context.Context, in MerchantLogInput) error {
	var merchantID int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM merchants WHERE uuid = ?`, in.MerchantUUID).Scan(&merchantID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var orderID *int64
	if in.OrderUUID != "" {
		var id int64
		if err := s.DB.QueryRowContext(ctx, `SELECT id FROM orders WHERE uuid = ?`, in.OrderUUID).Scan(&id); err == nil {
			orderID = &id
		}
	}
	request, err := marshalJSON(in.Request)
	if err != nil {
		return err
	}
	message := ""
	if in.Err != nil {
		message = in.Err.Error()
	}
	response := map[string]any{"success": in.Err == nil}
	if in.OrderUUID != "" {
		response["order_id"] = in.OrderUUID
	}
	if message != "" {
		response["message"] = message
	}
	encoded, err := marshalJSON(response)
	if err != nil {
		return err
	}
	id, err := newUUID()
	if err != nil {
		return err
	}
	elapsed := time.Since(in.Started).Seconds() * 1000
	body := event.MerchantLog{
		RequestID:    id,
		MerchantID:   merchantID,
		ExternalID:   in.ExternalID,
		Amount:       in.Amount,
		Currency:     in.Currency,
		Gateway:      in.Gateway,
		DetailType:   in.DetailType,
		RequestData:  request,
		ResponseData: encoded,
		IP:           in.IP,
		UserAgent:    in.UserAgent,
		ExecutionMS:  elapsed,
		Successful:   in.Err == nil,
		ErrorMessage: message,
		OrderID:      orderID,
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		return outbox.Insert(ctx, tx, id, event.TopicMerchantLog, id, body, s.now())
	})
}

func marshalJSON(value any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
