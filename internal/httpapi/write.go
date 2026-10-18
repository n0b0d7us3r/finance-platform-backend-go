package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"finance-platform/go/internal/flow"
	"finance-platform/go/internal/impersonate"
	"finance-platform/go/internal/read"
	"finance-platform/go/internal/requisite"
	"finance-platform/go/internal/store"

	"github.com/go-chi/chi/v5"
)

func (h *handler) flow() *flow.Service {
	var requisites flow.Requisites
	if h.deps.Prices.Cache != nil {
		requisites = requisite.Store{Cache: h.deps.Prices.Cache, Prefix: h.deps.Prices.Prefix}
	}
	var cascadeID int64
	if id := strings.TrimSpace(h.deps.CascadePaymentDetailID); id != "" {
		cascadeID, _ = strconv.ParseInt(id, 10, 64)
	}
	return &flow.Service{
		DB:              h.deps.DB.SQL,
		Quotes:          h.deps.Quotes,
		Now:             h.now,
		Location:        h.deps.Location,
		MaxWait:         h.deps.MaxWait,
		Poll:            h.deps.Poll,
		Gate:            h.deps.Gate,
		Holds:           h.deps.Holds,
		LocalWithdraw:   h.deps.LocalWithdraw,
		Cascade:         h.deps.FeatureCascade,
		CascadeDetailID: cascadeID,
		Requisites:      requisites,
	}
}

func (h *handler) writeFlow(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var client *flow.Error
	if errors.As(err, &client) {
		if client.Fields != nil {
			writeJSON(w, client.Status, map[string]any{"message": client.Message, "errors": client.Fields})
			return true
		}
		if client.Plain {
			writeMessage(w, client.Status, client.Message)
			return true
		}
		writeFail(w, client.Status, client.Message)
		return true
	}
	writeFail(w, http.StatusInternalServerError, err.Error())
	return true
}

func (h *handler) actor(w http.ResponseWriter, r *http.Request, role string) (int64, bool) {
	id, ok := h.user(w, r)
	if !ok {
		return 0, false
	}
	if h.deps.DB.SQL == nil {
		writeFail(w, http.StatusInternalServerError, "database unavailable")
		return 0, false
	}
	has, err := store.HasRole(r.Context(), h.deps.DB.SQL, id, role)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return 0, false
	}
	if !has {
		writeMessage(w, http.StatusForbidden, unauthorizedMessage)
		return 0, false
	}
	return id, true
}

func decode(r *http.Request, dest any) error {
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dest); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

type loginBody struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *handler) login(w http.ResponseWriter, r *http.Request, role, label string) {
	var body loginBody
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, "Неверный логин или пароль")
		return
	}
	session, err := h.flow().Login(r.Context(), body.Email, body.Password, role)
	if h.writeFlow(w, err) {
		return
	}
	_ = h.flow().RecordLogin(r.Context(), session.UserID, requestIP(r), r.UserAgent())
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"token":   session.Token,
		"user": map[string]any{
			"id":    session.UserID,
			"name":  session.Name,
			"email": session.Email,
			"role":  label,
		},
	})
}

func (h *handler) loginAdmin(w http.ResponseWriter, r *http.Request) {
	h.login(w, r, "Admin", "admin")
}
func (h *handler) loginTrader(w http.ResponseWriter, r *http.Request) {
	h.login(w, r, "Trader", "trader")
}
func (h *handler) loginMerchant(w http.ResponseWriter, r *http.Request) {
	h.login(w, r, "Merchant", "merchant")
}
func (h *handler) loginSupport(w http.ResponseWriter, r *http.Request) {
	h.login(w, r, "Support", "support")
}
func (h *handler) loginLeader(w http.ResponseWriter, r *http.Request) {
	h.login(w, r, "Team Leader", "leader")
}

