package flow

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"finance-platform/go/internal/event"
	"finance-platform/go/internal/market"
	"finance-platform/go/internal/money"
	"finance-platform/go/internal/outbox"

	"github.com/shopspring/decimal"
)

// PayoutView is the merchant payout card.
type PayoutView struct {
	UUID                       string  `json:"uuid"`
	ExternalID                 string  `json:"external_id"`
	Detail                     string  `json:"detail"`
	DetailType                 string  `json:"detail_type"`
	DetailInitials             string  `json:"detail_initials"`
	PayoutAmount               string  `json:"payout_amount"`
	Currency                   string  `json:"currency"`
	BaseLiquidityAmount        string  `json:"base_liquidity_amount"`
	LiquidityAmount            string  `json:"liquidity_amount"`
	LiquidityCurrency          string  `json:"liquidity_currency"`
	ServiceCommissionRate      float64 `json:"service_commission_rate"`
	ServiceCommissionAmount    string  `json:"service_commission_amount"`
	TraderProfitAmount         string  `json:"trader_profit_amount"`
	TraderExchangeMarkupRate   float64 `json:"trader_exchange_markup_rate"`
	TraderExchangeMarkupAmount string  `json:"trader_exchange_markup_amount"`
	BaseExchangePrice          string  `json:"base_exchange_price"`
	ExchangePrice              string  `json:"exchange_price"`
	Status                     string  `json:"status"`
	SubStatus                  string  `json:"sub_status"`
	CallbackURL                *string `json:"callback_url"`
	PaymentGateway             string  `json:"payment_gateway"`
	PaymentGatewayName         string  `json:"payment_gateway_name"`
	SubPaymentGateway          *string `json:"sub_payment_gateway"`
	SubPaymentGatewayName      *string `json:"sub_payment_gateway_name"`
	FinishedAt                 *int64  `json:"finished_at"`
	ExpiresAt                  int64   `json:"expires_at"`
	CreatedAt                  int64   `json:"created_at"`
}

// PayoutInput is a merchant payout request.
type PayoutInput struct {
	OwnerID           int64
	PayoutGatewayUUID string
	ExternalID        string
	Detail            string
	DetailType        string
	DetailInitials    string
	Amount            string
	PaymentGateway    string
	SubPaymentGateway string
	CallbackURL       string
}

