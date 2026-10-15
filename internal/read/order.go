package read

import (
	"strconv"
	"time"

	"finance-platform/go/internal/money"
)

// Order is one V1 show row after SQL nulls are collapsed.
type Order struct {
	ID        int64
	UUID      string
	External  string
	Merchant  Merchant
	BaseUnits string
	Amount    string
	Profit    string
	MerchantP string
	ServiceP  string
	Currency  string

	BaseConversion string
	Conversion     string

	TraderRate  *float64
	ServiceRate *float64

	Status    string
	SubStatus string
	Callback  *string

	Gateway *GatewayRef
	Detail  *Detail

	DetailOwner *int64
	Dispute     *Dispute

	Finished *time.Time
	Expires  time.Time
	Created  time.Time
}

// Merchant is the owning merchant of an order.
type Merchant struct {
	UUID        string
	Name        string
	Description *string
	UserID      int64
}

// GatewayRef is the payment gateway attached to an order.
type GatewayRef struct {
	Code      *string
	Schema    *string
	Name      *string
	Transgran bool
	Intrabank bool
}

// Detail is a trader requisite.
type Detail struct {
	Detail   string
	Type     string
	Initials string
}

// Dispute is the latest dispute on the order, when one exists.
type Dispute struct {
	Status       *string
	CancelReason *string
	Reason       *string
}

// External is a cascade requisite stored as JSON for one order.
type External struct {
	Detail   string
	Type     string
	Initials string
}

// CanSeeOrder repeats Gate access-to-order.
func CanSeeOrder(userID int64, order Order, superAdmin bool) bool {
	if superAdmin || userID == order.Merchant.UserID {
		return true
	}
	return order.DetailOwner != nil && userID == *order.DetailOwner
}

// OrderView is the V1 order JSON object.
type OrderView struct {
	OrderID                       string       `json:"order_id"`
	ExternalID                    string       `json:"external_id"`
	MerchantID                    string       `json:"merchant_id"`
	BaseAmount                    string       `json:"base_amount"`
	Amount                        string       `json:"amount"`
	Profit                        string       `json:"profit"`
	MerchantProfit                string       `json:"merchant_profit"`
	ServiceProfit                 string       `json:"service_profit"`
	Currency                      string       `json:"currency"`
	ProfitCurrency                string       `json:"profit_currency"`
	ConversionPriceCurrency       string       `json:"conversion_price_currency"`
	BaseConversionPrice           string       `json:"base_conversion_price"`
	ConversionPrice               string       `json:"conversion_price"`
	TraderCommissionRate          string       `json:"trader_commission_rate"`
	ServiceCommissionRateTotal    string       `json:"service_commission_rate_total"`
	ServiceCommissionRateMerchant string       `json:"service_commission_rate_merchant"`
	ServiceCommissionRateClient   string       `json:"service_commission_rate_client"`
	Status                        string       `json:"status"`
	SubStatus                     string       `json:"sub_status"`
	CallbackURL                   *string      `json:"callback_url"`
	PaymentGateway                *string      `json:"payment_gateway"`
	PaymentGatewaySchema          *string      `json:"payment_gateway_nspk_schema"`
	PaymentGatewayName            *string      `json:"payment_gateway_name"`
	PaymentGatewayTransgran       bool         `json:"payment_gateway_is_transgran"`
	PaymentGatewayIntrabank       bool         `json:"payment_gateway_is_intrabank"`
	Method                        *string      `json:"method"`
	MethodName                    *string      `json:"method_name"`
	MethodSchema                  *string      `json:"method_schema"`
	PaymentDetail                 DetailView   `json:"payment_detail"`
	Merchant                      MerchantView `json:"merchant"`
	FinishedAt                    *int64       `json:"finished_at"`
	ExpiresAt                     int64        `json:"expires_at"`
	CreatedAt                     int64        `json:"created_at"`
	CurrentServerTime             int64        `json:"current_server_time"`
}

// DetailView is payment_detail inside the order resource.
type DetailView struct {
	Detail     string       `json:"detail"`
	DetailType string       `json:"detail_type"`
	Initials   string       `json:"initials"`
	Dispute    *DisputeView `json:"dispute"`
}

// DisputeView is nested under payment_detail. Null fields stay null.
type DisputeView struct {
	Status       *string `json:"status"`
	CancelReason *string `json:"cancel_reason"`
	Reason       *string `json:"reason"`
}

// MerchantView is the merchant block.
type MerchantView struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