func (h *handler) createUser(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	var raw map[string]json.RawMessage
	if err := decode(r, &raw); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	edit, fields := userEdit(raw)
	if !edit.RoleSet {
		if roleName, present := optionalString(raw["role"]); present && strings.TrimSpace(roleName) != "" {
			var roleID int64
			err := h.deps.DB.SQL.QueryRowContext(r.Context(), `SELECT id FROM roles WHERE name = ?`, strings.TrimSpace(roleName)).Scan(&roleID)
			if errors.Is(err, sql.ErrNoRows) {
				writeFail(w, http.StatusUnprocessableEntity, "Роль не найдена.")
				return
			}
			if err != nil {
				writeFail(w, http.StatusInternalServerError, err.Error())
				return
			}
			edit.RoleID, edit.RoleSet = roleID, true
		}
	}
	if strings.TrimSpace(edit.Name) == "" {
		fields["name"] = []string{"The name field is required."}
	}
	if strings.TrimSpace(edit.Email) == "" {
		fields["email"] = []string{"The email field is required."}
	}
	if _, mismatch := fields["password"]; !mismatch && (!edit.PasswordSet || strings.TrimSpace(edit.Password) == "") {
		fields["password"] = []string{"The password field is required."}
	}
	if !edit.RoleSet {
		if _, present := raw["role"]; present {
			fields["role"] = []string{"The role field is required."}
		} else {
			fields["role_id"] = []string{"The role id field is required."}
		}
	}
	if len(fields) > 0 {
		h.writeFlow(w, flow.Invalid(fields))
		return
	}
	created, err := h.flow().CreateCabinetUser(r.Context(), actor, edit)
	if h.writeFlow(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Пользователь создан",
		"data":    map[string]any{"id": created.ID, "email": created.Email},
	})
}

func (h *handler) patchUser(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var raw map[string]json.RawMessage
	if err := decode(r, &raw); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	edit, fields := userEdit(raw)
	if len(fields) > 0 {
		h.writeFlow(w, &flow.Error{Status: http.StatusUnprocessableEntity, Message: "The given data was invalid.", Fields: fields})
		return
	}
	if h.writeFlow(w, h.flow().UpdateUser(r.Context(), actor, id, edit)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Пользователь обновлён"})
}

func userEdit(raw map[string]json.RawMessage) (flow.UserEdit, map[string][]string) {
	var edit flow.UserEdit
	fields := map[string][]string{}
	if text, ok := optionalString(raw["name"]); ok {
		edit.Name, edit.NameSet = strings.TrimSpace(text), true
	}
	if text, ok := optionalString(raw["email"]); ok {
		edit.Email, edit.EmailSet = strings.TrimSpace(text), true
	}
	if text, ok := optionalString(raw["password"]); ok && strings.TrimSpace(text) != "" {
		edit.Password, edit.PasswordSet = text, true
		if confirm, present := optionalString(raw["password_confirmation"]); present && confirm != text {
			fields["password"] = []string{"The password confirmation does not match."}
		}
	}
	if value, ok := flexInt(raw["role_id"]); ok && value > 0 {
		edit.RoleID, edit.RoleSet = value, true
	}
	if text, ok := optionalString(raw["promo_code"]); ok {
		edit.Promo, edit.PromoSet = strings.TrimSpace(text), true
	}
	if _, present := raw["trader_category_id"]; present && string(raw["trader_category_id"]) != "null" {
		edit.CategorySet = true
		if value, ok := flexInt(raw["trader_category_id"]); ok && value > 0 {
			edit.CategoryID = value
		} else {
			edit.CategoryClear = true
		}
	}
	if flag, ok := flexBool(raw["banned"]); ok {
		edit.Banned = &flag
	}
	if flag, ok := flexBool(raw["payouts_enabled"]); ok {
		edit.Payouts = &flag
	}
	if flag, ok := flexBool(raw["is_online"]); ok {
		edit.Online = &flag
	}
	if flag, ok := flexBool(raw["stop_traffic"]); ok {
		edit.Stop = &flag
	}
	if text, ok := jsonScalar(raw["referral_commission_percentage"]); ok {
		edit.Referral, edit.ReferralSet = text, true
	}
	if text, ok := jsonScalar(raw["trader_commission_rate"]); ok {
		edit.TraderRateSet = true
		if strings.TrimSpace(text) == "" {
			edit.TraderRateClear = true
		} else {
			edit.TraderRate = text
		}
	}
	if text, ok := jsonScalar(raw["target_reserve_amount"]); ok && strings.TrimSpace(text) != "" {
		value, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil {
			fields["target_reserve_amount"] = []string{"The target reserve amount field must be an integer."}
		} else {
			edit.Reserve, edit.ReserveSet = value, true
		}
	}
	if ids, ok := raw["category_ids"]; ok && string(ids) != "null" {
		edit.MerchantCategoriesSet = true
		var list []int64
		if json.Unmarshal(ids, &list) == nil {
			edit.MerchantCategories = list
		} else {
			fields["category_ids"] = []string{"The category ids field must be a list."}
		}
	}
	return edit, fields
}

func optionalString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return "", false
	}
	return text, true
}

