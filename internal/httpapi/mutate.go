package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"finance-platform/go/internal/flow"
	"finance-platform/go/internal/impersonate"

	"github.com/go-chi/chi/v5"
)

func (h *handler) mutateRoutes(router chi.Router) {
	router.Post("/api/frontend/admin/users/{id}/impersonate", h.startImpersonate)
	router.Post("/api/frontend/admin/security/actions", h.adminSecurityAction)
	router.Post("/api/frontend/merchant/impersonate/consume", h.consumeImpersonate("Merchant"))
	router.Post("/api/frontend/trader/impersonate/consume", h.consumeImpersonate("Trader"))
	router.Post("/api/frontend/support/impersonate/consume", h.consumeImpersonate("Support", "Merchant Support"))
	router.Post("/api/frontend/leader/impersonate/consume", h.consumeImpersonate("Team Leader"))
	router.Post("/api/frontend/merchant/impersonate/leave", h.leaveImpersonate)
	router.Post("/api/frontend/trader/impersonate/leave", h.leaveImpersonate)
	router.Post("/api/frontend/support/impersonate/leave", h.leaveImpersonate)
	router.Post("/api/frontend/leader/impersonate/leave", h.leaveImpersonate)

	router.Patch("/api/frontend/admin/password", h.changePassword("Admin"))
	router.Patch("/api/frontend/merchant/password", h.changePassword("Merchant"))
	router.Patch("/api/frontend/trader/password", h.changePassword("Trader"))
	router.Patch("/api/frontend/support/password", h.changePassword("Support", "Merchant Support"))
	router.Patch("/api/frontend/leader/password", h.changePassword("Team Leader"))

	router.Patch("/api/frontend/admin/users/{id}/toggle-online", h.toggleOnline)
	router.Delete("/api/frontend/admin/users/{id}/reset-2fa", h.reset2FA)
	router.Post("/api/frontend/admin/users/{id}/notes", h.addNote)
	router.Patch("/api/frontend/admin/settings/{key}", h.updateSetting)
	router.Patch("/api/frontend/admin/merchants/{id}/ban", h.banMerchant(true))
	router.Patch("/api/frontend/admin/merchants/{id}/unban", h.banMerchant(false))
	router.Patch("/api/frontend/admin/merchants/{id}/settings", h.merchantSettings)
	router.Patch("/api/frontend/admin/payment-details/{id}/toggle-active", h.toggleDetail)
	router.Post("/api/frontend/admin/payment-details/{id}/archive", h.archiveDetail(true))
	router.Delete("/api/frontend/admin/payment-details/{id}/unarchive", h.archiveDetail(false))
	router.Post("/api/frontend/admin/categories", h.saveCategory("categories", false))
	router.Patch("/api/frontend/admin/categories/{id}", h.saveCategory("categories", true))
	router.Delete("/api/frontend/admin/categories/{id}", h.deleteCategory("categories"))
	router.Post("/api/frontend/admin/trader-categories", h.saveCategory("trader_categories", false))
	router.Patch("/api/frontend/admin/trader-categories/{id}", h.saveCategory("trader_categories", true))
	router.Delete("/api/frontend/admin/trader-categories/{id}", h.deleteCategory("trader_categories"))
	router.Post("/api/frontend/admin/trader-categories/{id}/add-trader", h.assignTrader(true))
	router.Delete("/api/frontend/admin/trader-categories/{id}/remove-trader", h.assignTrader(false))
	router.Post("/api/frontend/admin/trader-categories/{id}/bulk-add-traders", h.bulkTraders)
	router.Delete("/api/frontend/admin/trader-categories/{id}/remove-all-traders", h.clearTraders)
	router.Post("/api/frontend/admin/trader-category-priorities/{id}/add-category", h.addPriority)
	router.Delete("/api/frontend/admin/trader-category-priorities/{id}/remove-category", h.removePriority)
	router.Post("/api/frontend/admin/promo-codes", h.savePromo(false))
	router.Patch("/api/frontend/admin/promo-codes/{id}", h.savePromo(true))
	router.Delete("/api/frontend/admin/promo-codes/{id}", h.deletePromo)
	router.Post("/api/frontend/admin/sms-stop-word", h.saveWord("sms_stop_words", "word"))
	router.Delete("/api/frontend/admin/sms-stop-word/{id}", h.deleteWord("sms_stop_words"))
	router.Post("/api/frontend/admin/sms-block-word", h.saveWord("sms_block_words", "word"))
	router.Delete("/api/frontend/admin/sms-block-word/{id}", h.deleteWord("sms_block_words"))
	router.Post("/api/frontend/admin/sender-stop-list/{id}", h.senderStop)
	router.Delete("/api/frontend/admin/sender-stop-list/{id}", h.deleteWord("sender_stop_lists"))
	router.Post("/api/frontend/admin/notifications", h.saveNotification)
	router.Post("/api/frontend/admin/payment-gateways", h.saveGateway(false))
	router.Post("/api/frontend/admin/payment-gateways/{id}", h.saveGateway(true))
	router.Patch("/api/frontend/admin/payment-gateways/{id}", h.saveGateway(true))
	router.Delete("/api/frontend/admin/payment-gateways/{id}", h.deleteGateway)
	router.Patch("/api/frontend/admin/providers/{id}/toggle", h.toggleProvider)
	router.Patch("/api/frontend/admin/provider-terminals/{id}/toggle", h.toggleTerminal)

	router.Patch("/api/frontend/merchant/{merchant}/callback", h.merchantCallback)
	router.Patch("/api/frontend/merchant/{merchant}/gateway-settings", h.merchantGateways)
	router.Patch("/api/frontend/trader/payment-details/{id}", h.traderDetail)
	router.Patch("/api/frontend/support/users/{id}/toggle-traffic", h.toggleTraffic)
}