// PayoutOffers is the public menu of free payout offers.
func (s *Service) PayoutOffers(ctx context.Context) (any, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT po.max_amount, po.min_amount, po.currency, po.detail_types, pg.code, pg.name
		FROM payout_offers po
		INNER JOIN users u ON u.id = po.owner_id
		INNER JOIN payment_gateways pg ON pg.id = po.payment_gateway_id
		WHERE u.is_payout_online = 1 AND po.occupied = 0 AND po.active = 1
		ORDER BY po.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type item struct {
		max      []int
		min      []int
		currency string
		detail   string
		code     string
		name     string
	}
	grouped := map[string]*item{}
	var order []string
	for rows.Next() {
		var maxUnits, minUnits, currency, rawTypes, code, name string
		if err := rows.Scan(&maxUnits, &minUnits, &currency, &rawTypes, &code, &name); err != nil {
			return nil, err
		}
		maxBeauty, err := beautyOf(maxUnits, currency)
		if err != nil {
			return nil, err
		}
		minBeauty, err := beautyOf(minUnits, currency)
		if err != nil {
			return nil, err
		}
		maxN, _ := strconv.Atoi(strings.Split(maxBeauty, ".")[0])
		minN, _ := strconv.Atoi(strings.Split(minBeauty, ".")[0])
		current := grouped[code]
		if current == nil {
			current = &item{currency: strings.ToLower(currency), detail: firstDetail(rawTypes), code: code, name: name}
			grouped[code] = current
			order = append(order, code)
		}
		current.max = append(current.max, maxN)
		current.min = append(current.min, minN)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(order) == 0 {
		return []any{}, nil
	}
	byCurrency := map[string]map[string]any{}
	for _, code := range order {
		item := grouped[code]
		maxAmount, minAmount := item.max[0], item.min[0]
		for _, value := range item.max {
			if value > maxAmount {
				maxAmount = value
			}
		}
		for _, value := range item.min {
			if value < minAmount {
				minAmount = value
			}
		}
		recMax, recMin := recommend(item.max), recommend(item.min)
		bucket := byCurrency[item.currency]
		if bucket == nil {
			bucket = map[string]any{}
			byCurrency[item.currency] = bucket
		}
		bucket[code] = map[string]any{
			"max_amount":             maxAmount,
			"min_amount":             minAmount,
			"currency":               item.currency,
			"detail_type":            item.detail,
			"payment_gateway":        map[string]any{"name": item.name, "code": item.code},
			"offers_count":           len(item.max),
			"recommended_max_amount": recMax,
			"recommended_min_amount": recMin,
		}
	}
	return byCurrency, nil
}

// CreatePayout reserves merchant funds against a free offer.
func (s *Service) CreatePayout(ctx context.Context, in PayoutInput) (PayoutView, error) {
	if err := s.requirePayouts(ctx, in.OwnerID); err != nil {
		return PayoutView{}, err
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		gateway, err := s.lockPayoutGateway(ctx, tx, in)
		if err != nil {
			return err
		}
		amount, err := money.FromPrecision(strings.TrimSpace(in.Amount), gateway.Currency)
		if err != nil {
			return invalid(map[string][]string{"amount": {"The amount field must be an integer."}})
		}
		sellUnits, err := market.SellUnits(ctx, s.Quotes, gateway.Currency, "bybit")
		if err != nil {
			return err
		}
		if sellUnits == "" || sellUnits == "0" {
			return fail(400, "Курс не найден.")
		}
		basePrice, err := money.FromUnits(sellUnits, gateway.Currency)
		if err != nil {
			return err
		}
		markupRate := decimal.RequireFromString(gateway.MarkupRate).Div(decimal.NewFromInt(100))
		serviceRate := decimal.RequireFromString(gateway.ServiceRate).Div(decimal.NewFromInt(100))
		markupPart, err := basePrice.Mul(markupRate.String())
		if err != nil {
			return err
		}
		exchange, err := basePrice.SubMoney(markupPart)
		if err != nil {
			return err
		}
		baseLiquidity, err := amount.Convert(exchange, "usdt")
		if err != nil {
			return err
		}
		serviceAmount, err := baseLiquidity.Mul(serviceRate.String())
		if err != nil {
			return err
		}
		atBase, err := amount.Convert(basePrice, "usdt")
		if err != nil {
			return err
		}
		markupAmount, err := atBase.SubMoney(baseLiquidity)
		if err != nil {
			return err
		}
		markupAmount, err = markupAmount.Abs()
		if err != nil {
			return err
		}
		liquidity, err := baseLiquidity.AddMoney(serviceAmount)
		if err != nil {
			return err
		}
		ownerWallet, err := lockWallet(ctx, tx, gateway.OwnerID)
		if err != nil {
			return err
		}
		have, err := money.FromUnits(zero(ownerWallet.Merchant), "usdt")
		if err != nil {
			return err
		}
		enough, err := have.GreaterOrEqual(liquidity)
		if err != nil {
			return err
		}
		if !enough {
			return fail(400, "Не достаточно средств на балансе для создания выплаты.")
		}
		offer, err := s.pickOffer(ctx, tx, amount, in.DetailType, gateway.PaymentID, gateway.Currency)
		if err != nil {
			return err
		}
		traderWallet, err := lockWallet(ctx, tx, offer.OwnerID)
		if err != nil {
			return err
		}
		var adminWallet int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM wallets WHERE user_id = 1`).Scan(&adminWallet); err != nil {
			return err
		}
		uuid, err := newUUID()
		if err != nil {
			return err
		}
		expires := s.now().Add(time.Duration(gateway.ReserveMin) * time.Minute)
		res, err := tx.ExecContext(ctx, `
			INSERT INTO payouts (
				uuid, external_id, detail, detail_type, detail_initials,
				payout_amount, currency, base_liquidity_amount, liquidity_amount,
				service_commission_rate, service_commission_amount, trader_profit_amount,
				trader_exchange_markup_rate, trader_exchange_markup_amount,
				base_exchange_price, exchange_price, status, sub_status, callback_url,
				payout_offer_id, payout_gateway_id, payment_gateway_id, sub_payment_gateway_id,
				trader_id, owner_id, expires_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 'processing_by_trader', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid, in.ExternalID, in.Detail, in.DetailType, in.DetailInitials,
			amount.Units(), gateway.Currency, baseLiquidity.Units(), liquidity.Units(),
			gateway.ServiceRate, serviceAmount.Units(), baseLiquidity.Units(),
			gateway.MarkupRate, markupAmount.Units(), basePrice.Units(), exchange.Units(),
			emptyAsNil(in.CallbackURL), offer.ID, gateway.ID, gateway.PaymentID, emptyInt(gateway.SubID),
			offer.OwnerID, gateway.OwnerID, expires, s.now(), s.now())
		if err != nil {
			return err
		}
		payoutID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if err := s.openHold(ctx, tx, baseLiquidity.Units(), ownerWallet.ID, "merchant", traderWallet.ID, "trust", payoutID, nil); err != nil {
			return err
		}
		if err := s.openHold(ctx, tx, serviceAmount.Units(), ownerWallet.ID, "merchant", adminWallet, "commission", payoutID, nil); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE payout_offers SET occupied = 1, updated_at = ? WHERE owner_id = ?`, s.now(), offer.OwnerID); err != nil {
			return err
		}
		return s.enqueuePayout(ctx, tx, payoutID)
	})
	if err != nil {
		return PayoutView{}, err
	}
	return s.FindPayout(ctx, in.OwnerID, "", in.ExternalID, in.PayoutGatewayUUID)
}

// FindPayout loads a payout the caller is allowed to see.
func (s *Service) FindPayout(ctx context.Context, actor int64, uuid, externalID, gatewayUUID string) (PayoutView, error) {
	view, ownerID, traderID, err := s.loadPayout(ctx, uuid, externalID, gatewayUUID)
	if err != nil {
		return PayoutView{}, err
	}
	if err := s.canSeePayout(ctx, actor, ownerID, traderID); err != nil {
		return PayoutView{}, err
	}
	return view, nil
}

// FinishPayoutByTrader starts the hold timer and releases it when the timer is already due.
func (s *Service) FinishPayoutByTrader(ctx context.Context, actor int64, uuid string) (PayoutView, error) {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		row, err := s.lockPayout(ctx, tx, uuid)
		if err != nil {
			return err
		}
		if row.TraderID != actor {
			return plain(403, "This action is unauthorized.")
		}
		if row.Status != "pending" {
			return fail(400, "Действие не возможно для текущего статуса.")
		}
		minutes, err := s.holdMinutes(ctx, tx)
		if err != nil {
			return err
		}
		until := s.now().Add(time.Duration(minutes) * time.Minute)
		if minutes <= 0 {
			until = s.now().Add(-time.Second)
		}
		if _, err = tx.ExecContext(ctx, `
			UPDATE users SET is_payout_online = 0, updated_at = ? WHERE id = ?`, s.now(), actor); err != nil {
			return err
		}
		if row.OfferID.Valid {
			var owner int64
			if err = tx.QueryRowContext(ctx, `SELECT owner_id FROM payout_offers WHERE id = ?`, row.OfferID.Int64).Scan(&owner); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE payout_offers SET occupied = 0, updated_at = ? WHERE owner_id = ?`, s.now(), owner); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `
			UPDATE payouts SET status = 'success', sub_status = 'fully_completed', finished_at = ?, updated_at = ? WHERE id = ?`,
			s.now(), s.now(), row.ID); err != nil {
			return err
		}
		if err = s.armHolds(ctx, tx, row.ID, until); err != nil {
			return err
		}
		return s.enqueuePayout(ctx, tx, row.ID)
	})
	if err != nil {
		return PayoutView{}, err
	}
	return s.FindPayout(ctx, actor, uuid, "", "")
}