func jsonScalar(raw json.RawMessage) (string, bool) {
	if text, ok := optionalString(raw); ok {
		return text, true
	}
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var number json.Number
	if json.Unmarshal(raw, &number) != nil {
		return "", false
	}
	return number.String(), true
}

type depositBody struct {
	Amount      json.Number `json:"amount"`
	BalanceType string      `json:"balance_type"`
	TxHash      string      `json:"tx_hash"`
}

func (h *handler) deposit(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body depositBody
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.writeFlow(w, h.flow().Deposit(r.Context(), actor, id, body.Amount.String(), body.BalanceType, body.TxHash)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Средства зачислены"})
}

func (h *handler) viewedDeposit(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.user(w, r); !ok {
		return
	}
	active, viewing := impersonate.FromContext(r.Context())
	if !viewing {
		writeMessage(w, http.StatusForbidden, "Пополнение доступно администратору в режиме просмотра.")
		return
	}
	var body depositBody
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.writeFlow(w, h.flow().Deposit(r.Context(), active.ActorID, active.TargetID, body.Amount.String(), body.BalanceType, body.TxHash)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Средства зачислены"})
}

func (h *handler) validateMerchant(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if h.writeFlow(w, h.flow().ValidateMerchant(r.Context(), actor, id)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Проект валидирован"})
}

type priceBody struct {
	Currency string      `json:"currency"`
	Market   string      `json:"market"`
	Buy      json.Number `json:"buy_price"`
	Sell     json.Number `json:"sell_price"`
}

func (h *handler) marketPrice(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	var body priceBody
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.writeFlow(w, h.flow().SetMarketPrice(r.Context(), actor, body.Currency, body.Market, body.Buy.String(), body.Sell.String())) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Курс записан"})
}

type deviceBody struct {
	Name string `json:"name"`
}

func (h *handler) createDevice(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Trader")
	if !ok {
		return
	}
	var body deviceBody
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	device, err := h.flow().CreateDevice(r.Context(), actor, body.Name)
	if h.writeFlow(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Токен для устройства создан",
		"data":    map[string]any{"id": device.ID, "token": device.Token, "name": device.Name},
	})
}

type connectBody struct {
	AndroidID    string `json:"android_id"`
	DeviceModel  string `json:"device_model"`
	Version      string `json:"android_version"`
	Manufacturer string `json:"manufacturer"`
	Brand        string `json:"brand"`
}

func (h *handler) connectDevice(w http.ResponseWriter, r *http.Request) {
	var body connectBody
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	device, err := h.flow().ConnectDevice(r.Context(), r.Header.Get(accessTokenHeader), body.AndroidID, body.DeviceModel, body.Version, body.Manufacturer, body.Brand)
	if h.writeFlow(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"id": device.ID, "name": device.Name}})
}

type detailBody struct {
	Name       string  `json:"name"`
	Detail     string  `json:"detail"`
	DetailType string  `json:"detail_type"`
	Initials   string  `json:"initials"`
	Currency   string  `json:"currency"`
	Gateway    string  `json:"payment_gateway"`
	GatewayIDs []int64 `json:"payment_gateway_ids"`
	DeviceID   int64   `json:"user_device_id"`
}

