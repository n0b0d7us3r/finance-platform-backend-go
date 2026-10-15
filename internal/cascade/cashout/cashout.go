// Package cashout talks to the provider wired to the DEMO_CARD integration.
package cashout

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"finance-platform/go/internal/cascade"
)

// Handles reports whether this adapter owns the integration name.
func Handles(integration string) bool {
	switch strings.ToUpper(strings.TrimSpace(integration)) {
	case "DEMO_CARD", "CASHOUT":
		return true
	default:
		return false
	}
}

type settings map[string]string

func readSettings(raw json.RawMessage) settings {
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return settings{}
	}
	out := settings{}
	for key, value := range decoded {
		text, ok := value.(string)
		if ok {
			out[key] = text
		}
	}
	return out
}

func paymentMethod(cfg settings, detailType string) string {
	if detailType != "" {
		if mapped := cfg["method_"+detailType]; mapped != "" {
			return mapped
		}
	}
	if legacy := cfg["payment_method"]; legacy != "" && legacy != "AUTO" {
		return legacy
	}
	switch detailType {
	case "phone":
		return "SBP"
	case "sim":
		return "MOBILE"
	default:
		return "CARD"
	}
}

// Open creates one provider transaction. The api key stays in the Authorization header.
func Open(ctx context.Context, client *http.Client, terminal cascade.Terminal, req cascade.Request) (cascade.Quote, error) {
	if !Handles(terminal.Integration) {
		return cascade.Quote{}, cascade.ErrSkip
	}
	cfg := readSettings(terminal.Settings)
	apiURL := strings.TrimRight(cfg["api_url"], "/")
	apiKey := cfg["api_key"]
	if apiURL == "" || apiKey == "" {
		return cascade.Quote{}, cascade.ErrMiss
	}
	method := paymentMethod(cfg, req.DetailType)
	payload, err := json.Marshal(map[string]any{
		"amount":                json.Number(trimAmount(req.Amount)),
		"currency":              strings.ToUpper(req.Currency),
		"paymentMethod":         method,
		"merchantTransactionId": req.OrderUUID,
	})
	if err != nil {
		return cascade.Quote{}, err
	}
	attempts := terminal.Retries
	if attempts < 1 {
		attempts = 1
	}
	timeout := time.Duration(terminal.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if client == nil {
		client = http.DefaultClient
	}
	var last cascade.Quote
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		quote, retry, err := once(ctx, client, apiURL, apiKey, payload, timeout, attempt)
		last = quote
		if err == nil {
			return quote, nil
		}
		if !retry || attempt == attempts {
			return last, cascade.ErrMiss
		}
	}
	return last, cascade.ErrMiss
}

