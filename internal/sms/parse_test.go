package sms

import "testing"

func TestParseCreditMessage(t *testing.T) {
	got, ok := Parse("Зачисление 1600.00 RUB", nil)
	if !ok || got.Amount != "1600" || got.Currency != "rub" {
		t.Fatalf("%+v %v", got, ok)
	}
	whole, ok := Parse("Зачисление 1900 RUB", nil)
	if !ok || whole.Amount != "1900" || whole.Currency != "rub" {
		t.Fatalf("%+v %v", whole, ok)
	}
}

func TestParseStopWordAndThousands(t *testing.T) {
	if _, ok := Parse("Зачисление 100 RUB otp code", []string{"otp"}); ok {
		t.Fatal("stop word still parsed")
	}
	got, ok := Parse("Поступление 1,234.50 RUB", nil)
	if !ok || got.Amount != "1234.5" {
		t.Fatalf("%+v %v", got, ok)
	}
	if !Blocked("security-hold: card", []string{"security-hold"}) {
		t.Fatal("block word")
	}
	if Blocked("Зачисление 1600.00 RUB", []string{"security-hold"}) {
		t.Fatal("plain credit is not a block")
	}
}

func TestNormalizeAmountCommaFraction(t *testing.T) {
	got, ok := Parse("+ 10,50 ₽ — баланс: 20", nil)
	if !ok || got.Amount != "10.5" || got.Currency != "rub" {
		t.Fatalf("%+v %v", got, ok)
	}
}
