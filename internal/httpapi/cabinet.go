package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"finance-platform/go/internal/auditlog"
	"finance-platform/go/internal/auth"
	"finance-platform/go/internal/cabinet"
	"finance-platform/go/internal/store"

	"github.com/go-chi/chi/v5"
)

// merchant-frontend.php is not loaded by the Laravel bootstrap, so those routes are not ported.
// GET /api/frontend/admin/user-balances is commented out in admin-frontend.php and stays unimplemented.

func (h *handler) cabinetRoutes(router chi.Router) {
	router.Get("/api/frontend/admin/dashboard", h.adminDashboard)
	router.Get("/api/frontend/admin/menu", h.adminMenu)
	router.Get("/api/frontend/admin/merchant-statistics", h.adminMerchantStatistics)
	router.Get("/api/frontend/admin/trader-statistics", h.adminTraderStatistics)
	router.Get("/api/frontend/admin/users/form-options", h.adminFormOptions)
	router.Get("/api/frontend/admin/users", h.adminUsers)
	router.Get("/api/frontend/admin/users/{id}", h.adminUser)
	router.Get("/api/frontend/admin/users/{id}/notes", h.adminNotes)
	router.Get("/api/frontend/admin/users/{id}/wallet", h.adminWallet)
	router.Get("/api/frontend/admin/orders", h.adminOrders)
	router.Get("/api/frontend/admin/disputes", h.adminDisputes)
	router.Get("/api/frontend/admin/payouts", h.adminPayouts)
	router.Get("/api/frontend/admin/payouts/{id}", h.adminPayout)
	router.Get("/api/frontend/admin/deposits", h.adminDeposits)
	router.Get("/api/frontend/admin/withdrawals", h.adminWithdrawals)
	router.Get("/api/frontend/admin/merchants", h.adminMerchants)
	router.Get("/api/frontend/admin/merchants/{id}", h.adminMerchant)
	router.Get("/api/frontend/admin/payment-details", h.adminDetails)
	router.Get("/api/frontend/admin/payment-gateways", h.adminGateways)
	router.Get("/api/frontend/admin/payment-gateways/{id}", h.adminGateway)
	router.Get("/api/frontend/admin/currencies", h.adminRates)
	router.Get("/api/frontend/admin/enabled-cards", h.adminEnabled)
	router.Get("/api/frontend/admin/filters/detail-types", h.adminDetailTypes)
	router.Get("/api/frontend/admin/filters/payment-gateways", h.adminGatewayFilter)
	router.Get("/api/frontend/admin/filters/users", h.adminUserFilter)
	router.Get("/api/frontend/admin/categories", h.adminCategories)
	router.Get("/api/frontend/admin/trader-categories", h.adminTraderCategories)
	router.Get("/api/frontend/admin/trader-categories/{id}/manage-traders", h.adminManageTraders)
	router.Get("/api/frontend/admin/trader-category-priorities", h.adminPriorities)
	router.Get("/api/frontend/admin/trader-category-priorities/{id}", h.adminPriority)
	router.Get("/api/frontend/admin/settings", h.adminSettings)
	router.Get("/api/frontend/admin/user-logins", h.adminUserLogins)
	router.Get("/api/frontend/admin/security/actions", h.adminSecurityHistory)
	router.Get("/api/frontend/admin/sms-logs", h.adminSmsLogs)
	router.Get("/api/frontend/admin/promo-codes", h.adminPromos)
	router.Get("/api/frontend/admin/notifications", h.adminNotifications)
	router.Get("/api/frontend/admin/merchant-api-logs", h.adminMerchantLogs)
	router.Get("/api/frontend/admin/callback-logs", h.adminCallbackLogs)
	router.Get("/api/frontend/admin/provider-logs", h.adminProviderLogs)
	router.Get("/api/frontend/admin/provider-callback-logs", h.adminProviderCallbackLogs)
	router.Get("/api/frontend/admin/providers", h.adminProviders)
	router.Get("/api/frontend/admin/provider-terminals", h.adminTerminals)
	router.Get("/api/frontend/admin/provider-terminals/{id}", h.adminTerminal)
	router.Get("/api/frontend/admin/requisite-providers", h.adminRequisiteProviders)
	router.Get("/api/frontend/admin/trader-devices", h.adminDevices)

	router.Get("/api/frontend/merchant/dashboard", h.merchantDashboard)
	router.Get("/api/frontend/merchant/integration", h.merchantIntegration)
	router.Get("/api/frontend/merchant/wallet", h.merchantWallet)
	router.Get("/api/frontend/merchant/finances", h.merchantWallet)
	router.Get("/api/frontend/merchant/payouts", h.merchantPayouts)
	router.Get("/api/frontend/merchant/list", h.merchantProjects)
	router.Get("/api/frontend/merchant/payments/list", h.merchantPayments)
	router.Get("/api/frontend/merchant/payments/list/{merchant}", h.merchantPayments)
	router.Get("/api/frontend/merchant/payments/create-data", h.merchantCreateData)
	router.Get("/api/frontend/merchant/rates", h.merchantRates)
	router.Get("/api/frontend/merchant/disputes/list", h.merchantDisputes)
	router.Get("/api/frontend/merchant/{merchant}", h.merchantProject)

	router.Get("/api/frontend/trader/dashboard", h.traderDashboard)
	router.Get("/api/frontend/trader/rates", h.traderRates)
	router.Get("/api/frontend/trader/integration", h.traderIntegration)
	router.Get("/api/frontend/trader/settings", h.traderSettings)
	router.Get("/api/frontend/trader/wallet", h.traderWallet)
	router.Get("/api/frontend/trader/finances", h.traderWallet)
	router.Get("/api/frontend/trader/payouts", h.traderPayouts)
	router.Get("/api/frontend/trader/payment-details", h.traderDetails)
	router.Get("/api/frontend/trader/payment-methods", h.traderMethods)
	router.Get("/api/frontend/trader/currencies", h.traderRates)
	router.Get("/api/frontend/trader/payments/list", h.traderPayments)
	router.Get("/api/frontend/trader/disputes/list", h.traderDisputes)
	router.Get("/api/frontend/trader/devices", h.traderDevices)

	router.Get("/api/frontend/support/me", h.supportMe)
	router.Get("/api/frontend/support/menu", h.supportMenu)
	router.Get("/api/frontend/support/dashboard", h.supportDashboard)
	router.Get("/api/frontend/support/users", h.supportUsers)
	router.Get("/api/frontend/support/orders", h.supportOrders)
	router.Get("/api/frontend/support/disputes", h.supportDisputes)
	router.Get("/api/frontend/support/devices", h.supportDevices)
	router.Get("/api/frontend/support/payments", h.supportOrders)
	router.Get("/api/frontend/support/integration", h.supportIntegration)

	router.Get("/api/frontend/leader/me", h.leaderMe)
	router.Get("/api/frontend/leader/menu", h.leaderMenu)
	router.Get("/api/frontend/leader/dashboard", h.leaderDashboard)
	router.Get("/api/frontend/leader/finances", h.leaderFinances)
	router.Get("/api/frontend/leader/referrals", h.leaderReferrals)
}