func (h *handler) createDetail(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Trader")
	if !ok {
		return
	}
	var body detailBody
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	code := body.Gateway
	if code == "" && len(body.GatewayIDs) > 0 {
		if err := h.deps.DB.SQL.QueryRowContext(r.Context(), `SELECT code FROM payment_gateways WHERE id = ?`, body.GatewayIDs[0]).Scan(&code); err != nil {
			writeFail(w, http.StatusBadRequest, "Платёжный метод не найден.")
			return
		}
	}
	id, err := h.flow().CreatePaymentDetail(r.Context(), actor, body.Name, body.Detail, body.DetailType, body.Initials, body.Currency, code, body.DeviceID)
	if h.writeFlow(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

type merchantBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ProjectLink string `json:"project_link"`
}

func (h *handler) createMerchantProject(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Merchant")
	if !ok {
		return
	}
	var body merchantBody
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	id, merchantUUID, err := h.flow().CreateMerchant(r.Context(), actor, body.Name, body.Description, body.ProjectLink)
	if h.writeFlow(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"message":     "Проект успешно создан",
		"merchant_id": id,
		"data":        map[string]any{"id": id, "uuid": merchantUUID},
	})
}

type orderBody struct {
	ExternalID  string      `json:"external_id"`
	MerchantID  string      `json:"merchant_id"`
	Amount      json.Number `json:"amount"`
	Currency    string      `json:"currency"`
	Gateway     string      `json:"payment_gateway"`
	DetailType  string      `json:"payment_detail_type"`
	CallbackURL string      `json:"callback_url"`
}

func headerWait(r *http.Request) time.Duration {
	raw := strings.TrimSpace(r.Header.Get("X-Max-Wait-Ms"))
	if raw == "" {
		return 0
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

func (h *handler) createOrder(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.user(w, r)
	if !ok {
		return
	}
	var body orderBody
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	started := time.Now()
	orderUUID, err := h.flow().CreateOrder(r.Context(), flow.CreateOrderInput{
		UserID:      actor,
		ExternalID:  body.ExternalID,
		MerchantID:  body.MerchantID,
		Amount:      body.Amount.String(),
		Currency:    body.Currency,
		GatewayCode: body.Gateway,
		DetailType:  body.DetailType,
		Callback:    body.CallbackURL,
		MaxWait:     headerWait(r),
	})
	_ = h.flow().EnqueueMerchantLog(r.Context(), flow.MerchantLogInput{
		MerchantUUID: body.MerchantID,
		ExternalID:   body.ExternalID,
		Amount:       body.Amount.String(),
		Currency:     body.Currency,
		Gateway:      body.Gateway,
		DetailType:   body.DetailType,
		OrderUUID:    orderUUID,
		IP:           r.RemoteAddr,
		UserAgent:    r.UserAgent(),
		Started:      started,
		Err:          err,
		Request:      body,
	})
	if h.writeFlow(w, err) {
		return
	}
	h.writeOrderView(w, r, orderUUID)
}

func (h *handler) cancelOrder(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, func(userID int64, orderUUID string) error {
		return h.flow().CancelOrder(r.Context(), userID, orderUUID)
	})
}

func (h *handler) finishOrder(w http.ResponseWriter, r *http.Request) {
	if h.deps.AppEnv == "production" {
		writeMessage(w, http.StatusNotFound, "")
		return
	}
	h.transition(w, r, func(userID int64, orderUUID string) error {
		return h.flow().FinishOrder(r.Context(), userID, orderUUID, "cancelled")
	})
}

func (h *handler) traderAccept(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Trader")
	if !ok {
		return
	}
	orderUUID := chi.URLParam(r, "uuid")
	if h.writeFlow(w, h.flow().AcceptByTrader(r.Context(), actor, orderUUID)) {
		return
	}
	h.writeOrderView(w, r, orderUUID)
}

func (h *handler) transition(w http.ResponseWriter, r *http.Request, fn func(int64, string) error) {
	actor, ok := h.user(w, r)
	if !ok {
		return
	}
	orderUUID := chi.URLParam(r, "uuid")
	if !uuidPattern.MatchString(orderUUID) {
		writeMessage(w, http.StatusNotFound, orderMissingMessage)
		return
	}
	if h.writeFlow(w, fn(actor, orderUUID)) {
		return
	}
	h.writeOrderView(w, r, orderUUID)
}

func (h *handler) sms(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Sender  string `json:"sender"`
		Message string `json:"message"`
		Type    string `json:"type"`
	}
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Type == "" {
		body.Type = "sms"
	}
	if h.writeFlow(w, h.flow().HandleSMS(r.Context(), r.Header.Get(accessTokenHeader), body.Sender, body.Message, body.Type, r.Header.Get("Idempotency-Key"))) {
		return
	}
	writeSuccess(w, http.StatusOK, map[string]any{})
}