func (h *handler) sessions() *impersonate.Service {
	return h.deps.Sessions
}

func (h *handler) startImpersonate(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	code, cabinet, expires, err := h.sessions().Start(r.Context(), actor, id, r.RemoteAddr, r.UserAgent())
	if h.writeImpersonate(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"handshake": code, "cabinet_url": cabinet, "expires_in": expires,
		},
	})
}

func (h *handler) consumeImpersonate(roles ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Code string `json:"code"`
		}
		if err := decode(r, &body); err != nil || strings.TrimSpace(body.Code) == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Недействительный или уже использованный код"})
			return
		}
		payload, err := h.sessions().Consume(r.Context(), body.Code, roles, r.RemoteAddr, r.UserAgent())
		if h.writeImpersonate(w, err) {
			return
		}
		payload["success"] = true
		writeJSON(w, http.StatusOK, payload)
	}
}

func (h *handler) leaveImpersonate(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.user(w, r); !ok {
		return
	}
	if h.writeImpersonate(w, h.sessions().Leave(r.Context(), r.Header.Get(accessTokenHeader))) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Просмотр завершен"})
}

func (h *handler) writeImpersonate(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var status *impersonate.StatusError
	if errors.As(err, &status) {
		writeJSON(w, status.Status, map[string]any{"success": false, "message": status.Message})
		return true
	}
	writeFail(w, http.StatusInternalServerError, "impersonation unavailable")
	return true
}

type passwordBody struct {
	Current string `json:"current_password"`
	Next    string `json:"password"`
	Confirm string `json:"password_confirmation"`
}

func (h *handler) changePassword(roles ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.actorRoles(w, r, roles...)
		if !ok {
			return
		}
		if _, viewing := impersonate.FromContext(r.Context()); viewing {
			writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "message": "Смена пароля недоступна в режиме просмотра"})
			return
		}
		var body passwordBody
		if err := decode(r, &body); err != nil {
			writeFail(w, http.StatusBadRequest, err.Error())
			return
		}
		if h.writeFlow(w, h.flow().ChangePassword(r.Context(), id, body.Current, body.Next, body.Confirm)) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Пароль обновлён"})
	}
}