func (h *handler) openCabinet(w http.ResponseWriter, r *http.Request, roles ...string) (*cabinet.Reader, int64, bool) {
	id, ok := h.user(w, r)
	if !ok {
		return nil, 0, false
	}
	if h.deps.DB.SQL == nil {
		writeFail(w, http.StatusInternalServerError, "cabinet unavailable")
		return nil, 0, false
	}
	allowed := false
	for _, role := range roles {
		has, err := store.HasRole(r.Context(), h.deps.DB.SQL, id, role)
		if err != nil {
			writeFail(w, http.StatusInternalServerError, "cabinet unavailable")
			return nil, 0, false
		}
		if has {
			allowed = true
			break
		}
	}
	if !allowed {
		writeMessage(w, http.StatusForbidden, unauthorizedMessage)
		return nil, 0, false
	}
	return &cabinet.Reader{
		DB: h.deps.DB.SQL, Location: h.deps.Location, Quotes: h.deps.Quotes, Cascade: h.deps.FeatureCascade,
	}, id, true
}

func (h *handler) showCabinet(w http.ResponseWriter, data any, err error) {
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "cabinet unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

func queryMerchantID(r *http.Request) int64 {
	id, err := strconv.ParseInt(r.URL.Query().Get("merchant_id"), 10, 64)
	if err != nil || id < 0 {
		return 0
	}
	return id
}

func numberParam(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return id, err == nil && id > 0
}

func orderStatuses() []map[string]any {
	return []map[string]any{
		{"name": "Оплачен", "value": "success"},
		{"name": "В обработке", "value": "pending"},
		{"name": "Отклонен", "value": "fail"},
	}
}

func invoiceStatuses() []map[string]any {
	return []map[string]any{
		{"name": "Ожидание", "value": "pending"},
		{"name": "Успех", "value": "success"},
		{"name": "Ошибка", "value": "fail"},
	}
}

func (h *handler) adminDashboard(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	data, err := reader.AdminDashboard(r.Context(), r.URL.Query().Get("period"), queryMerchantID(r))
	h.showCabinet(w, data, err)
}

func (h *handler) adminMenu(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	h.showCabinet(w, reader.Menu(r.Context()), nil)
}

func (h *handler) adminFormOptions(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	roles, err := reader.RoleOptions(r.Context())
	if err != nil {
		h.showCabinet(w, nil, err)
		return
	}
	categories, err := reader.TraderCategoryOptions(r.Context())
	if err != nil {
		h.showCabinet(w, nil, err)
		return
	}
	merchantCategories, err := reader.CategoryOptions(r.Context())
	h.showCabinet(w, map[string]any{"roles": roles, "traderCategories": categories, "categories": merchantCategories}, err)
}

func (h *handler) adminUsers(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	page := cabinet.FromRequest(r, 15)
	block, err := reader.Users(r.Context(), page, cabinet.Filter(r, "user"), cabinet.Filter(r, "roles"), cabinet.Filter(r, "online"), cabinet.Filter(r, "traffic"))
	if err != nil {
		h.showCabinet(w, nil, err)
		return
	}
	roles, err := reader.RoleOptions(r.Context())
	h.showCabinet(w, map[string]any{
		"users":           block,
		"filtersVariants": map[string]any{"roles": roles},
	}, err)
}

func (h *handler) adminUser(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	user, err := reader.UserCard(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	if err != nil {
		h.showCabinet(w, nil, err)
		return
	}
	roles, err := reader.RoleOptions(r.Context())
	if err != nil {
		h.showCabinet(w, nil, err)
		return
	}
	categories, err := reader.TraderCategoryOptions(r.Context())
	if err != nil {
		h.showCabinet(w, nil, err)
		return
	}
	merchantCategories, err := reader.CategoryOptions(r.Context())
	h.showCabinet(w, map[string]any{
		"user":                user,
		"roles":               roles,
		"traderCategories":    categories,
		"categories":          merchantCategories,
		"can_update_password": true,
	}, err)
}

func (h *handler) adminNotes(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	notes, err := reader.Notes(r.Context(), id)
	h.showCabinet(w, map[string]any{"notes": notes}, err)
}

func (h *handler) adminWallet(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	page := cabinet.FromRequest(r, 15)
	tx, err := reader.Transactions(r.Context(), page, id)
	h.showCabinet(w, map[string]any{
		"walletStats":  reader.WalletStats(r.Context(), id, "trust_balance"),
		"transactions": tx,
	}, err)
}

func (h *handler) adminOrders(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	where, args := cabinet.OrderFilter(cabinet.Filter(r, "uuid"), cabinet.Filter(r, "externalID"), cabinet.Filter(r, "user"), cabinet.Filter(r, "paymentDetail"), cabinet.Filter(r, "orderStatuses"), 0)
	block, err := reader.Orders(r.Context(), cabinet.FromRequest(r, 15), where, args)
	h.showCabinet(w, map[string]any{
		"orders": block, "filters": map[string]any{},
		"filtersVariants": map[string]any{"orderStatuses": orderStatuses()},
	}, err)
}

func (h *handler) adminDisputes(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	status := cabinet.Filter(r, "disputeStatuses")
	if status == "" {
		status = cabinet.Filter(r, "status")
	}
	block, err := reader.Disputes(r.Context(), cabinet.FromRequest(r, 15), status, 0, 0)
	h.showCabinet(w, disputePage(block), err)
}

func (h *handler) adminPayouts(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	page := cabinet.FromRequest(r, 15)
	payouts, err := reader.Payouts(r.Context(), page, 0, 0, "")
	if err != nil {
		h.showCabinet(w, nil, err)
		return
	}
	refusals, err := reader.Payouts(r.Context(), page, 0, 0, "fail")
	if err != nil {
		h.showCabinet(w, nil, err)
		return
	}
	gateways, err := reader.PayoutGateways(r.Context(), page)
	if err != nil {
		h.showCabinet(w, nil, err)
		return
	}
	offers, err := reader.PayoutOffers(r.Context(), page)
	h.showCabinet(w, map[string]any{
		"payouts": payouts, "problematicPayouts": refusals,
		"payoutGateways": gateways, "payoutOffers": offers,
	}, err)
}

func (h *handler) adminPayout(w http.ResponseWriter, r *http.Request) {
	_, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "No query results for model [App\\Models\\Payout].")
		return
	}
	var uuid, status, amount, currency string
	err := h.deps.DB.SQL.QueryRowContext(r.Context(), `
		SELECT uuid, COALESCE(status, ''), COALESCE(payout_amount, '0'), COALESCE(currency, 'rub')
		FROM payouts WHERE id = ?`, id).Scan(&uuid, &status, &amount, &currency)
	if errors.Is(err, sql.ErrNoRows) {
		writeMessage(w, http.StatusNotFound, "No query results for model [App\\Models\\Payout].")
		return
	}
	h.showCabinet(w, map[string]any{"id": id, "uuid": uuid, "status": status, "amount": amount, "currency": currency}, err)
}

