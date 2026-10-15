package httpapi

import (
	"context"
	"net/http"
	"time"

	"finance-platform/go/internal/auditlog"
	"finance-platform/go/internal/auth"
	"finance-platform/go/internal/flow"
	"finance-platform/go/internal/impersonate"
	"finance-platform/go/internal/market"
	"finance-platform/go/internal/money"

	"github.com/go-chi/chi/v5"
)

const accessTokenHeader = "Access-Token"

const invalidAccessToken = "Invalid Access Token."

// Pinger is MySQL or Redis readiness.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps are the process dependencies the HTTP server is allowed to touch.
type Deps struct {
	Tokens                 auth.Resolver
	MySQL                  Pinger
	Redis                  Pinger
	PriceRedis             Pinger
	DB                     DB
	Prices                 Prices
	Quotes                 market.Book
	Now                    func() time.Time
	Location               *time.Location
	AppEnv                 string
	MaxWait                time.Duration
	Poll                   time.Duration
	Gate                   *flow.Gate
	Holds                  flow.Hold
	BotToken               string
	DepositToken           string
	WithdrawToken          string
	LocalWithdraw          bool
	ReceiptsDir            string
	FeatureCascade         bool
	CascadePaymentDetailID string
	Sessions               *impersonate.Service
	Audit                  auditlog.Log
}

type handler struct {
	deps Deps
}

// NewHandler routes liveness, readiness and the V1 read API.
func NewHandler(deps Deps) http.Handler {
	h := &handler{deps: deps}
	router := chi.NewRouter()
	h.routes(router)
	return withCORS(router)
}

func up(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func ready(mysql, redis, prices Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := ping(ctx, mysql); err != nil {
			writeFail(w, http.StatusServiceUnavailable, "mysql unavailable")
			return
		}
		if err := ping(ctx, redis); err != nil {
			writeFail(w, http.StatusServiceUnavailable, "redis unavailable")
			return
		}
		if err := ping(ctx, prices); err != nil {
			writeFail(w, http.StatusServiceUnavailable, "prices unavailable")
			return
		}
		writeSuccess(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

func ping(ctx context.Context, pinger Pinger) error {
	if pinger == nil {
		return errNilPinger
	}
	return pinger.Ping(ctx)
}

type currencyView struct {
	Currency  string `json:"currency"`
	Precision int    `json:"precision"`
	Symbol    string `json:"symbol"`
	Name      string `json:"name"`
}

func (h *handler) currencies(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.user(w, r); !ok {
		return
	}
	all := money.All()
	views := make([]currencyView, 0, len(all))
	for _, item := range all {
		views = append(views, currencyView{
			Currency:  item.Code(),
			Precision: item.Precision(),
			Symbol:    item.Symbol(),
			Name:      item.Name(),
		})
	}
	writeSuccess(w, http.StatusOK, views)
}