func (h *handler) actorRoles(w http.ResponseWriter, r *http.Request, roles ...string) (int64, bool) {
	id, ok := h.user(w, r)
	if !ok {
		return 0, false
	}
	for _, role := range roles {
		var n int
		err := h.deps.DB.SQL.QueryRowContext(r.Context(), `
			SELECT COUNT(*) FROM user_roles ur INNER JOIN roles r ON r.id = ur.role_id
			WHERE ur.user_id = ? AND r.name = ?`, id, role).Scan(&n)
		if err != nil {
			writeFail(w, http.StatusInternalServerError, "cabinet unavailable")
			return 0, false
		}
		if n > 0 {
			return id, true
		}
	}
	writeMessage(w, http.StatusForbidden, unauthorizedMessage)
	return 0, false
}

func (h *handler) toggleOnline(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	var raw map[string]json.RawMessage
	if err := decode(r, &raw); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	onlineFlag := flagPointer(raw["is_online"])
	payoutFlag := flagPointer(raw["is_payout_online"])
	online, payout, err := h.flow().ToggleOnline(r.Context(), actor, id, onlineFlag, payoutFlag)
	if h.writeFlow(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Статус обновлен",
		"data":    map[string]any{"is_online": online, "is_payout_online": payout},
	})
}

func (h *handler) reset2FA(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	if h.writeFlow(w, h.flow().Reset2FA(r.Context(), actor, id)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "2FA сброшена"})
}

func (h *handler) addNote(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.writeFlow(w, h.flow().AddNote(r.Context(), actor, id, body.Content)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Заметка сохранена"})
}

func (h *handler) updateSetting(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	var body map[string]json.RawMessage
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	message, err := h.flow().UpdateSetting(r.Context(), actor, chi.URLParam(r, "key"), body)
	if h.writeFlow(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": message})
}

func (h *handler) banMerchant(ban bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := h.actor(w, r, "Admin")
		if !ok {
			return
		}
		id, ok := numberParam(r, "id")
		if !ok {
			writeMessage(w, http.StatusNotFound, "Not found.")
			return
		}
		if h.writeFlow(w, h.flow().BanMerchant(r.Context(), actor, id, ban)) {
			return
		}
		message := "Проект разблокирован"
		if ban {
			message = "Проект заблокирован"
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": message})
	}
}

func (h *handler) merchantSettings(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	var body struct {
		Market string `json:"market"`
		Wait   string `json:"max_order_wait_time"`
	}
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	wait, err := parseWait(body.Wait)
	if err != nil {
		writeFail(w, http.StatusUnprocessableEntity, "max_order_wait_time")
		return
	}
	if h.writeFlow(w, h.flow().SaveMerchantSettings(r.Context(), actor, id, body.Market, wait)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Настройки проекта сохранены"})
}

func parseWait(raw string) (*int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var value int
	if _, err := json.Number(raw).Int64(); err != nil {
		return nil, err
	}
	n, _ := json.Number(raw).Int64()
	value = int(n)
	return &value, nil
}

func (h *handler) toggleDetail(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	if h.writeFlow(w, h.flow().ToggleDetail(r.Context(), actor, id)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Реквизит обновлён"})
}

func (h *handler) archiveDetail(archive bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := h.actor(w, r, "Admin")
		if !ok {
			return
		}
		id, ok := numberParam(r, "id")
		if !ok {
			writeMessage(w, http.StatusNotFound, "Not found.")
			return
		}
		if h.writeFlow(w, h.flow().ArchiveDetail(r.Context(), actor, id, archive)) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Реквизит обновлён"})
	}
}

type categoryBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Active      *bool  `json:"is_active"`
}

func (h *handler) saveCategory(table string, existing bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := h.actor(w, r, "Admin")
		if !ok {
			return
		}
		var id int64
		if existing {
			var okID bool
			id, okID = numberParam(r, "id")
			if !okID {
				writeMessage(w, http.StatusNotFound, "Not found.")
				return
			}
		}
		var body categoryBody
		if err := decode(r, &body); err != nil {
			writeFail(w, http.StatusBadRequest, err.Error())
			return
		}
		saved, err := h.flow().SaveCategory(r.Context(), actor, table, id, body.Name, body.Description, body.Active)
		if h.writeFlow(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Категория сохранена", "data": map[string]any{"id": saved}})
	}
}

func (h *handler) deleteCategory(table string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := h.actor(w, r, "Admin")
		if !ok {
			return
		}
		id, ok := numberParam(r, "id")
		if !ok {
			writeMessage(w, http.StatusNotFound, "Not found.")
			return
		}
		if h.writeFlow(w, h.flow().DeleteCategory(r.Context(), actor, table, id)) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Категория удалена"})
	}
}

func (h *handler) assignTrader(assign bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := h.actor(w, r, "Admin")
		if !ok {
			return
		}
		categoryID, ok := numberParam(r, "id")
		if !ok {
			writeMessage(w, http.StatusNotFound, "Not found.")
			return
		}
		var body struct {
			TraderID int64 `json:"trader_id"`
		}
		if err := decode(r, &body); err != nil {
			writeFail(w, http.StatusBadRequest, err.Error())
			return
		}
		if h.writeFlow(w, h.flow().AssignTrader(r.Context(), actor, categoryID, body.TraderID, assign)) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Трейдер обновлён"})
	}
}