func (h *handler) adminDeposits(w http.ResponseWriter, r *http.Request) {
	h.invoiceList(w, r, "deposit")
}

func (h *handler) adminWithdrawals(w http.ResponseWriter, r *http.Request) {
	h.invoiceList(w, r, "withdrawal")
}

func (h *handler) invoiceList(w http.ResponseWriter, r *http.Request, kind string) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	block, err := reader.Invoices(r.Context(), cabinet.FromRequest(r, 15), kind, cabinet.Filter(r, "invoiceStatuses"), cabinet.Filter(r, "user"), 0)
	h.showCabinet(w, map[string]any{
		"invoices":        block,
		"filtersVariants": map[string]any{"invoiceStatuses": invoiceStatuses()},
	}, err)
}

func (h *handler) adminMerchants(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	block, err := reader.Merchants(r.Context(), cabinet.FromRequest(r, 15), cabinet.Filter(r, "user"), 0)
	h.showCabinet(w, map[string]any{"merchants": block}, err)
}

func (h *handler) adminMerchant(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	card, err := reader.MerchantCard(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	h.showCabinet(w, card, err)
}

func (h *handler) adminMerchantStatistics(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	data, err := reader.MerchantStatistics(r.Context(), r.URL.Query().Get("period"))
	h.showCabinet(w, data, err)
}

func (h *handler) adminTraderStatistics(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	data, err := reader.TraderStatistics(r.Context(), r.URL.Query().Get("period"))
	h.showCabinet(w, data, err)
}

func (h *handler) adminDetails(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	block, err := reader.Details(r.Context(), cabinet.FromRequest(r, 15), 0)
	h.showCabinet(w, map[string]any{"paymentDetails": block}, err)
}

func (h *handler) adminGateways(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	block, err := reader.Gateways(r.Context(), cabinet.FromRequest(r, 15))
	h.showCabinet(w, map[string]any{"paymentGateways": block}, err)
}

func (h *handler) adminGateway(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	item, err := reader.Gateway(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	h.showCabinet(w, item, err)
}

func (h *handler) adminRates(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	h.showCabinet(w, reader.Rates(r.Context()), nil)
}

func (h *handler) adminEnabled(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	gatewayID, _ := strconv.ParseInt(r.URL.Query().Get("payment_gateway_id"), 10, 64)
	userID, _ := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	h.showCabinet(w, reader.Enabled(r.Context(), r.URL.Query().Get("detail_type"), gatewayID, userID), nil)
}

func (h *handler) adminDetailTypes(w http.ResponseWriter, r *http.Request) {
	_, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	h.showCabinet(w, map[string]any{"detailTypes": []map[string]any{
		{"value": "card", "name": "Карта"},
		{"value": "phone", "name": "Телефон"},
		{"value": "sim", "name": "SIM"},
	}}, nil)
}

func (h *handler) adminGatewayFilter(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	block, err := reader.Gateways(r.Context(), cabinet.Page{Number: 1, Size: 100})
	h.showCabinet(w, map[string]any{"paymentGateways": block}, err)
}

func (h *handler) adminUserFilter(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	block, err := reader.Users(r.Context(), cabinet.Page{Number: 1, Size: 100}, "", "", "", "")
	h.showCabinet(w, map[string]any{"users": block}, err)
}

func (h *handler) adminCategories(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	block, err := reader.Categories(r.Context(), cabinet.FromRequest(r, 15), "categories")
	h.showCabinet(w, map[string]any{"categories": block}, err)
}

func (h *handler) adminTraderCategories(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	block, err := reader.Categories(r.Context(), cabinet.FromRequest(r, 15), "trader_categories")
	h.showCabinet(w, map[string]any{"categories": block}, err)
}

func (h *handler) adminManageTraders(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	block, err := reader.Users(r.Context(), cabinet.FromRequest(r, 15), "", "Trader", "", "")
	h.showCabinet(w, map[string]any{"traders": block}, err)
}

func (h *handler) adminPriorities(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	block, err := reader.Merchants(r.Context(), cabinet.Page{Number: 1, Size: 500}, "", 0)
	h.showCabinet(w, map[string]any{"merchants": block}, err)
}

func (h *handler) adminPriority(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	card, err := reader.PriorityCard(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	h.showCabinet(w, card, err)
}

func (h *handler) adminSettings(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	data, err := reader.Settings(r.Context())
	h.showCabinet(w, data, err)
}

func (h *handler) adminUserLogins(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	block, err := reader.UserLogins(r.Context(), cabinet.FromRequest(r, 25))
	h.showCabinet(w, map[string]any{"logins": block}, err)
}

func (h *handler) adminSecurityAction(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r, "Admin")
	if !ok {
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeFail(w, http.StatusBadRequest, "Неизвестное действие.")
		return
	}
	result, err := h.flow().SecurityAction(r.Context(), actor, body.Action)
	if h.writeFlow(w, err) {
		return
	}
	for _, token := range result.Tokens {
		_ = auth.ForgetAccessToken(r.Context(), h.deps.Prices.Cache, h.deps.Prices.Prefix, token)
	}
	h.recordSecurityAction(r.Context(), actor, body.Action)
	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"message":  securityMessage(body.Action),
		"affected": result.Affected,
	})
}

