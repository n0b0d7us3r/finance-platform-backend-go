package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"finance-platform/go/internal/read"
)

// ErrAbsent is a missing order or wallet row.
var ErrAbsent = errors.New("not found")

const findOrderSQL = `
SELECT
    o.id, o.uuid, o.external_id, o.base_amount, o.amount, o.total_profit,
    o.merchant_profit, o.service_profit, o.currency, o.base_conversion_price,
    o.conversion_price, o.trader_commission_rate, o.total_service_commission_rate,
    o.status, o.sub_status, o.callback_url, o.finished_at, o.expires_at, o.created_at,
    m.uuid, m.name, m.description, m.user_id,
    pg.code, pg.nspk_schema, pg.name, pg.is_transgran, pg.is_intrabank,
    pd.detail, pd.detail_type, pd.initials, pd.user_id,
    d.status, d.cancel_reason, d.reason
FROM orders o
INNER JOIN merchants m ON m.id = o.merchant_id
LEFT JOIN payment_gateways pg ON pg.id = o.payment_gateway_id
LEFT JOIN payment_details pd ON pd.id = o.payment_detail_id
LEFT JOIN disputes d ON d.id = (
    SELECT id FROM disputes WHERE order_id = o.id ORDER BY id DESC LIMIT 1
)
`

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// FindOrderByUUID loads one order for V1 show.
func FindOrderByUUID(ctx context.Context, db *sql.DB, uuid string) (read.Order, error) {
	return scanOrder(ctx, db, findOrderSQL+` WHERE o.uuid = ? LIMIT 1`, uuid)
}

// FindOrderByIDTx loads an order inside the transaction that just changed it.
func FindOrderByIDTx(ctx context.Context, tx *sql.Tx, id int64) (read.Order, error) {
	return scanOrder(ctx, tx, findOrderSQL+` WHERE o.id = ? LIMIT 1`, id)
}

// FindOrderByExternal loads an order by merchant uuid and external id.
func FindOrderByExternal(ctx context.Context, db *sql.DB, merchantUUID, externalID string) (read.Order, error) {
	return scanOrder(ctx, db, findOrderSQL+` WHERE m.uuid = ? AND o.external_id = ? LIMIT 1`, merchantUUID, externalID)
}

func scanOrder(ctx context.Context, db queryRower, query string, args ...any) (read.Order, error) {
	var (
		order          read.Order
		external       sql.NullString
		baseAmount     sql.NullString
		amount         sql.NullString
		profit         sql.NullString
		merchantProfit sql.NullString
		serviceProfit  sql.NullString
		currency       sql.NullString
		baseConv       sql.NullString
		conversion     sql.NullString
		traderRate     sql.NullFloat64
		serviceRate    sql.NullFloat64
		status         sql.NullString
		subStatus      sql.NullString
		callback       sql.NullString
		finished       sql.NullTime
		expires        sql.NullTime
		created        sql.NullTime
		description    sql.NullString
		gatewayCode    sql.NullString
		gatewaySchema  sql.NullString
		gatewayName    sql.NullString
		transgran      sql.NullBool
		intrabank      sql.NullBool
		detail         sql.NullString
		detailType     sql.NullString
		initials       sql.NullString
		detailUser     sql.NullInt64
		disputeStatus  sql.NullString
		cancelReason   sql.NullString
		reason         sql.NullString
	)
	err := db.QueryRowContext(ctx, query, args...).Scan(
		&order.ID, &order.UUID, &external, &baseAmount, &amount, &profit,
		&merchantProfit, &serviceProfit, &currency, &baseConv, &conversion,
		&traderRate, &serviceRate, &status, &subStatus, &callback, &finished, &expires, &created,
		&order.Merchant.UUID, &order.Merchant.Name, &description, &order.Merchant.UserID,
		&gatewayCode, &gatewaySchema, &gatewayName, &transgran, &intrabank,
		&detail, &detailType, &initials, &detailUser,
		&disputeStatus, &cancelReason, &reason,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return read.Order{}, ErrAbsent
	}
	if err != nil {
		return read.Order{}, err
	}
	order.External = external.String
	order.BaseUnits = baseAmount.String
	order.Amount = amount.String
	order.Profit = profit.String
	order.MerchantP = merchantProfit.String
	order.ServiceP = serviceProfit.String
	order.Currency = currency.String
	order.BaseConversion = baseConv.String
	order.Conversion = conversion.String
	order.TraderRate = nullFloat(traderRate)
	order.ServiceRate = nullFloat(serviceRate)
	order.Status = status.String
	order.SubStatus = subStatus.String
	order.Callback = nullString(callback)
	order.Merchant.Description = nullString(description)
	if !expires.Valid || !created.Valid {
		return read.Order{}, errors.New("order timestamps are missing")
	}
	order.Expires = expires.Time
	order.Created = created.Time
	if finished.Valid {
		finishedAt := finished.Time
		order.Finished = &finishedAt
	}
	if gatewayCode.Valid || gatewayName.Valid || gatewaySchema.Valid {
		order.Gateway = &read.GatewayRef{
			Code:      nullString(gatewayCode),
			Schema:    nullString(gatewaySchema),
			Name:      nullString(gatewayName),
			Transgran: transgran.Bool,
			Intrabank: intrabank.Bool,
		}
	}
	if detail.Valid || detailType.Valid || initials.Valid || detailUser.Valid {
		order.Detail = &read.Detail{Detail: detail.String, Type: detailType.String, Initials: initials.String}
	}
	if detailUser.Valid {
		owner := detailUser.Int64
		order.DetailOwner = &owner
	}
	if disputeStatus.Valid || cancelReason.Valid || reason.Valid {
		order.Dispute = &read.Dispute{
			Status:       nullString(disputeStatus),
			CancelReason: nullString(cancelReason),
			Reason:       nullString(reason),
		}
	}
	return order, nil
}

