package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"finance-platform/go/internal/auth"
	"finance-platform/go/internal/impersonate"
	"finance-platform/go/internal/market"
	"finance-platform/go/internal/read"
	"finance-platform/go/internal/requisite"
	"finance-platform/go/internal/store"

	"github.com/go-chi/chi/v5"
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const (
	orderMissingMessage = "No query results for model [App\\Models\\Order]."
	unauthorizedMessage = "This action is unauthorized."
)

// DB is the MySQL pool used by V1 reads.
type DB struct {
	SQL *sql.DB
}

// Prices is the access-token cache. Cascade requisites are JSON in the same Redis.
type Prices struct {
	Cache  auth.Cache
	Prefix string
}

func (h *handler) routes(router chi.Router) {
	router.Get("/up", up)
	router.Get("/ready", ready(h.deps.MySQL, h.deps.Redis, h.deps.PriceRedis))
	h.merchantRoutes(router)
	h.frontendRoutes(router)
	h.integrationRoutes(router)
}

// merchantRoutes is the external merchant contract under /api/v1,
// plus the provider callback and wallet webhooks that complete that contract.
func (h *handler) merchantRoutes(router chi.Router) {
	router.Get("/api/v1/currencies", h.currencies)
	router.Get("/api/v1/payment-gateways", h.paymentGateways)
	router.Get("/api/v1/wallet/balance", h.balance)
	router.Post("/api/v1/wallet/withdraw", h.withdraw)
	router.Get("/api/v1/payout/offers", h.payoutOffers)
	router.Post("/api/v1/payout", h.createPayout)
	router.Get("/api/v1/payout/{uuid}", h.showPayout)
	router.With(h.tokenAuth(h.deps.DepositToken)).Post("/api/deposit/webhook", h.depositWebhook)
	router.With(h.tokenAuth(h.deps.WithdrawToken)).Post("/api/withdraw/webhook", h.withdrawWebhook)
	router.Post("/api/callback/{uuid}", h.providerCallback)
	router.Post("/api/v1/order", h.createOrder)
	router.Get("/api/v1/order/{merchant_id}/{external_id}", h.orderByExternal)
	router.Patch("/api/v1/order/{uuid}/cancel", h.cancelOrder)
	router.Patch("/api/v1/order/{uuid}/finish", h.finishOrder)
	router.Get("/api/v1/order/{uuid}", h.orderByUUID)
	router.Get("/api/v1/dispute/cancel-reasons", h.disputeReasons)
	router.Get("/api/v1/dispute/{uuid}", h.showDispute)
	router.Post("/api/v1/dispute/{uuid}", h.openDispute)
}

// frontendRoutes is the cabinet API used by the admin, merchant, trader, support and leader SPAs.
func (h *handler) frontendRoutes(router chi.Router) {
	router.Post("/api/frontend/admin/login", h.loginAdmin)
	router.Post("/api/frontend/trader/login", h.loginTrader)
	router.Post("/api/frontend/merchant/login", h.loginMerchant)
	router.Post("/api/frontend/support/login", h.loginSupport)
	router.Post("/api/frontend/leader/login", h.loginLeader)
	h.cabinetRoutes(router)
	h.mutateRoutes(router)
	router.Post("/api/frontend/admin/users", h.createUser)
	router.Patch("/api/frontend/admin/users/{id}", h.patchUser)
	router.Post("/api/frontend/admin/users/{id}/wallet/deposit", h.deposit)
	router.Patch("/api/frontend/admin/merchants/{id}/validated", h.validateMerchant)
	router.Post("/api/frontend/admin/market-price", h.marketPrice)
	router.Post("/api/frontend/trader/devices", h.createDevice)
	router.Post("/api/frontend/trader/payment-details", h.createDetail)
	router.Post("/api/frontend/trader/orders/{uuid}/accept", h.traderAccept)
	router.Post("/api/frontend/trader/payouts/{uuid}/finish", h.traderFinishPayout)
	router.Post("/api/frontend/admin/payouts/{id}/finish", h.adminFinishPayout)
	router.Post("/api/frontend/admin/payouts/{id}/cancel", h.adminCancelPayout)
	router.Post("/api/frontend/admin/payouts/{id}/pass-to-trader", h.adminPassPayout)
	router.Post("/api/frontend/merchant/store", h.createMerchantProject)
	router.Patch("/api/frontend/support/orders/{uuid}/accept", h.supportAcceptOrder)
	router.Post("/api/frontend/support/disputes/{uuid}", h.supportOpenDispute)
	router.Patch("/api/frontend/support/disputes/{uuid}/accept", h.supportAcceptDispute)
	router.Patch("/api/frontend/support/disputes/{uuid}/cancel", h.supportCancelDispute)
	router.Patch("/api/frontend/support/disputes/{uuid}/rollback", h.supportRollbackDispute)
	router.Post("/api/frontend/admin/disputes/{uuid}", h.supportOpenDispute)
	router.Patch("/api/frontend/admin/disputes/{uuid}/accept", h.supportAcceptDispute)
	router.Patch("/api/frontend/admin/disputes/{uuid}/cancel", h.supportCancelDispute)
	router.Patch("/api/frontend/admin/disputes/{uuid}/rollback", h.supportRollbackDispute)
}