// FinishPayoutByAdmin pays the holds immediately to the acting admin.
func (s *Service) FinishPayoutByAdmin(ctx context.Context, actor int64, id int64) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		var payoutID int64
		var status string
		err := tx.QueryRowContext(ctx, `SELECT id, status FROM payouts WHERE id = ? FOR UPDATE`, id).Scan(&payoutID, &status)
		if err == sql.ErrNoRows {
			return plain(404, "No query results for model [App\\Models\\Payout].")
		}
		if err != nil {
			return err
		}
		admin, err := lockWallet(ctx, tx, actor)
		if err != nil {
			return err
		}
		holds, err := s.payoutHolds(ctx, tx, payoutID)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `
			UPDATE payouts SET status = 'success', sub_status = 'fully_completed', finished_at = ?, updated_at = ? WHERE id = ?`,
			s.now(), s.now(), payoutID); err != nil {
			return err
		}
		for i, hold := range holds {
			if i == 0 {
				if _, err = tx.ExecContext(ctx, `
					UPDATE funds_on_holds
					SET destination_wallet_id = ?, destination_wallet_balance_type = 'trust', updated_at = ?
					WHERE id = ? AND status = 'timer_not_set'`, admin.ID, s.now(), hold); err != nil {
					return err
				}
			}
			if _, err = tx.ExecContext(ctx, `
				UPDATE funds_on_holds SET hold_until = ?, status = 'pending_for_execution', updated_at = ? WHERE id = ?`,
				s.now().Add(-time.Second), s.now(), hold); err != nil {
				return err
			}
			if err = s.executeHold(ctx, tx, hold); err != nil {
				return err
			}
		}
		return s.enqueuePayout(ctx, tx, payoutID)
	})
}