func (h *handler) openDispute(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.user(w, r)
	if !ok {
		return
	}
	orderUUID := chi.URLParam(r, "uuid")
	body, err := h.takeReceipt(r)
	if err != nil {
		writeReceiptInvalid(w)
		return
	}
	receipt, err := h.saveReceipt(body)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.writeFlow(w, h.flow().OpenDispute(r.Context(), actor, orderUUID, receipt)) {
		h.removeReceipt(receipt)
		return
	}
	h.writeDispute(w, r, orderUUID, "")
}

func (h *handler) showDispute(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.user(w, r)
	if !ok {
		return
	}
	h.writeDisputeFor(w, r, actor, chi.URLParam(r, "uuid"), "")
}

func (h *handler) disputeReasons(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.user(w, r); !ok {
		return
	}
	writeCancelReasons(w)
}

func writeCancelReasons(w http.ResponseWriter) {
	writeSuccess(w, http.StatusOK, map[string]any{"cancel_reasons": cancelReasonOptions()})
}

func cancelReasonOptions() []map[string]string {
	return []map[string]string{
		{"value": "requires_bank_statement", "label": "Требуется выписка"},
		{"value": "requires_video_proof", "label": "Требуется видео входа в ЛК банка и в сам перевод"},
		{"value": "wrong_payment_refund_required", "label": "По данному ордеру - требует вернуть средства, отправлены по ошибке"},
		{"value": "incorrect_amount_received", "label": "Поступила неверная сумма"},
		{"value": "cancelled_by_provider", "label": "Отклонён провайдером"},
	}
}

func disputeStatuses() []map[string]any {
	return []map[string]any{
		{"name": "В ожидании", "value": "pending"},
		{"name": "Принят", "value": "accepted"},
		{"name": "Отклонён", "value": "canceled"},
	}
}

func disputePage(block map[string]any) map[string]any {
	return map[string]any{
		"disputes":      block,
		"cancelReasons": cancelReasonOptions(),
		"filtersVariants": map[string]any{
			"disputeStatuses": disputeStatuses(),
		},
	}
}