// integrationRoutes serves the trader device app, the bot and dispute receipt files.
func (h *handler) integrationRoutes(router chi.Router) {
	router.Get("/disputes/{id}/receipt", h.disputeReceipt)
	router.Route("/api/bot", func(bot chi.Router) {
		bot.Use(h.botAuth)
		bot.Get("/order/{merchant_id}/{external_id}", h.botOrderExternal)
		bot.Get("/order/{uuid}", h.botOrder)
		bot.Post("/order/{uuid}/dispute/accept", h.botAcceptDispute)
		bot.Post("/order/{uuid}/dispute/cancel", h.botCancelDispute)
		bot.Post("/order/{uuid}/dispute", h.botOpenDispute)
		bot.Get("/dispute/cancel-reasons", h.botReasons)
		bot.Post("/user/register", h.botRegister)
		bot.Post("/user/add-balance", h.botAddBalance)
	})
	router.Post("/api/app/device/connect", h.connectDevice)
	router.Post("/api/app/sms", h.sms)
}

func (h *handler) paymentGateways(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.user(w, r); !ok {
		return
	}
	if h.deps.DB.SQL == nil || h.deps.Quotes == nil {
		writeFail(w, http.StatusInternalServerError, "catalog unavailable")
		return
	}
	filter := transgranFilter(r)
	rows, err := store.ListGateways(r.Context(), h.deps.DB.SQL, filter)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "catalog unavailable")
		return
	}
	views := make([]read.GatewayView, 0, len(rows))
	for _, row := range rows {
		units, err := market.BuyUnits(r.Context(), h.deps.Quotes, strings.ToLower(row.Currency), "bybit")
		if err != nil {
			writeFail(w, http.StatusInternalServerError, "catalog unavailable")
			return
		}
		view, err := read.PresentGateway(row, units)
		if err != nil {
			writeFail(w, http.StatusInternalServerError, "catalog unavailable")
			return
		}
		views = append(views, view)
	}
	writeSuccess(w, http.StatusOK, views)
}

func (h *handler) balance(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	if h.deps.DB.SQL == nil {
		writeFail(w, http.StatusInternalServerError, "wallet unavailable")
		return
	}
	units, err := store.MerchantBalanceUnits(r.Context(), h.deps.DB.SQL, userID)
	if errors.Is(err, store.ErrAbsent) {
		writeMessage(w, http.StatusNotFound, "No query results for model [App\\Models\\Wallet].")
		return
	}
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "wallet unavailable")
		return
	}
	view, err := read.PresentBalance(units)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "wallet unavailable")
		return
	}
	writeSuccess(w, http.StatusOK, view)
}

func (h *handler) orderByUUID(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	uuid := chi.URLParam(r, "uuid")
	if !uuidPattern.MatchString(uuid) {
		writeMessage(w, http.StatusNotFound, orderMissingMessage)
		return
	}
	h.showOrder(w, r, userID, func(ctx context.Context) (read.Order, error) {
		return store.FindOrderByUUID(ctx, h.deps.DB.SQL, uuid)
	})
}

func (h *handler) orderByExternal(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	merchantID := chi.URLParam(r, "merchant_id")
	externalID := chi.URLParam(r, "external_id")
	h.showOrder(w, r, userID, func(ctx context.Context) (read.Order, error) {
		return store.FindOrderByExternal(ctx, h.deps.DB.SQL, merchantID, externalID)
	})
}

func (h *handler) showOrder(w http.ResponseWriter, r *http.Request, userID int64, load func(context.Context) (read.Order, error)) {
	if h.deps.DB.SQL == nil {
		writeFail(w, http.StatusInternalServerError, "order unavailable")
		return
	}
	order, err := load(r.Context())
	if errors.Is(err, store.ErrAbsent) {
		writeMessage(w, http.StatusNotFound, orderMissingMessage)
		return
	}
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "order unavailable")
		return
	}
	super, err := store.HasRole(r.Context(), h.deps.DB.SQL, userID, "Admin")
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "order unavailable")
		return
	}
	if !read.CanSeeOrder(userID, order, super) {
		writeMessage(w, http.StatusForbidden, unauthorizedMessage)
		return
	}
	var external *read.External
	if h.deps.Prices.Cache != nil {
		external, err = requisite.Load(r.Context(), h.deps.Prices.Cache, h.deps.Prices.Prefix, order.ID)
		if err != nil {
			writeFail(w, http.StatusInternalServerError, "order unavailable")
			return
		}
	}
	view, err := read.PresentOrder(order, external, h.now())
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "order unavailable")
		return
	}
	writeSuccess(w, http.StatusOK, view)
}

func (h *handler) user(w http.ResponseWriter, r *http.Request) (int64, bool) {
	token := r.Header.Get(accessTokenHeader)
	if h.deps.Sessions != nil {
		decision, sessionErr := h.deps.Sessions.Lookup(r.Context(), token, r.URL.Path)
		if sessionErr != nil {
			writeFail(w, http.StatusInternalServerError, "auth unavailable")
			return 0, false
		}
		if decision.Found {
			if !decision.Allowed {
				writeFail(w, http.StatusBadRequest, invalidAccessToken)
				return 0, false
			}
			*r = *r.WithContext(impersonate.WithActive(r.Context(), impersonate.Active{ActorID: decision.ActorID, TargetID: decision.TargetID}))
			return decision.TargetID, true
		}
	}
	if h.deps.Tokens == nil {
		writeFail(w, http.StatusInternalServerError, "auth unavailable")
		return 0, false
	}
	id, ok, err := h.deps.Tokens.Resolve(r.Context(), token)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "auth unavailable")
		return 0, false
	}
	if !ok {
		writeFail(w, http.StatusBadRequest, invalidAccessToken)
		return 0, false
	}
	return id, true
}

func (h *handler) now() time.Time {
	if h.deps.Now == nil {
		return time.Now()
	}
	return h.deps.Now()
}

func transgranFilter(r *http.Request) *bool {
	raw := r.URL.Query().Get("transgran")
	if _, present := r.URL.Query()["transgran"]; !present || raw == "" {
		return nil
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		value := true
		return &value
	case "0", "false", "no", "off":
		value := false
		return &value
	default:
		return nil
	}
}