// CancelPayout returns the held funds to the merchant once.
func (s *Service) CancelPayout(ctx context.Context, actor int64, id int64, reason string) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	if reason != "" && (len([]rune(reason)) < 10 || len([]rune(reason)) > 1000) {
		return invalid(map[string][]string{"reason": {"The reason field must be at least 10 characters."}})
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		var payoutID int64
		var status string
		err := tx.QueryRowContext(ctx, `SELECT id, status FROM payouts WHERE id = ? FOR UPDATE`, id).Scan(&payoutID, &status)
		if err == sql.ErrNoRows {
			return plain(404, "No query results for model [App\\Models\\Payout].")
		}
		if err != nil {
			return err
		}
		if status != "pending" {
			return fail(400, "Действие не возможно для текущего статуса.")
		}
		if _, err = tx.ExecContext(ctx, `
			UPDATE payouts
			SET status = 'fail', sub_status = 'fully_completed', cancel_reason = ?, finished_at = ?, updated_at = ?
			WHERE id = ?`, emptyAsNil(reason), s.now(), s.now(), payoutID); err != nil {
			return err
		}
		holds, err := s.payoutHolds(ctx, tx, payoutID)
		if err != nil {
			return err
		}
		for _, hold := range holds {
			if err := s.cancelHold(ctx, tx, hold); err != nil {
				return err
			}
		}
		return s.enqueuePayout(ctx, tx, payoutID)
	})
}

// PassPayout assigns another free trader and points the liquidity hold at that trader.
func (s *Service) PassPayout(ctx context.Context, actor int64, id int64) error {
	if err := s.requireRole(ctx, actor, "Admin"); err != nil {
		return err
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		row, err := s.lockPayoutID(ctx, tx, id)
		if err != nil {
			return err
		}
		amount, err := money.FromUnits(row.Amount, row.Currency)
		if err != nil {
			return err
		}
		offer, err := s.pickOfferExcept(ctx, tx, amount, row.DetailType, row.PaymentID, row.Currency, row.TraderID)
		if err != nil {
			return err
		}
		wallet, err := lockWallet(ctx, tx, offer.OwnerID)
		if err != nil {
			return err
		}
		expires := s.now().Add(time.Duration(row.ReserveMin) * time.Minute)
		if _, err = tx.ExecContext(ctx, `
			UPDATE payouts
			SET sub_status = 'processing_by_trader', trader_id = ?, payout_offer_id = ?,
			    refuse_reason = NULL, previous_trader_id = NULL, expires_at = ?, updated_at = ?
			WHERE id = ?`, offer.OwnerID, offer.ID, expires, s.now(), row.ID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE payout_offers SET occupied = 1, updated_at = ? WHERE owner_id = ?`, s.now(), offer.OwnerID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE funds_on_holds
			SET destination_wallet_id = ?, destination_wallet_balance_type = 'trust', updated_at = ?
			WHERE holdable_id = ? AND holdable_type = ? AND destination_wallet_balance_type = 'trust' AND status = 'timer_not_set'`,
			wallet.ID, s.now(), row.ID, holdablePayout)
		return err
	})
}

