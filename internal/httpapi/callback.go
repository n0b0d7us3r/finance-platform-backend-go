package httpapi

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"finance-platform/go/internal/cascade/cashout"
	"finance-platform/go/internal/event"

	"github.com/go-chi/chi/v5"
)

func (h *handler) providerCallback(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Validation failed"})
		return
	}
	uuid := chi.URLParam(r, "uuid")
	terminal, provider, ok := h.callbackTarget(r, w, uuid, raw)
	if !ok {
		return
	}
	secret := settingString(terminal.Settings, "webhook_secret")
	parsed, err := cashout.Parse(raw, r.Header.Get("X-Webhook-Event"), r.Header.Get("X-Webhook-Signature"), secret)
	if err != nil {
		status, message := callbackError(err)
		h.writeCallbackLog(r, provider.Name, 0, 0, raw, status, message, started)
		writeJSON(w, status, map[string]string{"error": message})
		return
	}
	order, found := h.callbackOrder(r, provider.ID, terminal.FromProvider, parsed)
	if !found {
		h.writeCallbackLog(r, provider.Name, 0, 0, raw, http.StatusNotFound, "Order not found", started)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Order not found"})
		return
	}
	if parsed.Action == "success" || parsed.Action == "fail" {
		if err := h.flow().QueueProviderCallback(r.Context(), order.ID, event.ProviderCallback{
			OrderUUID: order.UUID,
			Action:    parsed.Action,
			Sub:       parsed.Sub,
		}); err != nil {
			h.writeCallbackLog(r, provider.Name, order.MerchantID, order.ID, raw, http.StatusInternalServerError, "queue failed", started)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
			return
		}
	}
	h.writeCallbackLog(r, provider.Name, order.MerchantID, order.ID, raw, http.StatusOK, "", started)
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

type callbackTerminal struct {
	ID           int64
	Settings     []byte
	FromProvider bool
}

type callbackProvider struct {
	ID          int64
	Name        string
	Integration string
}

func (h *handler) callbackTarget(r *http.Request, w http.ResponseWriter, uuid string, raw []byte) (callbackTerminal, callbackProvider, bool) {
	terminal, provider, err := h.terminalByUUID(r, uuid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return callbackTerminal{}, callbackProvider{}, false
	}
	if terminal.ID != 0 {
		if !cashout.Handles(provider.Integration) {
			h.writeCallbackLog(r, provider.Name, 0, 0, raw, http.StatusBadRequest, "Unknown integration", time.Now())
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Unknown integration type"})
			return callbackTerminal{}, callbackProvider{}, false
		}
		return terminal, provider, true
	}
	provider, err = h.providerByUUID(r, uuid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return callbackTerminal{}, callbackProvider{}, false
	}
	if provider.ID == 0 {
		h.writeCallbackLog(r, "provider_terminal:"+uuid, 0, 0, raw, http.StatusNotFound, "Terminal or Provider not found", time.Now())
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Terminal or Provider not found"})
		return callbackTerminal{}, callbackProvider{}, false
	}
	if !cashout.Handles(provider.Integration) {
		h.writeCallbackLog(r, provider.Name, 0, 0, raw, http.StatusBadRequest, "Unknown integration", time.Now())
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Unknown integration type"})
		return callbackTerminal{}, callbackProvider{}, false
	}
	parsed, err := cashout.Parse(raw, r.Header.Get("X-Webhook-Event"), "", "")
	if err != nil {
		status, message := callbackError(err)
		h.writeCallbackLog(r, provider.Name, 0, 0, raw, status, message, time.Now())
		writeJSON(w, status, map[string]string{"error": message})
		return callbackTerminal{}, callbackProvider{}, false
	}
	order, found := h.callbackOrder(r, provider.ID, true, parsed)
	if !found {
		h.writeCallbackLog(r, provider.Name, 0, 0, raw, http.StatusNotFound, "Terminal not resolved from payload", time.Now())
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Could not resolve terminal from callback data"})
		return callbackTerminal{}, callbackProvider{}, false
	}
	settings, err := h.terminalSettings(r, order.TerminalID)
	if err != nil || order.TerminalID == 0 {
		h.writeCallbackLog(r, provider.Name, order.MerchantID, order.ID, raw, http.StatusNotFound, "Terminal not resolved from payload", time.Now())
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Could not resolve terminal from callback data"})
		return callbackTerminal{}, callbackProvider{}, false
	}
	return callbackTerminal{ID: order.TerminalID, Settings: settings, FromProvider: true}, provider, true
}

type callbackOrder struct {
	ID         int64
	UUID       string
	MerchantID int64
	TerminalID int64
}