func (h *handler) adminSecurityHistory(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.actor(w, r, "Admin"); !ok {
		return
	}
	rows := []auditlog.Entry{}
	var err error
	if h.deps.Audit != nil {
		rows, err = h.deps.Audit.Recent(r.Context(), 50)
		if rows == nil {
			rows = []auditlog.Entry{}
		}
	}
	h.showCabinet(w, map[string]any{"actions": rows}, err)
}

func (h *handler) recordSecurityAction(ctx context.Context, actor int64, action string) {
	if h.deps.Audit == nil {
		return
	}
	var name, email string
	if h.deps.DB.SQL != nil {
		_ = h.deps.DB.SQL.QueryRowContext(ctx, `SELECT COALESCE(name, ''), COALESCE(email, '') FROM users WHERE id = ?`, actor).Scan(&name, &email)
	}
	at := time.Now()
	if h.deps.Location != nil {
		at = at.In(h.deps.Location)
	}
	_ = h.deps.Audit.Append(ctx, auditlog.Entry{
		ID:        strconv.FormatInt(at.UnixNano(), 10),
		UserID:    actor,
		UserName:  name,
		UserEmail: email,
		Action:    action,
		Label:     securityLabel(action),
		CreatedAt: at.Format("2006-01-02 15:04:05"),
	})
}