func (s *Service) armHolds(ctx context.Context, tx *sql.Tx, payoutID int64, until time.Time) error {
	holds, err := s.payoutHolds(ctx, tx, payoutID)
	if err != nil {
		return err
	}
	for _, id := range holds {
		if _, err = tx.ExecContext(ctx, `
			UPDATE funds_on_holds SET hold_until = ?, status = 'pending_for_execution', updated_at = ? WHERE id = ?`,
			until, s.now(), id); err != nil {
			return err
		}
		if !until.After(s.now()) {
			if err = s.executeHold(ctx, tx, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) payoutHolds(ctx context.Context, tx *sql.Tx, payoutID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM funds_on_holds
		WHERE holdable_id = ? AND holdable_type = ?
		ORDER BY id FOR UPDATE`, payoutID, holdablePayout)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Service) holdMinutes(ctx context.Context, tx *sql.Tx) (int, error) {
	var raw sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE `key` = 'funds_on_hold_time'").Scan(&raw)
	if err == sql.ErrNoRows || !raw.Valid {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	minutes, err := strconv.Atoi(strings.TrimSpace(raw.String))
	if err != nil {
		return 0, nil
	}
	return minutes, nil
}

type offerRow struct {
	ID      int64
	OwnerID int64
}

type payoutGateway struct {
	ID          int64
	OwnerID     int64
	Enabled     bool
	Currency    string
	PaymentID   int64
	SubID       int64
	ServiceRate string
	MarkupRate  string
	ReserveMin  int
	DetailTypes string
}

func (s *Service) lockPayoutGateway(ctx context.Context, tx *sql.Tx, in PayoutInput) (payoutGateway, error) {
	var row payoutGateway
	var enabled sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT id, owner_id, enabled FROM payout_gateways WHERE uuid = ? FOR UPDATE`, in.PayoutGatewayUUID).Scan(
		&row.ID, &row.OwnerID, &enabled)
	if err == sql.ErrNoRows {
		return row, invalid(map[string][]string{"payout_gateway_id": {"The selected payout gateway id is invalid."}})
	}
	if err != nil {
		return row, err
	}
	row.Enabled = enabled.Valid && enabled.Int64 == 1
	err = tx.QueryRowContext(ctx, `
		SELECT id, LOWER(currency),
		       CAST(total_service_commission_rate_for_payouts AS CHAR),
		       CAST(trader_commission_rate_for_payouts AS CHAR),
		       reservation_time_for_payouts, detail_types
		FROM payment_gateways WHERE code = ?`, in.PaymentGateway).Scan(
		&row.PaymentID, &row.Currency, &row.ServiceRate, &row.MarkupRate, &row.ReserveMin, &row.DetailTypes)
	if err == sql.ErrNoRows {
		return row, invalid(map[string][]string{"payment_gateway": {"The selected payment gateway is invalid."}})
	}
	if err != nil {
		return row, err
	}
	if in.SubPaymentGateway != "" {
		err = tx.QueryRowContext(ctx, `SELECT id FROM payment_gateways WHERE code = ?`, in.SubPaymentGateway).Scan(&row.SubID)
		if err == sql.ErrNoRows {
			return row, invalid(map[string][]string{"sub_payment_gateway": {"The selected sub payment gateway is invalid."}})
		}
		if err != nil {
			return row, err
		}
	}
	if row.OwnerID != in.OwnerID {
		admin, err := hasRole(ctx, s.DB, in.OwnerID, "Admin")
		if err != nil {
			return row, err
		}
		if !admin {
			return row, plain(403, "This action is unauthorized.")
		}
	}
	if !row.Enabled {
		return row, fail(400, "Направление выключено.")
	}
	if !detailAllowed(row.DetailTypes, in.DetailType) {
		return row, invalid(map[string][]string{"detail_type": {"The selected detail type is invalid."}})
	}
	var dup int64
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM payouts WHERE external_id = ? AND payout_gateway_id = ? LIMIT 1`, in.ExternalID, row.ID).Scan(&dup)
	if err == nil {
		return row, invalid(map[string][]string{"external_id": {"The external id has already been taken."}})
	}
	if err != sql.ErrNoRows {
		return row, err
	}
	return row, nil
}

func (s *Service) pickOffer(ctx context.Context, tx *sql.Tx, amount money.Money, detailType string, gatewayID int64, currency string) (offerRow, error) {
	return s.pickOfferExcept(ctx, tx, amount, detailType, gatewayID, currency, 0)
}

func (s *Service) pickOfferExcept(ctx context.Context, tx *sql.Tx, amount money.Money, detailType string, gatewayID int64, currency string, except int64) (offerRow, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT po.id, po.owner_id, po.min_amount, po.max_amount, po.currency, po.detail_types, po.payment_gateway_id
		FROM payout_offers po
		INNER JOIN users u ON u.id = po.owner_id
		WHERE u.is_payout_online = 1 AND po.occupied = 0 AND po.active = 1 AND po.owner_id <> ?
		ORDER BY po.id
		FOR UPDATE`, except)
	if err != nil {
		return offerRow{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			row      offerRow
			minUnits string
			maxUnits string
			cur      string
			types    string
			gw       int64
		)
		if err := rows.Scan(&row.ID, &row.OwnerID, &minUnits, &maxUnits, &cur, &types, &gw); err != nil {
			return offerRow{}, err
		}
		if gw != gatewayID || !strings.EqualFold(cur, currency) || firstDetail(types) != detailType {
			continue
		}
		minAmount, err := money.FromUnits(minUnits, currency)
		if err != nil {
			return offerRow{}, err
		}
		maxAmount, err := money.FromUnits(maxUnits, currency)
		if err != nil {
			return offerRow{}, err
		}
		low, err := minAmount.LessOrEqual(amount)
		if err != nil {
			return offerRow{}, err
		}
		high, err := maxAmount.GreaterOrEqual(amount)
		if err != nil {
			return offerRow{}, err
		}
		if low && high {
			return row, nil
		}
	}
	if err := rows.Err(); err != nil {
		return offerRow{}, err
	}
	if except == 0 {
		return offerRow{}, fail(400, "Подходящие предложение выплаты не найдено. Попробуйте изменить параметры.")
	}
	return offerRow{}, fail(400, "Подходящий трейдер не найден.")
}

type payoutLock struct {
	ID         int64
	UUID       string
	Status     string
	TraderID   int64
	OwnerID    int64
	OfferID    sql.NullInt64
	Amount     string
	Currency   string
	DetailType string
	PaymentID  int64
	ReserveMin int
}

func (s *Service) lockPayout(ctx context.Context, tx *sql.Tx, uuid string) (payoutLock, error) {
	var row payoutLock
	err := tx.QueryRowContext(ctx, `
		SELECT p.id, p.uuid, p.status, p.trader_id, p.owner_id, p.payout_offer_id,
		       p.payout_amount, p.currency, p.detail_type, p.payment_gateway_id, pg.reservation_time_for_payouts
		FROM payouts p
		INNER JOIN payment_gateways pg ON pg.id = p.payment_gateway_id
		WHERE p.uuid = ? FOR UPDATE`, uuid).Scan(
		&row.ID, &row.UUID, &row.Status, &row.TraderID, &row.OwnerID, &row.OfferID,
		&row.Amount, &row.Currency, &row.DetailType, &row.PaymentID, &row.ReserveMin)
	if err == sql.ErrNoRows {
		return row, plain(404, "No query results for model [App\\Models\\Payout].")
	}
	return row, err
}

func (s *Service) lockPayoutID(ctx context.Context, tx *sql.Tx, id int64) (payoutLock, error) {
	var row payoutLock
	err := tx.QueryRowContext(ctx, `
		SELECT p.id, p.uuid, p.status, p.trader_id, p.owner_id, p.payout_offer_id,
		       p.payout_amount, p.currency, p.detail_type, p.payment_gateway_id, pg.reservation_time_for_payouts
		FROM payouts p
		INNER JOIN payment_gateways pg ON pg.id = p.payment_gateway_id
		WHERE p.id = ? FOR UPDATE`, id).Scan(
		&row.ID, &row.UUID, &row.Status, &row.TraderID, &row.OwnerID, &row.OfferID,
		&row.Amount, &row.Currency, &row.DetailType, &row.PaymentID, &row.ReserveMin)
	if err == sql.ErrNoRows {
		return row, plain(404, "No query results for model [App\\Models\\Payout].")
	}
	return row, err
}

type rowQuery interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Service) loadPayout(ctx context.Context, uuid, externalID, gatewayUUID string) (PayoutView, int64, int64, error) {
	return loadPayoutRow(ctx, s.DB, uuid, externalID, gatewayUUID)
}