// PresentOrder builds the V1 resource. Dispute is null when the order has no row.
func PresentOrder(order Order, external *External, now time.Time) (OrderView, error) {
	base, err := beauty(order.BaseUnits, order.Currency)
	if err != nil {
		return OrderView{}, err
	}
	amount, err := beauty(order.Amount, order.Currency)
	if err != nil {
		return OrderView{}, err
	}
	profit, err := beauty(order.Profit, "usdt")
	if err != nil {
		return OrderView{}, err
	}
	merchantProfit, err := beauty(order.MerchantP, "usdt")
	if err != nil {
		return OrderView{}, err
	}
	serviceProfit, err := beauty(order.ServiceP, "usdt")
	if err != nil {
		return OrderView{}, err
	}
	conversion, err := money.FromUnits(zeroUnits(order.Conversion), order.Currency)
	if err != nil {
		return OrderView{}, err
	}
	conversionBeauty, err := conversion.Beauty()
	if err != nil {
		return OrderView{}, err
	}

	baseConversion := order.BaseConversion
	if baseConversion == "" {
		baseConversion = "0"
	}

	view := OrderView{
		OrderID:                       order.UUID,
		ExternalID:                    order.External,
		MerchantID:                    order.Merchant.UUID,
		BaseAmount:                    base,
		Amount:                        amount,
		Profit:                        profit,
		MerchantProfit:                merchantProfit,
		ServiceProfit:                 serviceProfit,
		Currency:                      conversion.Currency().Code(),
		ProfitCurrency:                "usdt",
		ConversionPriceCurrency:       conversion.Currency().Code(),
		BaseConversionPrice:           baseConversion,
		ConversionPrice:               conversionBeauty,
		TraderCommissionRate:          floatString(order.TraderRate),
		ServiceCommissionRateTotal:    floatString(order.ServiceRate),
		ServiceCommissionRateMerchant: "0",
		ServiceCommissionRateClient:   "0",
		Status:                        order.Status,
		SubStatus:                     order.SubStatus,
		CallbackURL:                   order.Callback,
		Method:                        nil,
		MethodName:                    nil,
		MethodSchema:                  nil,
		Merchant: MerchantView{
			Name:        order.Merchant.Name,
			Description: order.Merchant.Description,
		},
		ExpiresAt:         order.Expires.Unix(),
		CreatedAt:         order.Created.Unix(),
		CurrentServerTime: now.Unix(),
		PaymentDetail: DetailView{
			Detail:     "Данные от партнерской платформы",
			DetailType: "phone",
			Initials:   "Партнерские реквизиты",
			Dispute:    disputeView(order.Dispute),
		},
	}
	if order.Gateway != nil {
		view.PaymentGateway = order.Gateway.Code
		view.PaymentGatewaySchema = order.Gateway.Schema
		view.PaymentGatewayName = order.Gateway.Name
		view.PaymentGatewayTransgran = order.Gateway.Transgran
		view.PaymentGatewayIntrabank = order.Gateway.Intrabank
	}
	if external != nil {
		view.PaymentDetail.Detail = external.Detail
		view.PaymentDetail.DetailType = fallback(external.Type, "phone")
		view.PaymentDetail.Initials = external.Initials
	} else if order.Detail != nil {
		view.PaymentDetail.Detail = fallback(order.Detail.Detail, "Данные от партнерской платформы")
		view.PaymentDetail.DetailType = fallback(order.Detail.Type, "phone")
		view.PaymentDetail.Initials = fallback(order.Detail.Initials, "Партнерские реквизиты")
	}
	if order.Finished != nil {
		unix := order.Finished.Unix()
		view.FinishedAt = &unix
	}
	// Currency code on the order itself, not the conversion cast, when conversion units were empty.
	if code, err := money.New(order.Currency); err == nil {
		view.Currency = code.Code()
	}
	return view, nil
}

func disputeView(dispute *Dispute) *DisputeView {
	if dispute == nil {
		return nil
	}
	return &DisputeView{
		Status:       dispute.Status,
		CancelReason: dispute.CancelReason,
		Reason:       dispute.Reason,
	}
}

func beauty(units, code string) (string, error) {
	amount, err := money.FromUnits(zeroUnits(units), code)
	if err != nil {
		return "", err
	}
	return amount.Beauty()
}

func zeroUnits(units string) string {
	if units == "" {
		return "0"
	}
	return units
}

func floatString(value *float64) string {
	if value == nil {
		return "0"
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

func fallback(value, def string) string {
	if value == "" {
		return def
	}
	return value
}