func securityMessage(action string) string {
	switch action {
	case "stop_traders":
		return "Трафик трейдеров остановлен"
	case "stop_merchants":
		return "Трафик мерчантов остановлен"
	case "stop_users":
		return "Трафик пользователей остановлен"
	case "ban_traders":
		return "Трейдеры заблокированы"
	case "ban_merchants":
		return "Мерчанты заблокированы"
	case "ban_users":
		return "Пользователи заблокированы"
	case "reset_trader_tokens":
		return "Токены трейдеров сброшены"
	case "reset_merchant_tokens":
		return "Токены мерчантов сброшены"
	case "reset_user_tokens":
		return "Токены пользователей сброшены"
	default:
		return "Готово"
	}
}

func securityLabel(action string) string {
	switch action {
	case "stop_traders":
		return "Остановить трафик трейдеров"
	case "stop_merchants":
		return "Остановить трафик мерчантов"
	case "stop_users":
		return "Остановить трафик всех пользователей"
	case "ban_traders":
		return "Заблокировать всех трейдеров"
	case "ban_merchants":
		return "Заблокировать всех мерчантов"
	case "ban_users":
		return "Заблокировать всех пользователей"
	case "reset_trader_tokens":
		return "Сбросить токены трейдеров"
	case "reset_merchant_tokens":
		return "Сбросить токены мерчантов"
	case "reset_user_tokens":
		return "Сбросить токены всех пользователей"
	default:
		return action
	}
}

func (h *handler) adminSmsLogs(w http.ResponseWriter, r *http.Request) {
	h.logList(w, r, "smsLogs", "sms_logs", "id, sender, message, type, created_at")
}

