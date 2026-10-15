package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"finance-platform/go/internal/flow"

	"github.com/go-chi/chi/v5"
)

func (h *handler) tokenAuth(want string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.Header.Get(accessTokenHeader)
			if want == "" || subtle.ConstantTimeCompare([]byte(token), []byte(want)) != 1 {
				writeFail(w, http.StatusBadRequest, invalidAccessToken)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (h *handler) depositWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email         string      `json:"email"`
		Amount        json.Number `json:"amount"`
		TransactionID string      `json:"transaction_id"`
		TxHash        string      `json:"tx_hash"`
	}
	if err := decode(r, &body); err != nil && !errors.Is(err, io.EOF) {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if utf8.RuneCountInString(body.TxHash) > 1000 {
		h.writeFlow(w, flowInvalid("tx_hash", "The tx hash field must not be greater than 1000 characters."))
		return
	}
	if h.writeFlow(w, h.flow().DepositWebhook(r.Context(), body.Email, body.Amount.String(), body.TransactionID, body.TxHash)) {
		return
	}
	writeSuccess(w, http.StatusOK, []any{})
}

func (h *handler) withdrawWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PaymentID json.Number `json:"payment_id"`
		Status    string      `json:"status"`
		TxHash    string      `json:"tx_hash"`
	}
	if err := decode(r, &body); err != nil && !errors.Is(err, io.EOF) {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	fields := map[string][]string{}
	paymentID, err := body.PaymentID.Int64()
	if body.PaymentID.String() == "" {
		fields["payment_id"] = []string{"The payment id field is required."}
	} else if err != nil {
		fields["payment_id"] = []string{"The payment id field must be an integer."}
	}
	if body.Status == "" {
		fields["status"] = []string{"The status field is required."}
	}
	if utf8.RuneCountInString(body.TxHash) > 1000 {
		fields["tx_hash"] = []string{"The tx hash field must not be greater than 1000 characters."}
	}
	if len(fields) > 0 {
		h.writeFlow(w, &flow.Error{Status: http.StatusUnprocessableEntity, Fields: fields, Message: "The given data was invalid."})
		return
	}
	view, err := h.flow().WithdrawWebhook(r.Context(), paymentID, body.Status, body.TxHash)
	if h.writeFlow(w, err) {
		return
	}
	writeSuccess(w, http.StatusOK, view)
}

