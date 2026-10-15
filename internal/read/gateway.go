package read

import (
	"strconv"

	"finance-platform/go/internal/money"
)

// Gateway is an active payment gateway row.
type Gateway struct {
	Name        string
	Code        string
	Schema      *string
	Currency    string
	MinLimit    *string
	MaxLimit    *string
	Reservation int
	DetailTypes []string
	Transgran   bool
	Intrabank   bool
	OrderRate   float64
	PayoutRate  float64
	BuyMarkup   float64
	SellMarkup  float64
}

// GatewayView is the V1 payment gateway resource.
type GatewayView struct {
	Name              string   `json:"name"`
	Code              string   `json:"code"`
	Schema            *string  `json:"schema"`
	SubMethods        *string  `json:"sub_methods"`
	Currency          string   `json:"currency"`
	MinLimit          *string  `json:"min_limit"`
	MaxLimit          *string  `json:"max_limit"`
	ReservationTime   int      `json:"reservation_time"`
	DetailTypes       []string `json:"detail_types"`
	Transgran         bool     `json:"is_transgran"`
	Intrabank         bool     `json:"is_intrabank"`
	OrderServiceRate  float64  `json:"order_service_commission_rate"`
	PayoutServiceRate float64  `json:"payout_service_commission_rate"`
	BuyPriceMarkup    float64  `json:"buy_price_markup_rate"`
	SellPriceMarkup   float64  `json:"sell_price_markup_rate"`
	BaseConversion    string   `json:"base_conversion_price"`
	ConversionPrice   string   `json:"conversion_price"`
}

// PresentGateway applies the trader markup to the cached buy price.
func PresentGateway(row Gateway, buyUnits string) (GatewayView, error) {
	base, err := money.FromUnits(buyUnits, row.Currency)
	if err != nil {
		return GatewayView{}, err
	}
	basePrecise, err := base.Precision()
	if err != nil {
		return GatewayView{}, err
	}
	multiplier := strconv.FormatFloat(row.BuyMarkup/100, 'f', -1, 64)
	part, err := base.Mul(multiplier)
	if err != nil {
		return GatewayView{}, err
	}
	withMarkup, err := base.AddMoney(part)
	if err != nil {
		return GatewayView{}, err
	}
	conversion, err := withMarkup.Precision()
	if err != nil {
		return GatewayView{}, err
	}
	code, err := money.New(row.Currency)
	if err != nil {
		return GatewayView{}, err
	}
	return GatewayView{
		Name:              row.Name,
		Code:              row.Code,
		Schema:            row.Schema,
		Currency:          code.Code(),
		MinLimit:          row.MinLimit,
		MaxLimit:          row.MaxLimit,
		ReservationTime:   row.Reservation,
		DetailTypes:       row.DetailTypes,
		Transgran:         row.Transgran,
		Intrabank:         row.Intrabank,
		OrderServiceRate:  row.OrderRate,
		PayoutServiceRate: row.PayoutRate,
		BuyPriceMarkup:    row.BuyMarkup,
		SellPriceMarkup:   row.SellMarkup,
		BaseConversion:    basePrecise,
		ConversionPrice:   conversion,
	}, nil
}

// BalanceView is GET /api/v1/wallet/balance.
type BalanceView struct {
	Balance string `json:"balance"`
}

// PresentBalance formats the merchant USDT units the way WalletService does.
func PresentBalance(units string) (BalanceView, error) {
	beauty, err := beauty(units, "usdt")
	if err != nil {
		return BalanceView{}, err
	}
	return BalanceView{Balance: beauty}, nil
}