func loadPayoutRow(ctx context.Context, db rowQuery, uuid, externalID, gatewayUUID string) (PayoutView, int64, int64, error) {
	query := `
		SELECT p.uuid, p.external_id, p.detail, p.detail_type, p.detail_initials,
		       p.payout_amount, p.currency, p.base_liquidity_amount, p.liquidity_amount,
		       p.service_commission_rate, p.service_commission_amount, p.trader_profit_amount,
		       p.trader_exchange_markup_rate, p.trader_exchange_markup_amount,
		       p.base_exchange_price, p.exchange_price, p.status, p.sub_status, p.callback_url,
		       pg.code, pg.name, sub.code, sub.name, p.finished_at, p.expires_at, p.created_at,
		       p.owner_id, p.trader_id
		FROM payouts p
		INNER JOIN payment_gateways pg ON pg.id = p.payment_gateway_id
		LEFT JOIN payment_gateways sub ON sub.id = p.sub_payment_gateway_id
		INNER JOIN payout_gateways g ON g.id = p.payout_gateway_id`
	var args []any
	switch {
	case uuid != "":
		query += ` WHERE p.uuid = ?`
		args = append(args, uuid)
	default:
		query += ` WHERE p.external_id = ? AND g.uuid = ?`
		args = append(args, externalID, gatewayUUID)
	}
	var (
		view                           PayoutView
		amount, currency               string
		baseLiq, liq, service, profit  string
		markupAmount, basePrice, price string
		callback                       sql.NullString
		subCode, subName               sql.NullString
		finished, expires, created     sql.NullTime
		ownerID, traderID              int64
		serviceRate, markupRate        float64
	)
	err := db.QueryRowContext(ctx, query, args...).Scan(
		&view.UUID, &view.ExternalID, &view.Detail, &view.DetailType, &view.DetailInitials,
		&amount, &currency, &baseLiq, &liq, &serviceRate, &service, &profit, &markupRate, &markupAmount,
		&basePrice, &price, &view.Status, &view.SubStatus, &callback,
		&view.PaymentGateway, &view.PaymentGatewayName, &subCode, &subName,
		&finished, &expires, &created, &ownerID, &traderID)
	if err == sql.ErrNoRows {
		return PayoutView{}, 0, 0, plain(404, "No query results for model [App\\Models\\Payout].")
	}
	if err != nil {
		return PayoutView{}, 0, 0, err
	}
	view.Currency = strings.ToLower(currency)
	view.PayoutAmount, err = beautyOf(amount, currency)
	if err != nil {
		return PayoutView{}, 0, 0, err
	}
	view.BaseLiquidityAmount, err = beautyOf(baseLiq, "usdt")
	if err != nil {
		return PayoutView{}, 0, 0, err
	}
	view.LiquidityAmount, err = beautyOf(liq, "usdt")
	if err != nil {
		return PayoutView{}, 0, 0, err
	}
	view.LiquidityCurrency = "usdt"
	view.ServiceCommissionAmount, err = beautyOf(service, "usdt")
	if err != nil {
		return PayoutView{}, 0, 0, err
	}
	view.TraderProfitAmount, err = beautyOf(profit, "usdt")
	if err != nil {
		return PayoutView{}, 0, 0, err
	}
	view.TraderExchangeMarkupAmount, err = beautyOf(markupAmount, "usdt")
	if err != nil {
		return PayoutView{}, 0, 0, err
	}
	view.BaseExchangePrice, err = beautyOf(basePrice, currency)
	if err != nil {
		return PayoutView{}, 0, 0, err
	}
	view.ExchangePrice, err = beautyOf(price, currency)
	if err != nil {
		return PayoutView{}, 0, 0, err
	}
	view.ServiceCommissionRate = serviceRate
	view.TraderExchangeMarkupRate = markupRate
	if callback.Valid {
		view.CallbackURL = &callback.String
	}
	if subCode.Valid {
		view.SubPaymentGateway = &subCode.String
	}
	if subName.Valid {
		view.SubPaymentGatewayName = &subName.String
	}
	if finished.Valid {
		stamp := finished.Time.Unix()
		view.FinishedAt = &stamp
	}
	if expires.Valid {
		view.ExpiresAt = expires.Time.Unix()
	}
	if created.Valid {
		view.CreatedAt = created.Time.Unix()
	}
	return view, ownerID, traderID, nil
}