func (h *handler) createPayout(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.user(w, r)
	if !ok {
		return
	}
	var body struct {
		PayoutGatewayID   string      `json:"payout_gateway_id"`
		ExternalID        string      `json:"external_id"`
		Detail            string      `json:"detail"`
		DetailType        string      `json:"detail_type"`
		DetailInitials    string      `json:"detail_initials"`
		Amount            json.Number `json:"amount"`
		PaymentGateway    string      `json:"payment_gateway"`
		SubPaymentGateway string      `json:"sub_payment_gateway"`
		CallbackURL       string      `json:"callback_url"`
	}
	if err := decode(r, &body); err != nil && !errors.Is(err, io.EOF) {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	fields := map[string][]string{}
	requireField(fields, "payout_gateway_id", body.PayoutGatewayID)
	requireField(fields, "external_id", body.ExternalID)
	requireField(fields, "detail", body.Detail)
	requireField(fields, "detail_type", body.DetailType)
	requireField(fields, "detail_initials", body.DetailInitials)
	requireField(fields, "amount", body.Amount.String())
	requireField(fields, "payment_gateway", body.PaymentGateway)
	if body.Amount.String() != "" {
		if _, err := body.Amount.Int64(); err != nil || strings.Contains(body.Amount.String(), ".") {
			fields["amount"] = []string{"The amount field must be an integer."}
		} else if value, _ := body.Amount.Int64(); value < 1 {
			fields["amount"] = []string{"The amount field must be at least 1."}
		}
	}
	if body.DetailInitials != "" && (utf8.RuneCountInString(body.DetailInitials) < 3 || utf8.RuneCountInString(body.DetailInitials) > 30) {
		fields["detail_initials"] = []string{"The detail initials field must be at least 3 characters."}
	}
	if body.Detail != "" && body.DetailType == "card" && !luhn(body.Detail) {
		fields["detail"] = []string{"The detail field is invalid."}
	}
	if body.Detail != "" && body.DetailType == "phone" && !strings.HasPrefix(body.Detail, "7") {
		fields["detail"] = []string{"The detail field is invalid."}
	}
	if body.Detail != "" && body.DetailType != "card" && body.DetailType != "phone" {
		n := utf8.RuneCountInString(body.Detail)
		if n < 3 || n > 30 {
			fields["detail"] = []string{"The detail field must be at least 3 characters."}
		}
	}
	if body.CallbackURL != "" {
		parsed, err := url.ParseRequestURI(body.CallbackURL)
		if err != nil || parsed.Scheme != "https" || utf8.RuneCountInString(body.CallbackURL) > 256 {
			fields["callback_url"] = []string{"The callback url field must be a valid URL."}
		}
	}
	if len(fields) > 0 {
		h.writeFlow(w, &flow.Error{Status: http.StatusUnprocessableEntity, Fields: fields, Message: "The given data was invalid."})
		return
	}
	view, err := h.flow().CreatePayout(r.Context(), flow.PayoutInput{
		OwnerID:           actor,
		PayoutGatewayUUID: body.PayoutGatewayID,
		ExternalID:        body.ExternalID,
		Detail:            body.Detail,
		DetailType:        body.DetailType,
		DetailInitials:    body.DetailInitials,
		Amount:            body.Amount.String(),
		PaymentGateway:    body.PaymentGateway,
		SubPaymentGateway: body.SubPaymentGateway,
		CallbackURL:       body.CallbackURL,
	})
	if h.writeFlow(w, err) {
		return
	}
	writeSuccess(w, http.StatusOK, view)
}

func (h *handler) showPayout(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.user(w, r)
	if !ok {
		return
	}
	view, err := h.flow().FindPayout(r.Context(), actor, chi.URLParam(r, "uuid"), "", "")
	if h.writeFlow(w, err) {
		return
	}
	writeSuccess(w, http.StatusOK, view)
}

func (h *handler) traderFinishPayout(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Trader")
	if !ok {
		return
	}
	view, err := h.flow().FinishPayoutByTrader(r.Context(), actor, chi.URLParam(r, "uuid"))
	if h.writeFlow(w, err) {
		return
	}
	writeSuccess(w, http.StatusOK, view)
}

func (h *handler) adminFinishPayout(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if h.writeFlow(w, h.flow().FinishPayoutByAdmin(r.Context(), actor, id)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Выплата завершена"})
}

func (h *handler) adminCancelPayout(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := decode(r, &body); err != nil && !errors.Is(err, io.EOF) {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.writeFlow(w, h.flow().CancelPayout(r.Context(), actor, id, body.Reason)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Выплата отклонена, деньги вернутся мерчанту"})
}

func (h *handler) adminPassPayout(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if h.writeFlow(w, h.flow().PassPayout(r.Context(), actor, id)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Выплата передана свободному трейдеру"})
}

func flowInvalid(field, message string) *flow.Error {
	return &flow.Error{
		Status:  http.StatusUnprocessableEntity,
		Fields:  map[string][]string{field: {message}},
		Message: message,
	}
}

func requireField(fields map[string][]string, key, value string) {
	if strings.TrimSpace(value) == "" {
		fields[key] = []string{"The " + strings.ReplaceAll(key, "_", " ") + " field is required."}
	}
}

func luhn(number string) bool {
	if len(number) < 12 || len(number) > 19 {
		return false
	}
	sum := 0
	alt := false
	for i := len(number) - 1; i >= 0; i-- {
		digit := int(number[i] - '0')
		if number[i] < '0' || number[i] > '9' {
			return false
		}
		if alt {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
		alt = !alt
	}
	return sum%10 == 0
}