func (h *handler) adminPromos(w http.ResponseWriter, r *http.Request) {
	h.logList(w, r, "promoCodes", "promo_codes", "id, code, max_uses, used_count, is_active")
}

func (h *handler) adminNotifications(w http.ResponseWriter, r *http.Request) {
	h.logList(w, r, "notifications", "notifications", "id, message, recipients_count, delivered_count, created_at")
}

func (h *handler) adminMerchantLogs(w http.ResponseWriter, r *http.Request) {
	h.logList(w, r, "logs", "merchant_api_request_logs", "id, external_id, amount, currency, is_successful, error_message, created_at")
}

func (h *handler) adminCallbackLogs(w http.ResponseWriter, r *http.Request) {
	h.logList(w, r, "logs", "callback_logs", "id, type, url, status_code, is_success, created_at")
}

func (h *handler) adminProviderLogs(w http.ResponseWriter, r *http.Request) {
	h.logList(w, r, "logs", "requisite_provider_logs", "id, provider_name, request_type, success, error_message, created_at")
}

func (h *handler) adminProviderCallbackLogs(w http.ResponseWriter, r *http.Request) {
	h.logList(w, r, "logs", "requisite_provider_callback_logs", "id, provider_name, status_code, is_successful, created_at")
}

func (h *handler) logList(w http.ResponseWriter, r *http.Request, key, table, columns string) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	block, err := reader.Logs(r.Context(), cabinet.FromRequest(r, 50), table, columns)
	h.showCabinet(w, map[string]any{key: block}, err)
}

func (h *handler) adminProviders(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	block, err := reader.Providers(r.Context(), cabinet.FromRequest(r, 15))
	if err != nil {
		h.showCabinet(w, nil, err)
		return
	}
	h.showCabinet(w, map[string]any{
		"providers": block,
		"summary":   map[string]any{"total_providers": block["meta"].(map[string]any)["total"], "active_providers": 0},
		"integrations": []map[string]any{
			{"value": "DEMO_CARD", "label": "DEMO_CARD"},
			{"value": "DEMO_SBP", "label": "DEMO_SBP"},
			{"value": "DEMO_MOBILE", "label": "DEMO_MOBILE"},
		},
	}, nil)
}

func (h *handler) adminTerminals(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	block, err := reader.Terminals(r.Context(), cabinet.FromRequest(r, 15))
	h.showCabinet(w, map[string]any{
		"terminals": block,
		"integrations": []map[string]any{
			{"value": "DEMO_CARD", "label": "DEMO_CARD"},
			{"value": "DEMO_SBP", "label": "DEMO_SBP"},
			{"value": "DEMO_MOBILE", "label": "DEMO_MOBILE"},
		},
	}, err)
}

