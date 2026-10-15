package httpapi

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"finance-platform/go/internal/read"
	"finance-platform/go/internal/store"

	"github.com/go-chi/chi/v5"
)

func (h *handler) botAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get(accessTokenHeader)
		want := h.deps.BotToken
		if want == "" || subtle.ConstantTimeCompare([]byte(token), []byte(want)) != 1 {
			writeFail(w, http.StatusBadRequest, invalidAccessToken)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *handler) botRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID       json.RawMessage `json:"id"`
		Username json.RawMessage `json:"username"`
	}
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	id, idOK := jsonString(body.ID)
	username, nameOK := jsonString(body.Username)
	if len(body.ID) > 0 && !idOK {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"message": "The id field must be a string.",
			"errors":  map[string][]string{"id": {"The id field must be a string."}},
		})
		return
	}
	if len(body.Username) > 0 && !nameOK {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"message": "The username field must be a string.",
			"errors":  map[string][]string{"username": {"The username field must be a string."}},
		})
		return
	}
	user, err := h.flow().RegisterTrader(r.Context(), id, username)
	if h.writeFlow(w, err) {
		return
	}
	writeSuccess(w, http.StatusOK, map[string]any{
		"message": "Пользователь успешно зарегистрирован",
		"user": map[string]any{
			"id":              user.ID,
			"name":            user.Name,
			"email":           user.Email,
			"avatar":          user.Avatar,
			"role":            "Trader",
			"reserve_balance": "$1000.00",
		},
		"credentials": map[string]any{
			"email":    user.Email,
			"password": user.Password,
		},
	})
}

func (h *handler) botAddBalance(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email       string          `json:"email"`
		Amount      json.RawMessage `json:"amount"`
		BalanceType string          `json:"balance_type"`
	}
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	amount, _ := jsonNumber(body.Amount)
	if len(body.Amount) > 0 && amount == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"message": "The amount field must be a number.",
			"errors":  map[string][]string{"amount": {"The amount field must be a number."}},
		})
		return
	}
	updated, err := h.flow().AddBotBalance(r.Context(), body.Email, amount, body.BalanceType)
	if h.writeFlow(w, err) {
		return
	}
	writeSuccess(w, http.StatusOK, map[string]any{
		"message": "Баланс успешно добавлен",
		"user": map[string]any{
			"id":    updated.UserID,
			"name":  updated.Name,
			"email": updated.Email,
		},
		"balance_update": map[string]any{
			"type":         updated.Type,
			"added_amount": updated.Added,
			"new_balance":  updated.Balance,
		},
	})
}

func (h *handler) botReasons(w http.ResponseWriter, _ *http.Request) {
	writeCancelReasons(w)
}

func (h *handler) botOrder(w http.ResponseWriter, r *http.Request) {
	uuid := chi.URLParam(r, "uuid")
	if !uuidPattern.MatchString(uuid) {
		writeMessage(w, http.StatusNotFound, orderMissingMessage)
		return
	}
	h.writeBotOrder(w, r, func() (read.Order, error) {
		return store.FindOrderByUUID(r.Context(), h.deps.DB.SQL, uuid)
	})
}

func (h *handler) botOrderExternal(w http.ResponseWriter, r *http.Request) {
	h.writeBotOrder(w, r, func() (read.Order, error) {
		return store.FindOrderByExternal(r.Context(), h.deps.DB.SQL, chi.URLParam(r, "merchant_id"), chi.URLParam(r, "external_id"))
	})
}

func (h *handler) botOpenDispute(w http.ResponseWriter, r *http.Request) {
	orderUUID := chi.URLParam(r, "uuid")
	body, err := h.takeReceipt(r)
	if err != nil {
		writeReceiptInvalid(w)
		return
	}
	name, err := h.saveReceipt(body)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.writeFlow(w, h.flow().OpenDisputeUnchecked(r.Context(), orderUUID, name)) {
		h.removeReceipt(name)
		return
	}
	h.writeDisputeBody(w, r, orderUUID, "")
}

func (h *handler) botAcceptDispute(w http.ResponseWriter, r *http.Request) {
	if h.writeFlow(w, h.flow().AcceptDisputeBot(r.Context(), chi.URLParam(r, "uuid"))) {
		return
	}
	writeSuccess(w, http.StatusOK, []any{})
}

func (h *handler) botCancelDispute(w http.ResponseWriter, r *http.Request) {
	var body cancelDisputeBody
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.writeFlow(w, h.flow().CancelDisputeBot(r.Context(), chi.URLParam(r, "uuid"), body.CancelReason, body.Comment)) {
		return
	}
	writeSuccess(w, http.StatusOK, []any{})
}

func (h *handler) writeBotOrder(w http.ResponseWriter, r *http.Request, load func() (read.Order, error)) {
	order, err := load()
	if errors.Is(err, store.ErrAbsent) {
		writeMessage(w, http.StatusNotFound, orderMissingMessage)
		return
	}
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	view, err := read.PresentOrder(order, nil, h.now())
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	detail, user, err := h.botParties(r, order.UUID)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	var dispute any
	if order.Dispute != nil && order.Dispute.Status != nil {
		dispute = map[string]any{
			"order_id":      order.UUID,
			"status":        *order.Dispute.Status,
			"cancel_reason": order.Dispute.CancelReason,
			"reason":        order.Dispute.Reason,
		}
	}
	writeSuccess(w, http.StatusOK, map[string]any{
		"order":   view,
		"detail":  detail,
		"user":    user,
		"dispute": dispute,
	})
}