func once(ctx context.Context, client *http.Client, apiURL, apiKey string, payload []byte, timeout time.Duration, attempt int) (cascade.Quote, bool, error) {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, apiURL+"/api/merchants/transactions/create-manual", bytes.NewReader(payload))
	if err != nil {
		return cascade.Quote{Attempt: attempt}, false, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	resp, err := client.Do(httpReq)
	elapsed := int(time.Since(started).Milliseconds())
	if err != nil {
		return cascade.Quote{Millis: elapsed, Attempt: attempt}, true, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	quote := cascade.Quote{Response: body, Millis: elapsed, Attempt: attempt}
	if resp.StatusCode >= 500 {
		return quote, true, errors.New("provider status")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return quote, false, cascade.ErrMiss
	}
	parsed, err := decodeQuote(body)
	if err != nil {
		return quote, false, err
	}
	quote.ProviderOrderID = parsed.ProviderOrderID
	quote.Detail = parsed.Detail
	quote.DetailType = parsed.DetailType
	quote.Initials = parsed.Initials
	return quote, false, nil
}

func decodeQuote(body []byte) (cascade.Quote, error) {
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Status         string         `json:"status"`
			TransactionID  string         `json:"transactionId"`
			PaymentDetails map[string]any `json:"paymentDetails"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || !payload.Success || payload.Data.Status == "" {
		return cascade.Quote{}, cascade.ErrMiss
	}
	if payload.Data.Status != "pending" && payload.Data.Status != "processing" {
		return cascade.Quote{}, cascade.ErrMiss
	}
	if len(payload.Data.PaymentDetails) == 0 {
		return cascade.Quote{}, cascade.ErrMiss
	}
	detail := firstString(payload.Data.PaymentDetails, "cardNumber", "phoneNumber", "phone")
	if detail == "" {
		return cascade.Quote{}, cascade.ErrMiss
	}
	initials := firstString(payload.Data.PaymentDetails, "cardHolder", "holder")
	detailType := "card"
	if _, ok := payload.Data.PaymentDetails["cardNumber"]; !ok {
		detailType = "phone"
	}
	return cascade.Quote{
		ProviderOrderID: payload.Data.TransactionID,
		Detail:          detail,
		DetailType:      detailType,
		Initials:        initials,
	}, nil
}

func firstString(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		text, ok := fields[key].(string)
		if ok && text != "" {
			return text
		}
	}
	return ""
}

func trimAmount(amount string) string {
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return "0"
	}
	return strings.TrimRight(strings.TrimRight(amount, "0"), ".")
}

// Parsed is a callback the worker can apply once.
type Parsed struct {
	OrderUUID       string
	ProviderOrderID string
	Action          string
	Sub             string
}

// Parse reads a CashOut webhook. An empty secret skips the HMAC check.
func Parse(body []byte, eventHeader, signature, secret string) (Parsed, error) {
	if strings.TrimSpace(secret) != "" && !signed(body, signature, secret) {
		return Parsed{}, errSignature
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return Parsed{}, errValidation
	}
	event := strings.TrimSpace(eventHeader)
	if event == "" {
		event, _ = payload["event"].(string)
	}
	switch event {
	case "transaction.status_changed":
		return parseStatus(payload)
	case "dispute.resolved":
		return parseDispute(payload)
	default:
		return Parsed{}, errUnknownEvent
	}
}

var (
	errSignature    = errors.New("invalid signature")
	errUnknownEvent = errors.New("unknown event")
	errValidation   = errors.New("validation failed")
)

// SignatureError is a bad HMAC.
func SignatureError(err error) bool { return errors.Is(err, errSignature) }

// UnknownEvent is an event this adapter does not accept.
func UnknownEvent(err error) bool { return errors.Is(err, errUnknownEvent) }

// ValidationError is a payload missing the fields the event requires.
func ValidationError(err error) bool { return errors.Is(err, errValidation) }

func parseStatus(payload map[string]any) (Parsed, error) {
	orderUUID, _ := payload["merchantTransactionId"].(string)
	providerID, _ := payload["transactionId"].(string)
	status, _ := payload["status"].(string)
	if orderUUID == "" || providerID == "" || status == "" {
		return Parsed{}, errValidation
	}
	action, sub := mapStatus(status)
	return Parsed{OrderUUID: orderUUID, ProviderOrderID: providerID, Action: action, Sub: sub}, nil
}

func parseDispute(payload map[string]any) (Parsed, error) {
	providerID, _ := payload["transactionId"].(string)
	result, _ := payload["result"].(string)
	if result == "" {
		result, _ = payload["status"].(string)
	}
	if providerID == "" || result == "" {
		return Parsed{}, errValidation
	}
	parsed := Parsed{ProviderOrderID: providerID}
	switch result {
	case "resolved_merchant":
		parsed.Action = "success"
		parsed.Sub = "successfully_paid"
	case "resolved_psp":
		parsed.Action = "fail"
		parsed.Sub = "cancelled"
	}
	return parsed, nil
}

func mapStatus(status string) (string, string) {
	switch status {
	case "completed", "completed_after_dispute":
		return "success", "successfully_paid"
	case "failed":
		return "fail", "expired"
	case "cancelled", "failed_after_dispute":
		return "fail", "cancelled"
	default:
		return "", ""
	}
}

func signed(body []byte, header, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(header))
}