func (h *handler) bulkTraders(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	categoryID, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	var body struct {
		IDs []int64 `json:"trader_ids"`
	}
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, traderID := range body.IDs {
		if h.writeFlow(w, h.flow().AssignTrader(r.Context(), actor, categoryID, traderID, true)) {
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Трейдеры обновлены"})
}

func (h *handler) clearTraders(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	if h.writeFlow(w, h.flow().ClearCategoryTraders(r.Context(), actor, id)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Трейдеры сняты"})
}

func (h *handler) addPriority(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	merchantID, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	var body struct {
		CategoryID int64 `json:"category_id"`
	}
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.writeFlow(w, h.flow().SavePriority(r.Context(), actor, merchantID, body.CategoryID)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Приоритет сохранён"})
}

func (h *handler) removePriority(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	merchantID, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	var body struct {
		CategoryID int64 `json:"category_id"`
	}
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.writeFlow(w, h.flow().DeletePriority(r.Context(), actor, merchantID, body.CategoryID)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Приоритет удалён"})
}

func rawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return ""
}

func flexInt(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var number int64
	if json.Unmarshal(raw, &number) == nil {
		return number, true
	}
	text := strings.TrimSpace(rawText(raw))
	if text == "" {
		return 0, false
	}
	number, err := strconv.ParseInt(text, 10, 64)
	return number, err == nil
}

func flagPointer(raw json.RawMessage) *int {
	flag, ok := flexBool(raw)
	if !ok {
		return nil
	}
	value := 0
	if flag {
		value = 1
	}
	return &value
}

func flexBool(raw json.RawMessage) (bool, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return false, false
	}
	var flag bool
	if json.Unmarshal(raw, &flag) == nil {
		return flag, true
	}
	var number int64
	if json.Unmarshal(raw, &number) == nil {
		return number != 0, true
	}
	switch strings.ToLower(strings.TrimSpace(rawText(raw))) {
	case "1", "true":
		return true, true
	case "0", "false":
		return false, true
	default:
		return false, false
	}
}

func (h *handler) savePromo(existing bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := h.actor(w, r, "Admin")
		if !ok {
			return
		}
		var id int64
		if existing {
			var okID bool
			id, okID = numberParam(r, "id")
			if !okID {
				writeMessage(w, http.StatusNotFound, "Not found.")
				return
			}
		}
		var raw map[string]json.RawMessage
		if err := decode(r, &raw); err != nil {
			writeFail(w, http.StatusBadRequest, err.Error())
			return
		}
		code := rawText(raw["code"])
		leader, leaderSet := flexInt(raw["team_leader_id"])
		maxUses, maxSet := flexInt(raw["max_uses"])
		active := true
		if flag, okFlag := flexBool(raw["is_active"]); okFlag {
			active = flag
		}
		if existing {
			var storedCode string
			var storedLeader int64
			err := h.deps.DB.SQL.QueryRowContext(r.Context(), `SELECT code, team_leader_id FROM promo_codes WHERE id = ?`, id).Scan(&storedCode, &storedLeader)
			if err == sql.ErrNoRows {
				writeFail(w, http.StatusNotFound, "Промокод не найден")
				return
			}
			if err != nil {
				writeFail(w, http.StatusInternalServerError, "cabinet unavailable")
				return
			}
			if strings.TrimSpace(code) == "" {
				code = storedCode
			}
			if !leaderSet {
				leader = storedLeader
			}
			if !maxSet {
				var storedMax int
				if err := h.deps.DB.SQL.QueryRowContext(r.Context(), `SELECT max_uses FROM promo_codes WHERE id = ?`, id).Scan(&storedMax); err != nil {
					writeFail(w, http.StatusInternalServerError, "cabinet unavailable")
					return
				}
				maxUses = int64(storedMax)
			}
		} else if !leaderSet {
			leader = actor
		}
		saved, err := h.flow().SavePromo(r.Context(), actor, id, leader, code, int(maxUses), active)
		if h.writeFlow(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Промокод сохранён", "data": map[string]any{"id": saved}})
	}
}

func (h *handler) deletePromo(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	if h.writeFlow(w, h.flow().DeletePromo(r.Context(), actor, id)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Промокод удалён"})
}

func (h *handler) saveWord(table, field string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := h.actor(w, r, "Admin")
		if !ok {
			return
		}
		var body map[string]string
		if err := decode(r, &body); err != nil {
			writeFail(w, http.StatusBadRequest, err.Error())
			return
		}
		id, err := h.flow().SaveWord(r.Context(), actor, table, body[field])
		if h.writeFlow(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Слово сохранено", "data": map[string]any{"id": id}})
	}
}

func (h *handler) deleteWord(table string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := h.actor(w, r, "Admin")
		if !ok {
			return
		}
		id, ok := numberParam(r, "id")
		if !ok {
			writeMessage(w, http.StatusNotFound, "Not found.")
			return
		}
		if h.writeFlow(w, h.flow().DeleteWord(r.Context(), actor, table, id)) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Слово удалено"})
	}
}

func (h *handler) senderStop(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	saved, err := h.flow().SenderFromSMS(r.Context(), actor, id)
	if h.writeFlow(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Отправитель добавлен", "data": map[string]any{"id": saved}})
}

func (h *handler) saveNotification(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := h.flow().SaveNotification(r.Context(), actor, body.Message)
	if h.writeFlow(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Уведомление сохранено", "data": map[string]any{"id": id}})
}

func (h *handler) toggleProvider(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	if h.writeFlow(w, h.flow().ToggleProvider(r.Context(), actor, id)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Провайдер обновлён"})
}

func (h *handler) toggleTerminal(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	if h.writeFlow(w, h.flow().ToggleTerminal(r.Context(), actor, id)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Терминал обновлён"})
}

func (h *handler) merchantCallback(w http.ResponseWriter, r *http.Request) {
	id, ok := h.actor(w, r, "Merchant")
	if !ok {
		return
	}
	merchantID, ok := numberParam(r, "merchant")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	var body struct {
		Callback string `json:"callback_url"`
	}
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.writeFlow(w, h.flow().SaveCallback(r.Context(), id, merchantID, body.Callback)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Callback обновлён"})
}

func (h *handler) merchantGateways(w http.ResponseWriter, r *http.Request) {
	id, ok := h.actor(w, r, "Merchant")
	if !ok {
		return
	}
	merchantID, ok := numberParam(r, "merchant")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	var body struct {
		Settings json.RawMessage `json:"gateway_settings"`
	}
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.writeFlow(w, h.flow().SaveGatewaySettings(r.Context(), id, merchantID, body.Settings)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Шлюзы обновлены"})
}

func (h *handler) traderDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := h.actor(w, r, "Trader")
	if !ok {
		return
	}
	detailID, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	var body struct {
		Name       string `json:"name"`
		Detail     string `json:"detail"`
		DetailType string `json:"detail_type"`
		Initials   string `json:"initials"`
		Active     *bool  `json:"is_active"`
	}
	if err := decode(r, &body); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.writeFlow(w, h.flow().SaveOwnDetail(r.Context(), id, detailID, body.Name, body.Detail, body.DetailType, body.Initials, body.Active)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Реквизит обновлён"})
}

func (h *handler) toggleTraffic(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actorRoles(w, r, "Support", "Merchant Support")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	if h.writeFlow(w, h.flow().ToggleTraffic(r.Context(), actor, id)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Трафик обновлён"})
}

func (h *handler) integrationToken(w http.ResponseWriter, r *http.Request, userID int64) {
	if _, viewing := impersonate.FromContext(r.Context()); viewing {
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "message": "Токен интеграции скрыт в режиме просмотра"})
		return
	}
	var token sql.NullString
	err := h.deps.DB.SQL.QueryRowContext(r.Context(), `SELECT api_access_token FROM users WHERE id = ?`, userID).Scan(&token)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "cabinet unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"token": token.String}})
}

func (h *handler) saveGateway(existing bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := h.actor(w, r, "Admin")
		if !ok {
			return
		}
		var id int64
		if existing {
			var okID bool
			id, okID = numberParam(r, "id")
			if !okID {
				writeMessage(w, http.StatusNotFound, "Not found.")
				return
			}
		}
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			if err := r.ParseForm(); err != nil {
				writeFail(w, http.StatusBadRequest, "Некорректная форма")
				return
			}
		}
		form := flow.GatewayForm{
			Name:           r.FormValue("name"),
			Code:           r.FormValue("code"),
			NSPK:           r.FormValue("nspk_schema"),
			Currency:       r.FormValue("currency"),
			MinLimit:       r.FormValue("min_limit"),
			MaxLimit:       r.FormValue("max_limit"),
			TraderOrders:   r.FormValue("trader_commission_rate_for_orders"),
			TraderPayouts:  r.FormValue("trader_commission_rate_for_payouts"),
			ServiceOrders:  r.FormValue("total_service_commission_rate_for_orders"),
			ServicePayouts: r.FormValue("total_service_commission_rate_for_payouts"),
			Active:         formFlag(r, "is_active"),
			Intrabank:      formFlag(r, "is_intrabank"),
			ReserveOrders:  formInt(r, "reservation_time_for_orders"),
			ReservePayouts: formInt(r, "reservation_time_for_payouts"),
			DetailTypes:    formList(r, "detail_types"),
			Senders:        formList(r, "sms_senders"),
		}
		if _, present := r.Form["is_transgran"]; present {
			flag := formFlag(r, "is_transgran")
			form.Transgran = &flag
		}
		saved, err := h.flow().SaveGateway(r.Context(), actor, id, form)
		if h.writeFlow(w, err) {
			return
		}
		message := "Платёжный метод создан"
		if existing {
			message = "Платёжный метод обновлён"
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": message, "data": map[string]any{"id": saved}})
	}
}

func (h *handler) deleteGateway(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	if h.writeFlow(w, h.flow().DeleteGateway(r.Context(), actor, id)) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Платёжный метод удалён"})
}

func formList(r *http.Request, key string) []string {
	values := append([]string{}, r.Form[key]...)
	values = append(values, r.Form[key+"[]"]...)
	return values
}

func formFlag(r *http.Request, key string) bool {
	switch strings.ToLower(strings.TrimSpace(r.FormValue(key))) {
	case "1", "true", "on":
		return true
	default:
		return false
	}
}

func formInt(r *http.Request, key string) int {
	number, _ := strconv.Atoi(strings.TrimSpace(r.FormValue(key)))
	return number
}