func (h *handler) botParties(r *http.Request, orderUUID string) (any, any, error) {
	var detailID sql.NullInt64
	var traderID sql.NullInt64
	err := h.deps.DB.SQL.QueryRowContext(r.Context(), `
		SELECT payment_detail_id, trader_id FROM orders WHERE uuid = ?`, orderUUID).Scan(&detailID, &traderID)
	if err != nil {
		return nil, nil, err
	}
	if !detailID.Valid {
		return nil, nil, nil
	}
	var (
		id         int64
		name       string
		detail     string
		detailType string
		initials   sql.NullString
		active     bool
		currency   sql.NullString
		deviceID   sql.NullInt64
		ownerID    sql.NullInt64
		ownerName  sql.NullString
		ownerEmail sql.NullString
	)
	err = h.deps.DB.SQL.QueryRowContext(r.Context(), `
		SELECT pd.id, pd.name, pd.detail, pd.detail_type, pd.initials, pd.is_active,
		       pd.currency, pd.user_device_id, pd.user_id, u.name, u.email
		FROM payment_details pd
		LEFT JOIN users u ON u.id = pd.user_id
		WHERE pd.id = ?`, detailID.Int64).Scan(
		&id, &name, &detail, &detailType, &initials, &active,
		&currency, &deviceID, &ownerID, &ownerName, &ownerEmail)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	view := map[string]any{
		"id":             id,
		"name":           name,
		"detail":         detail,
		"detail_type":    detailType,
		"initials":       nullString(initials),
		"is_active":      active,
		"currency":       nullString(currency),
		"user_device_id": nullInt(deviceID),
		"owner_name":     nullString(ownerName),
		"owner_email":    nullString(ownerEmail),
	}
	userID := ownerID
	cascade := h.deps.CascadePaymentDetailID
	if cascade != "" && strconv.FormatInt(detailID.Int64, 10) == cascade && traderID.Valid {
		userID = traderID
	}
	if !userID.Valid {
		return view, nil, nil
	}
	person, err := h.botUser(r, userID.Int64)
	if err != nil {
		return nil, nil, err
	}
	return view, person, nil
}

func (h *handler) botUser(r *http.Request, userID int64) (any, error) {
	var (
		name       string
		email      string
		avatarUUID sql.NullString
		style      sql.NullString
		banned     sql.NullTime
		created    sql.NullTime
		promo      sql.NullInt64
		payouts    bool
		stop       bool
		enabled    sql.NullTime
		online     bool
		payoutOn   bool
		vip        bool
		referral   sql.NullString
		traderRate sql.NullString
		has2FA     bool
		leaders    sql.NullString
		category   sql.NullInt64
	)
	err := h.deps.DB.SQL.QueryRowContext(r.Context(), `
		SELECT name, email, avatar_uuid, avatar_style, banned_at, created_at, promo_code_id,
		       payouts_enabled, stop_traffic, traffic_enabled_at, is_online, is_payout_online, is_vip,
		       referral_commission_percentage, trader_commission_rate, google2fa_secret IS NOT NULL,
		       additional_team_leader_ids, trader_category_id
		FROM users WHERE id = ?`, userID).Scan(
		&name, &email, &avatarUUID, &style, &banned, &created, &promo,
		&payouts, &stop, &enabled, &online, &payoutOn, &vip,
		&referral, &traderRate, &has2FA, &leaders, &category)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var extra any = []any{}
	if leaders.Valid && leaders.String != "" {
		if json.Unmarshal([]byte(leaders.String), &extra) != nil {
			extra = []any{}
		}
	}
	return map[string]any{
		"id":                             userID,
		"name":                           name,
		"email":                          email,
		"avatar_uuid":                    nullString(avatarUUID),
		"avatar_style":                   nullString(style),
		"apk_latest_ping_at":             nil,
		"banned_at":                      day(banned, h.deps.Location),
		"created_at":                     day(created, h.deps.Location),
		"promo_code_id":                  nullInt(promo),
		"payouts_enabled":                payouts,
		"stop_traffic":                   stop,
		"traffic_enabled_at":             clock(enabled, h.deps.Location),
		"is_online":                      online,
		"is_payout_online":               payoutOn,
		"is_vip":                         vip,
		"referral_commission_percentage": nullString(referral),
		"trader_commission_rate":         nullString(traderRate),
		"can_be_impersonated":            !banned.Valid,
		"has_2fa":                        has2FA,
		"additional_team_leader_ids":     extra,
		"trader_category_id":             nullInt(category),
	}, nil
}

func jsonString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", true
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func jsonNumber(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", true
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text), true
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String(), true
	}
	return "", false
}

func nullInt(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func day(value sql.NullTime, loc *time.Location) any {
	if !value.Valid {
		return nil
	}
	stamp := value.Time
	if loc != nil {
		stamp = stamp.In(loc)
	}
	return stamp.Format("2006-01-02")
}

func clock(value sql.NullTime, loc *time.Location) any {
	if !value.Valid {
		return nil
	}
	stamp := value.Time
	if loc != nil {
		stamp = stamp.In(loc)
	}
	return stamp.Format("2006-01-02 15:04:05")
}