func (h *handler) adminTerminal(w http.ResponseWriter, r *http.Request) {
	_, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	id, ok := numberParam(r, "id")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	reader := &cabinet.Reader{DB: h.deps.DB.SQL, Location: h.deps.Location, Quotes: h.deps.Quotes, Cascade: h.deps.FeatureCascade}
	card, err := reader.TerminalCard(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	h.showCabinet(w, card, err)
}

func (h *handler) adminRequisiteProviders(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	block, err := reader.Providers(r.Context(), cabinet.Page{Number: 1, Size: 100})
	if err != nil {
		h.showCabinet(w, nil, err)
		return
	}
	rows, _ := block["data"].([]map[string]any)
	h.showCabinet(w, map[string]any{
		"providers": rows,
		"summary": map[string]any{
			"total_providers": len(rows), "active_providers": 0,
			"total_requests_24h": 0, "average_success_rate": 0,
		},
	}, nil)
}

func (h *handler) adminDevices(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Admin")
	if !ok {
		return
	}
	block, err := reader.Devices(r.Context(), cabinet.FromRequest(r, 15), 0)
	h.showCabinet(w, map[string]any{"devices": block}, err)
}

func (h *handler) merchantIntegration(w http.ResponseWriter, r *http.Request) {
	_, id, ok := h.openCabinet(w, r, "Merchant")
	if !ok {
		return
	}
	h.integrationToken(w, r, id)
}

func (h *handler) merchantDashboard(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := h.openCabinet(w, r, "Merchant")
	if !ok {
		return
	}
	h.showCabinet(w, reader.MerchantDashboard(r.Context(), id), nil)
}

func (h *handler) merchantProjects(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := h.openCabinet(w, r, "Merchant")
	if !ok {
		return
	}
	block, err := reader.Merchants(r.Context(), cabinet.FromRequest(r, 15), "", id)
	h.showCabinet(w, block, err)
}

func (h *handler) merchantProject(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := h.openCabinet(w, r, "Merchant")
	if !ok {
		return
	}
	merchantID, ok := numberParam(r, "merchant")
	if !ok {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	card, err := reader.ProjectCard(r.Context(), merchantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeMessage(w, http.StatusNotFound, "Not found.")
		return
	}
	h.showCabinet(w, card, err)
}

func (h *handler) merchantPayments(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := h.openCabinet(w, r, "Merchant")
	if !ok {
		return
	}
	block, err := reader.MerchantOrders(r.Context(), id, cabinet.FromRequest(r, 25), cabinet.Filter(r, "uuid"), cabinet.Filter(r, "externalID"), cabinet.Filter(r, "orderStatuses"))
	h.showCabinet(w, block, err)
}

func (h *handler) merchantDisputes(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := h.openCabinet(w, r, "Merchant")
	if !ok {
		return
	}
	block, err := reader.Disputes(r.Context(), cabinet.FromRequest(r, 15), "", 0, id)
	h.showCabinet(w, block, err)
}

func (h *handler) merchantPayouts(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := h.openCabinet(w, r, "Merchant")
	if !ok {
		return
	}
	block, err := reader.Payouts(r.Context(), cabinet.FromRequest(r, 15), 0, id, "")
	h.showCabinet(w, map[string]any{"payouts": block, "payoutGateways": []any{}}, err)
}

func (h *handler) merchantWallet(w http.ResponseWriter, r *http.Request) {
	h.finance(w, r, "Merchant", "merchant_balance")
}

func (h *handler) merchantCreateData(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Merchant")
	if !ok {
		return
	}
	block, err := reader.Gateways(r.Context(), cabinet.Page{Number: 1, Size: 100})
	h.showCabinet(w, map[string]any{"paymentGateways": block}, err)
}

func (h *handler) merchantRates(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Merchant")
	if !ok {
		return
	}
	h.showCabinet(w, reader.Rates(r.Context()), nil)
}

func (h *handler) traderDashboard(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := h.openCabinet(w, r, "Trader")
	if !ok {
		return
	}
	h.showCabinet(w, reader.TraderDashboard(r.Context(), id), nil)
}

func (h *handler) traderRates(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Trader")
	if !ok {
		return
	}
	h.showCabinet(w, reader.Rates(r.Context()), nil)
}

func (h *handler) traderIntegration(w http.ResponseWriter, r *http.Request) {
	_, id, ok := h.openCabinet(w, r, "Trader")
	if !ok {
		return
	}
	h.integrationToken(w, r, id)
}

func (h *handler) traderSettings(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := h.openCabinet(w, r, "Trader")
	if !ok {
		return
	}
	me, err := reader.Me(r.Context(), id)
	h.showCabinet(w, me, err)
}

func (h *handler) traderWallet(w http.ResponseWriter, r *http.Request) {
	h.finance(w, r, "Trader", "trust_balance")
}

func (h *handler) traderPayouts(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := h.openCabinet(w, r, "Trader")
	if !ok {
		return
	}
	block, err := reader.Payouts(r.Context(), cabinet.FromRequest(r, 15), id, 0, "")
	h.showCabinet(w, block, err)
}

func (h *handler) traderDetails(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := h.openCabinet(w, r, "Trader")
	if !ok {
		return
	}
	block, err := reader.Details(r.Context(), cabinet.FromRequest(r, 15), id)
	h.showCabinet(w, block, err)
}

func (h *handler) traderMethods(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Trader")
	if !ok {
		return
	}
	block, err := reader.Gateways(r.Context(), cabinet.Page{Number: 1, Size: 100})
	h.showCabinet(w, block, err)
}

func (h *handler) traderPayments(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := h.openCabinet(w, r, "Trader")
	if !ok {
		return
	}
	block, err := reader.TraderOrders(r.Context(), id, cabinet.FromRequest(r, 15))
	h.showCabinet(w, block, err)
}

func (h *handler) traderDisputes(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := h.openCabinet(w, r, "Trader")
	if !ok {
		return
	}
	block, err := reader.Disputes(r.Context(), cabinet.FromRequest(r, 15), "", id, 0)
	h.showCabinet(w, block, err)
}

func (h *handler) traderDevices(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := h.openCabinet(w, r, "Trader")
	if !ok {
		return
	}
	block, err := reader.Devices(r.Context(), cabinet.FromRequest(r, 15), id)
	h.showCabinet(w, block, err)
}

func (h *handler) supportMe(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := h.openCabinet(w, r, "Support", "Merchant Support")
	if !ok {
		return
	}
	me, err := reader.Me(r.Context(), id)
	h.showCabinet(w, me, err)
}

func (h *handler) supportIntegration(w http.ResponseWriter, r *http.Request) {
	_, id, ok := h.openCabinet(w, r, "Support", "Merchant Support")
	if !ok {
		return
	}
	h.integrationToken(w, r, id)
}

func (h *handler) supportMenu(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Support", "Merchant Support")
	if !ok {
		return
	}
	h.showCabinet(w, reader.Menu(r.Context()), nil)
}

func (h *handler) supportDashboard(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Support", "Merchant Support")
	if !ok {
		return
	}
	data, err := reader.AdminDashboard(r.Context(), r.URL.Query().Get("period"), queryMerchantID(r))
	h.showCabinet(w, data, err)
}

func (h *handler) supportUsers(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Support", "Merchant Support")
	if !ok {
		return
	}
	block, err := reader.Users(r.Context(), cabinet.FromRequest(r, 15), cabinet.Filter(r, "user"), "", "", "")
	h.showCabinet(w, map[string]any{"users": block}, err)
}

func (h *handler) supportOrders(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Support", "Merchant Support")
	if !ok {
		return
	}
	where, args := cabinet.OrderFilter("", "", "", "", "", 0)
	block, err := reader.Orders(r.Context(), cabinet.FromRequest(r, 15), where, args)
	h.showCabinet(w, map[string]any{"orders": block}, err)
}

func (h *handler) supportDisputes(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Support", "Merchant Support")
	if !ok {
		return
	}
	status := cabinet.Filter(r, "disputeStatuses")
	if status == "" {
		status = cabinet.Filter(r, "status")
	}
	block, err := reader.Disputes(r.Context(), cabinet.FromRequest(r, 15), status, 0, 0)
	h.showCabinet(w, disputePage(block), err)
}

func (h *handler) supportDevices(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Support", "Merchant Support")
	if !ok {
		return
	}
	block, err := reader.Devices(r.Context(), cabinet.FromRequest(r, 15), 0)
	h.showCabinet(w, map[string]any{"devices": block}, err)
}

func (h *handler) leaderMe(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := h.openCabinet(w, r, "Team Leader")
	if !ok {
		return
	}
	me, err := reader.Me(r.Context(), id)
	h.showCabinet(w, me, err)
}

func (h *handler) leaderMenu(w http.ResponseWriter, r *http.Request) {
	reader, _, ok := h.openCabinet(w, r, "Team Leader")
	if !ok {
		return
	}
	h.showCabinet(w, reader.Menu(r.Context()), nil)
}

func (h *handler) leaderDashboard(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := h.openCabinet(w, r, "Team Leader")
	if !ok {
		return
	}
	h.showCabinet(w, reader.LeaderDashboard(r.Context(), id), nil)
}

func (h *handler) leaderFinances(w http.ResponseWriter, r *http.Request) {
	h.finance(w, r, "Team Leader", "teamleader_balance")
}

func (h *handler) leaderReferrals(w http.ResponseWriter, r *http.Request) {
	reader, id, ok := h.openCabinet(w, r, "Team Leader")
	if !ok {
		return
	}
	block, err := reader.Referrals(r.Context(), id, cabinet.FromRequest(r, 15))
	h.showCabinet(w, map[string]any{"referrals": block}, err)
}

func (h *handler) finance(w http.ResponseWriter, r *http.Request, role, column string) {
	reader, id, ok := h.openCabinet(w, r, role)
	if !ok {
		return
	}
	page := cabinet.FromRequest(r, 15)
	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "transactions"
	}
	tx, err := reader.Transactions(r.Context(), page, id)
	if err != nil {
		h.showCabinet(w, nil, err)
		return
	}
	invoices, err := reader.Invoices(r.Context(), page, "deposit", "", "", id)
	h.showCabinet(w, map[string]any{
		"walletStats":  reader.WalletStats(r.Context(), id, column),
		"tabs":         []string{"transactions", "invoices"},
		"currentTab":   tab,
		"transactions": tx,
		"invoices":     invoices,
	}, err)
}
