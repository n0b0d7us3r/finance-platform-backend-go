package read

import (
	"testing"
	"time"
)

func TestPresentOrderBeautyAndAccess(t *testing.T) {
	rate := 1.5
	owner := int64(9)
	finished := time.Unix(1_700_000_100, 0).UTC()
	order := Order{
		UUID:           "11111111-1111-1111-1111-111111111111",
		External:       "ext-1",
		Merchant:       Merchant{UUID: "m-1", Name: "Shop", UserID: 4},
		BaseUnits:      "10050",
		Amount:         "10100",
		Profit:         "150",
		MerchantP:      "100",
		ServiceP:       "50",
		Currency:       "RUB",
		BaseConversion: "90.00",
		Conversion:     "9500",
		TraderRate:     &rate,
		Status:         "pending",
		SubStatus:      "waiting_for_payment",
		DetailOwner:    &owner,
		Detail:         &Detail{Detail: "4111", Type: "card", Initials: "IVAN"},
		Expires:        time.Unix(1_700_000_000, 0).UTC(),
		Created:        time.Unix(1_699_999_000, 0).UTC(),
		Finished:       &finished,
	}
	if !CanSeeOrder(4, order, false) || !CanSeeOrder(9, order, false) || !CanSeeOrder(1, order, true) {
		t.Fatal("allowed users rejected")
	}
	if CanSeeOrder(3, order, false) {
		t.Fatal("stranger allowed")
	}

	view, err := PresentOrder(order, nil, time.Unix(1_700_000_050, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if view.BaseAmount != "100.5" || view.Amount != "101" || view.Profit != "1.5" {
		t.Fatalf("amounts %s %s %s", view.BaseAmount, view.Amount, view.Profit)
	}
	if view.Currency != "rub" || view.ProfitCurrency != "usdt" || view.ConversionPrice != "95" {
		t.Fatalf("currency %s profit %s conversion %s", view.Currency, view.ProfitCurrency, view.ConversionPrice)
	}
	if view.TraderCommissionRate != "1.5" || view.ServiceCommissionRateTotal != "0" {
		t.Fatalf("rates %s %s", view.TraderCommissionRate, view.ServiceCommissionRateTotal)
	}
	if view.PaymentDetail.Detail != "4111" || view.CurrentServerTime != 1_700_000_050 {
		t.Fatalf("detail %s time %d", view.PaymentDetail.Detail, view.CurrentServerTime)
	}
	if view.FinishedAt == nil || *view.FinishedAt != 1_700_000_100 {
		t.Fatalf("finished %#v", view.FinishedAt)
	}
}

func TestPresentOrderPrefersExternalRequisites(t *testing.T) {
	order := Order{
		UUID:      "11111111-1111-1111-1111-111111111111",
		Merchant:  Merchant{UUID: "m", Name: "n", UserID: 1},
		BaseUnits: "0", Amount: "0", Profit: "0", MerchantP: "0", ServiceP: "0",
		Currency: "rub", Conversion: "0",
		Status: "pending", SubStatus: "waiting_for_payment",
		Detail:  &Detail{Detail: "db", Type: "card", Initials: "DB"},
		Expires: time.Unix(10, 0), Created: time.Unix(10, 0),
	}
	view, err := PresentOrder(order, &External{Detail: "ext", Type: "phone", Initials: "EX"}, time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	if view.PaymentDetail.Detail != "ext" || view.PaymentDetail.Initials != "EX" {
		t.Fatalf("%+v", view.PaymentDetail)
	}
}

func TestPresentGatewayMarkup(t *testing.T) {
	view, err := PresentGateway(Gateway{
		Name: "Sber", Code: "sber", Currency: "rub",
		Reservation: 10, BuyMarkup: 10, OrderRate: 9, PayoutRate: 1, SellMarkup: 2,
	}, "10050")
	if err != nil {
		t.Fatal(err)
	}
	if view.BaseConversion != "100.50" || view.ConversionPrice != "110.55" || view.Currency != "rub" {
		t.Fatalf("%+v", view)
	}
}

func TestPresentBalance(t *testing.T) {
	view, err := PresentBalance("15075")
	if err != nil {
		t.Fatal(err)
	}
	if view.Balance != "150.75" {
		t.Fatalf("%s", view.Balance)
	}
}