// MerchantBalanceUnits returns the merchant wallet balance in USDT subunits.
func MerchantBalanceUnits(ctx context.Context, db *sql.DB, userID int64) (string, error) {
	var units sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT merchant_balance FROM wallets WHERE user_id = ? LIMIT 1
	`, userID).Scan(&units)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrAbsent
	}
	if err != nil {
		return "", err
	}
	return units.String, nil
}

const listGatewaysSQL = `
SELECT
    name, code, nspk_schema, currency, min_limit, max_limit,
    reservation_time_for_orders, detail_types, is_transgran, is_intrabank,
    total_service_commission_rate_for_orders, total_service_commission_rate_for_payouts,
    trader_commission_rate_for_orders, trader_commission_rate_for_payouts
FROM payment_gateways
WHERE is_active = 1
`

// ListGateways returns active gateways. transgran nil means no filter.
func ListGateways(ctx context.Context, db *sql.DB, transgran *bool) ([]read.Gateway, error) {
	query := listGatewaysSQL
	var args []any
	if transgran != nil {
		query += ` AND is_transgran = ?`
		args = append(args, *transgran)
	}
	query += ` ORDER BY id`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []read.Gateway
	for rows.Next() {
		var (
			name, code, currency                         sql.NullString
			schema, minLimit, maxLimit, detailTypes      sql.NullString
			reservation                                  sql.NullInt64
			trans, intra                                 sql.NullBool
			orderRate, payoutRate, buyMarkup, sellMarkup sql.NullFloat64
		)
		if err := rows.Scan(
			&name, &code, &schema, &currency, &minLimit, &maxLimit,
			&reservation, &detailTypes, &trans, &intra,
			&orderRate, &payoutRate, &buyMarkup, &sellMarkup,
		); err != nil {
			return nil, err
		}
		row := read.Gateway{
			Name:        name.String,
			Code:        code.String,
			Schema:      nullString(schema),
			Currency:    currency.String,
			MinLimit:    nullString(minLimit),
			MaxLimit:    nullString(maxLimit),
			Reservation: int(reservation.Int64),
			Transgran:   trans.Bool,
			Intrabank:   intra.Bool,
			OrderRate:   orderRate.Float64,
			PayoutRate:  payoutRate.Float64,
			BuyMarkup:   buyMarkup.Float64,
			SellMarkup:  sellMarkup.Float64,
		}
		if detailTypes.Valid && detailTypes.String != "" {
			if err := json.Unmarshal([]byte(detailTypes.String), &row.DetailTypes); err != nil {
				return nil, err
			}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func nullString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}

func nullFloat(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	number := value.Float64
	return &number
}