func (s *Service) requirePayouts(ctx context.Context, userID int64) error {
	var enabled bool
	if err := s.DB.QueryRowContext(ctx, `SELECT payouts_enabled FROM users WHERE id = ?`, userID).Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return plain(403, "This action is unauthorized.")
	}
	return nil
}

func (s *Service) canSeePayout(ctx context.Context, actor, ownerID, traderID int64) error {
	if actor == ownerID || actor == traderID {
		return nil
	}
	admin, err := hasRole(ctx, s.DB, actor, "Admin")
	if err != nil {
		return err
	}
	if admin {
		return nil
	}
	return plain(403, "This action is unauthorized.")
}

func (s *Service) enqueuePayout(ctx context.Context, tx *sql.Tx, payoutID int64) error {
	var callback, gatewayURL, token sql.NullString
	var uuid string
	err := tx.QueryRowContext(ctx, `
		SELECT p.uuid, p.callback_url, g.callback_url, u.api_access_token
		FROM payouts p
		INNER JOIN payout_gateways g ON g.id = p.payout_gateway_id
		INNER JOIN users u ON u.id = p.owner_id
		WHERE p.id = ?`, payoutID).Scan(&uuid, &callback, &gatewayURL, &token)
	if err != nil {
		return err
	}
	url := strings.TrimSpace(callback.String)
	if url == "" {
		url = strings.TrimSpace(gatewayURL.String)
	}
	if url == "" {
		return nil
	}
	view, _, _, err := loadPayoutRow(ctx, tx, uuid, "", "")
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
		OrderID:     payoutID,
		OrderUUID:   uuid,
		Status:      view.Status,
		Amount:      view.PayoutAmount,
		CallbackURL: url,
		AccessToken: token.String,
		Resource:    resource,
	}
	return outbox.Insert(ctx, tx, id, event.TopicPayoutStatus, strconv.FormatInt(payoutID, 10), body, s.now())
}

func beautyOf(units, code string) (string, error) {
	value, err := money.FromUnits(zero(units), code)
	if err != nil {
		return "", err
	}
	return value.Beauty()
}

func firstDetail(raw string) string {
	var types []string
	if json.Unmarshal([]byte(raw), &types) != nil || len(types) == 0 {
		return ""
	}
	return types[0]
}

func detailAllowed(raw, kind string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	var types []string
	if json.Unmarshal([]byte(raw), &types) != nil || len(types) == 0 {
		return true
	}
	for _, item := range types {
		if item == kind {
			return true
		}
	}
	return false
}

func recommend(values []int) int {
	if len(values) <= 2 {
		return values[0]
	}
	sorted := append([]int(nil), values...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	return sorted[len(sorted)/2+1]
}

func emptyInt(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}
