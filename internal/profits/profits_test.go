package profits

import (
	"testing"

	"finance-platform/go/internal/money"
)

func TestCalculate1500At100(t *testing.T) {
	amount, err := money.FromPrecision("1500", "rub")
	if err != nil {
		t.Fatal(err)
	}
	rate, err := money.FromPrecision("100", "rub")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Calculate(amount, rate, 10, 7)
	if err != nil {
		t.Fatal(err)
	}
	assertUnits(t, "total", got.Total, "1500")
	assertUnits(t, "merchant", got.Merchant, "1350")
	assertUnits(t, "service", got.Service, "45")
	assertUnits(t, "trader", got.Trader, "105")
	assertUnits(t, "paid", got.Paid, "1395")
}

func TestLeaderProfitTruncates(t *testing.T) {
	total, err := money.FromUnits("1500", "usdt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := LeaderProfit(total, "1.50")
	if err != nil {
		t.Fatal(err)
	}
	assertUnits(t, "leader", got, "22")
	service, err := money.FromUnits("45", "usdt")
	if err != nil {
		t.Fatal(err)
	}
	net, err := NetService(service, []money.Money{got})
	if err != nil {
		t.Fatal(err)
	}
	assertUnits(t, "net", net, "23")
	zeroRate, err := LeaderProfit(total, "0")
	if err != nil {
		t.Fatal(err)
	}
	assertUnits(t, "zero rate", zeroRate, "0")
	floored, err := NetService(service, []money.Money{total})
	if err != nil {
		t.Fatal(err)
	}
	assertUnits(t, "floor", floored, "0")
}

func assertUnits(t *testing.T, name string, value money.Money, units string) {
	t.Helper()
	if value.Units() != units {
		t.Fatalf("%s units %s want %s", name, value.Units(), units)
	}
}