func (h *handler) callbackOrder(r *http.Request, providerID int64, unified bool, parsed cashout.Parsed) (callbackOrder, bool) {
	var row callbackOrder
	var terminal sql.NullInt64
	query := `SELECT id, uuid, merchant_id, provider_terminal_id FROM orders WHERE uuid = ?`
	args := []any{parsed.OrderUUID}
	if unified {
		query += ` AND provider_id = ?`
		args = append(args, providerID)
	}
	err := h.deps.DB.SQL.QueryRowContext(r.Context(), query, args...).Scan(&row.ID, &row.UUID, &row.MerchantID, &terminal)
	if err == sql.ErrNoRows && parsed.ProviderOrderID != "" {
		err = h.deps.DB.SQL.QueryRowContext(r.Context(), `
			SELECT id, uuid, merchant_id, provider_terminal_id
			FROM orders WHERE provider_order_id = ? AND provider_id = ?`, parsed.ProviderOrderID, providerID).
			Scan(&row.ID, &row.UUID, &row.MerchantID, &terminal)
	}
	if err != nil {
		return callbackOrder{}, false
	}
	if terminal.Valid {
		row.TerminalID = terminal.Int64
	}
	return row, true
}

func (h *handler) terminalByUUID(r *http.Request, uuid string) (callbackTerminal, callbackProvider, error) {
	var terminal callbackTerminal
	var provider callbackProvider
	var settings []byte
	err := h.deps.DB.SQL.QueryRowContext(r.Context(), `
		SELECT t.id, t.additional_settings, p.id, p.name, p.integration
		FROM provider_terminals t
		INNER JOIN providers p ON p.id = t.provider_id
		WHERE t.uuid = ?`, uuid).Scan(&terminal.ID, &settings, &provider.ID, &provider.Name, &provider.Integration)
	if err == sql.ErrNoRows {
		return callbackTerminal{}, callbackProvider{}, nil
	}
	if err != nil {
		return callbackTerminal{}, callbackProvider{}, err
	}
	terminal.Settings = settings
	return terminal, provider, nil
}

func (h *handler) providerByUUID(r *http.Request, uuid string) (callbackProvider, error) {
	var provider callbackProvider
	err := h.deps.DB.SQL.QueryRowContext(r.Context(), `
		SELECT id, name, integration FROM providers WHERE uuid = ?`, uuid).
		Scan(&provider.ID, &provider.Name, &provider.Integration)
	if err == sql.ErrNoRows {
		return callbackProvider{}, nil
	}
	return provider, err
}

func (h *handler) terminalSettings(r *http.Request, id int64) ([]byte, error) {
	var settings []byte
	err := h.deps.DB.SQL.QueryRowContext(r.Context(), `SELECT additional_settings FROM provider_terminals WHERE id = ?`, id).Scan(&settings)
	return settings, err
}

func (h *handler) writeCallbackLog(r *http.Request, provider string, merchantID, orderID int64, raw []byte, status int, message string, started time.Time) {
	if provider == "" {
		provider = "cashout"
	}
	requestID, err := randomID()
	if err != nil {
		return
	}
	response, _ := json.Marshal(map[string]any{"status": status})
	if message != "" {
		response, _ = json.Marshal(map[string]string{"error": message})
	} else {
		response, _ = json.Marshal(map[string]bool{"success": true})
	}
	success := 0
	if status >= 200 && status < 300 {
		success = 1
	}
	var merchant, order any
	if merchantID > 0 {
		merchant = merchantID
	}
	if orderID > 0 {
		order = orderID
	}
	var logged any
	if strings.TrimSpace(message) != "" {
		logged = trimRunes(message, 255)
	}
	now := h.now()
	_, _ = h.deps.DB.SQL.ExecContext(r.Context(), `
		INSERT INTO requisite_provider_callback_logs (
			request_id, provider_name, merchant_id, order_id, request_data, response_data,
			ip_address, user_agent, execution_time, status_code, is_successful, error_message,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		requestID, provider, merchant, order, jsonBody(raw), response,
		clientIP(r), trimRunes(r.UserAgent(), 255), time.Since(started).Seconds(), status, success, logged, now, now)
}

func callbackError(err error) (int, string) {
	switch {
	case cashout.SignatureError(err):
		return http.StatusUnauthorized, "Invalid signature"
	case cashout.UnknownEvent(err):
		return http.StatusBadRequest, "Unknown event"
	case cashout.ValidationError(err):
		return http.StatusUnprocessableEntity, "Validation failed"
	default:
		return http.StatusBadRequest, "Validation failed"
	}
}

func settingString(raw []byte, key string) string {
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return ""
	}
	text, _ := decoded[key].(string)
	return text
}

func jsonBody(raw []byte) []byte {
	if json.Valid(raw) {
		return raw
	}
	encoded, _ := json.Marshal(map[string]string{"raw": string(raw)})
	return encoded
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return trimRunes(r.RemoteAddr, 45)
	}
	return trimRunes(host, 45)
}

func requestIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		part := strings.TrimSpace(strings.Split(forwarded, ",")[0])
		if part != "" {
			return trimRunes(part, 45)
		}
	}
	if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
		return trimRunes(real, 45)
	}
	return clientIP(r)
}

func trimRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	count := 0
	for i := range value {
		if count == limit {
			return value[:i]
		}
		count++
	}
	return value
}

func randomID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
