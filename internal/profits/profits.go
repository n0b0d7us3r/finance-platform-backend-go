package profits

import (
	"errors"

	"finance-platform/go/internal/money"

	"github.com/shopspring/decimal"
)

// Result is Profits::calculate with a zero team-leader rate.
type Result struct {
	Total    money.Money
	Merchant money.Money
	Service  money.Money
	Trader   money.Money
	Paid     money.Money
}

// Calculate divides the fiat amount by the buy rate and splits the USDT
// total by the gateway and trader commission rates.
func Calculate(amount, rate money.Money, totalRate, traderRate float64) (Result, error) {
	if totalRate < traderRate {
		return Result{}, errors.New("The total commission cannot be less than the trader's commission.")
	}
	if totalRate <= 0 {
		return Result{}, errors.New("total commission must be positive")
	}
	total, err := amount.Convert(rate, "usdt")
	if err != nil {
		return Result{}, err
	}
	totalRatio := decimal.NewFromFloat(totalRate).Div(decimal.NewFromInt(100)).String()
	commission, err := total.Mul(totalRatio)
	if err != nil {
		return Result{}, err
	}
	merchantProfit, err := total.SubMoney(commission)
	if err != nil {
		return Result{}, err
	}
	serviceRate := decimal.NewFromFloat(totalRate).Sub(decimal.NewFromFloat(traderRate))
	serviceRatio := serviceRate.Div(decimal.NewFromFloat(totalRate)).String()
	serviceProfit, err := commission.Mul(serviceRatio)
	if err != nil {
		return Result{}, err
	}
	traderProfit, err := commission.SubMoney(serviceProfit)
	if err != nil {
		return Result{}, err
	}
	paid, err := total.SubMoney(traderProfit)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Total:    total,
		Merchant: merchantProfit,
		Service:  serviceProfit,
		Trader:   traderProfit,
		Paid:     paid,
	}, nil
}

// LeaderProfit is total × rate/100, truncated the same way as the other shares.
// A rate that is not positive yields a zero amount.
func LeaderProfit(total money.Money, rate string) (money.Money, error) {
	zero, err := money.Zero(total.Currency().Code())
	if err != nil {
		return money.Money{}, err
	}
	value, err := decimal.NewFromString(rate)
	if err != nil {
		return money.Money{}, err
	}
	if !value.IsPositive() {
		return zero, nil
	}
	return total.Mul(value.Div(decimal.NewFromInt(100)).String())
}

// NetService subtracts leader shares from the service share and floors at zero.
func NetService(service money.Money, parts []money.Money) (money.Money, error) {
	net := service
	for _, part := range parts {
		next, err := net.SubMoney(part)
		if err != nil {
			return money.Money{}, err
		}
		net = next
	}
	negative, err := net.LessThanZero()
	if err != nil {
		return money.Money{}, err
	}
	if negative {
		return money.Zero(service.Currency().Code())
	}
	return net, nil
}