func (h *handler) supportAcceptOrder(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Support")
	if !ok {
		return
	}
	message, err := h.flow().SupportAcceptOrder(r.Context(), actor, chi.URLParam(r, "uuid"))
	if h.writeFlow(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": message})
}

func (h *handler) supportAcceptDispute(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.staff(w, r)
	if !ok {
		return
	}
	orderUUID, ok := h.orderFromDispute(w, r)
	if !ok {
		return
	}
	if h.writeFlow(w, h.flow().AcceptDispute(r.Context(), actor, orderUUID)) {
		return
	}
	h.writeDispute(w, r, orderUUID, "Спор принят")
}

type cancelDisputeBody struct {
	CancelReason string `json:"cancel_reason"`
	Comment      string `json:"comment"`
}

func (h *handler) supportCancelDispute(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.staff(w, r)
	if !ok {
		return
	}
	var body cancelDisputeBody
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	orderUUID, ok := h.orderFromDispute(w, r)
	if !ok {
		return
	}
	if h.writeFlow(w, h.flow().CancelDispute(r.Context(), actor, orderUUID, body.CancelReason, body.Comment)) {
		return
	}
	h.writeDispute(w, r, orderUUID, "Спор отклонён")
}

func (h *handler) supportRollbackDispute(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.staff(w, r)
	if !ok {
		return
	}
	orderUUID, ok := h.orderFromDispute(w, r)
	if !ok {
		return
	}
	if h.writeFlow(w, h.flow().RollbackDispute(r.Context(), actor, orderUUID)) {
		return
	}
	h.writeDispute(w, r, orderUUID, "Спор возвращён")
}

func (h *handler) supportOpenDispute(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.staff(w, r); !ok {
		return
	}
	orderUUID, ok := h.orderFromOrderKey(w, r)
	if !ok {
		return
	}
	if h.writeFlow(w, h.flow().OpenDisputeUnchecked(r.Context(), orderUUID, "")) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Спор успешно открыт"})
}

func (h *handler) payoutOffers(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.user(w, r)
	if !ok {
		return
	}
	enabled, err := h.flow().PayoutsEnabled(r.Context(), actor)
	if h.writeFlow(w, err) {
		return
	}
	if !enabled {
		writeMessage(w, http.StatusForbidden, "")
		return
	}
	menu, err := h.flow().PayoutOffers(r.Context())
	if h.writeFlow(w, err) {
		return
	}
	writeSuccess(w, http.StatusOK, menu)
}

func (h *handler) withdraw(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.user(w, r)
	if !ok {
		return
	}
	var body struct {
		Amount  json.Number `json:"amount"`
		Address string      `json:"address"`
		Network string      `json:"network"`
	}
	if err := decode(r, &body); err != nil && !errors.Is(err, io.EOF) {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	fields := map[string][]string{}
	if body.Amount.String() == "" {
		fields["amount"] = []string{"The amount field is required."}
	}
	if body.Address == "" {
		fields["address"] = []string{"The address field is required."}
	}
	if body.Network == "" {
		fields["network"] = []string{"The network field is required."}
	}
	if len(fields) > 0 {
		h.writeFlow(w, &flow.Error{Status: http.StatusUnprocessableEntity, Fields: fields, Message: "The amount field is required. (and 2 more errors)"})
		return
	}
	id, status, err := h.flow().CreateAutoWithdrawal(r.Context(), actor, body.Amount.String(), body.Address, body.Network)
	if h.writeFlow(w, err) {
		return
	}
	writeSuccess(w, http.StatusOK, map[string]any{"invoice_id": id, "status": status})
}

func (h *handler) writeOrderView(w http.ResponseWriter, r *http.Request, orderUUID string) {
	order, err := store.FindOrderByUUID(r.Context(), h.deps.DB.SQL, orderUUID)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	var external *read.External
	if h.deps.Prices.Cache != nil {
		external, err = requisite.Load(r.Context(), h.deps.Prices.Cache, h.deps.Prices.Prefix, order.ID)
		if err != nil {
			writeFail(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	view, err := read.PresentOrder(order, external, h.now())
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeSuccess(w, http.StatusOK, view)
}

func (h *handler) writeDispute(w http.ResponseWriter, r *http.Request, orderUUID, message string) {
	actor, ok := h.user(w, r)
	if !ok {
		return
	}
	h.writeDisputeFor(w, r, actor, orderUUID, message)
}

func (h *handler) writeDisputeBody(w http.ResponseWriter, r *http.Request, orderUUID, message string) {
	payload, ok := h.disputePayload(w, r, orderUUID)
	if !ok {
		return
	}
	if message == "" {
		writeSuccess(w, http.StatusOK, payload)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": message, "data": payload})
}

func (h *handler) writeDisputeFor(w http.ResponseWriter, r *http.Request, actor int64, orderUUID, message string) {
	order, err := store.FindOrderByUUID(r.Context(), h.deps.DB.SQL, orderUUID)
	if errors.Is(err, store.ErrAbsent) {
		writeMessage(w, http.StatusNotFound, orderMissingMessage)
		return
	}
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	super, err := store.HasRole(r.Context(), h.deps.DB.SQL, actor, "Admin")
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	support, err := store.HasRole(r.Context(), h.deps.DB.SQL, actor, "Support")
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !support && !read.CanSeeOrder(actor, order, super) {
		writeMessage(w, http.StatusForbidden, unauthorizedMessage)
		return
	}
	h.writeDisputeBody(w, r, orderUUID, message)
}

func (h *handler) disputePayload(w http.ResponseWriter, r *http.Request, orderUUID string) (map[string]any, bool) {
	order, err := store.FindOrderByUUID(r.Context(), h.deps.DB.SQL, orderUUID)
	if errors.Is(err, store.ErrAbsent) {
		writeMessage(w, http.StatusNotFound, orderMissingMessage)
		return nil, false
	}
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	var disputeID int64
	var status, reason, cancel, receipt sql.NullString
	err = h.deps.DB.SQL.QueryRowContext(r.Context(), `
		SELECT d.id, d.status, d.reason, d.cancel_reason, d.receipt
		FROM disputes d WHERE d.order_id = ? ORDER BY d.id DESC LIMIT 1`, order.ID).
		Scan(&disputeID, &status, &reason, &cancel, &receipt)
	if err == sql.ErrNoRows {
		writeFail(w, http.StatusBadRequest, "По сделке пока что небыло споров.")
		return nil, false
	}
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	var receiptURL any
	if receipt.Valid && receipt.String != "" {
		receiptURL = "/disputes/" + strconv.FormatInt(disputeID, 10) + "/receipt"
	}
	return map[string]any{
		"order_id":      order.UUID,
		"status":        status.String,
		"cancel_reason": nullString(cancel),
		"reason":        nullString(reason),
		"receipt_url":   receiptURL,
	}, true
}

func nullString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func (h *handler) staff(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, ok := h.user(w, r)
	if !ok {
		return 0, false
	}
	for _, role := range []string{"Support", "Admin"} {
		has, err := store.HasRole(r.Context(), h.deps.DB.SQL, id, role)
		if err != nil {
			writeFail(w, http.StatusInternalServerError, err.Error())
			return 0, false
		}
		if has {
			return id, true
		}
	}
	writeMessage(w, http.StatusForbidden, unauthorizedMessage)
	return 0, false
}

func (h *handler) orderFromDispute(w http.ResponseWriter, r *http.Request) (string, bool) {
	return h.resolveOrderKey(w, r, true)
}

func (h *handler) orderFromOrderKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	return h.resolveOrderKey(w, r, false)
}

func (h *handler) resolveOrderKey(w http.ResponseWriter, r *http.Request, numericIsDispute bool) (string, bool) {
	raw := chi.URLParam(r, "uuid")
	if uuidPattern.MatchString(raw) {
		return raw, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		model := "App\\Models\\Order"
		if numericIsDispute {
			model = "App\\Models\\Dispute"
		}
		writeMessage(w, http.StatusNotFound, "No query results for model ["+model+"].")
		return "", false
	}
	var orderUUID string
	var queryErr error
	if numericIsDispute {
		queryErr = h.deps.DB.SQL.QueryRowContext(r.Context(), `
			SELECT o.uuid FROM disputes d INNER JOIN orders o ON o.id = d.order_id WHERE d.id = ?`, id).Scan(&orderUUID)
	} else {
		queryErr = h.deps.DB.SQL.QueryRowContext(r.Context(), `SELECT uuid FROM orders WHERE id = ?`, id).Scan(&orderUUID)
	}
	if queryErr != nil {
		model := "App\\Models\\Order"
		if numericIsDispute {
			model = "App\\Models\\Dispute"
		}
		if queryErr != sql.ErrNoRows {
			writeFail(w, http.StatusInternalServerError, queryErr.Error())
			return "", false
		}
		writeMessage(w, http.StatusNotFound, "No query results for model ["+model+"].")
		return "", false
	}
	return orderUUID, true
}

func writeReceiptInvalid(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"message": "The receipt field must be a file of type: jpeg, jpg, png, pdf.",
		"errors":  map[string][]string{"receipt": {"The receipt field must be a file of type: jpeg, jpg, png, pdf."}},
	})
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return 0, false
	}
	return id, true
}
