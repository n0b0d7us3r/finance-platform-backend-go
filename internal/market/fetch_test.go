package market

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRapiraRatesUsesAskAndBid(t *testing.T) {
	client := testClient(t)
	quote, err := client.quote(context.Background(), "rub", "rapira_rates")
	if err != nil {
		t.Fatal(err)
	}
	if quote.BuyUnits != "9150" || quote.SellUnits != "9050" {
		t.Fatalf("quote %+v", quote)
	}
}

func TestPlateAverageAndTop1(t *testing.T) {
	client := testClient(t)
	ctx := context.Background()
	plate, err := client.quote(ctx, "rub", "rapira")
	if err != nil {
		t.Fatal(err)
	}
	if plate.BuyUnits != "10100" || plate.SellUnits != "9700" {
		t.Fatalf("plate %+v", plate)
	}
	plus, err := client.quote(ctx, "rub", "rapira_top1_plus10")
	if err != nil {
		t.Fatal(err)
	}
	if plus.BuyUnits != "11000" || plus.SellUnits != "10800" {
		t.Fatalf("plus %+v", plus)
	}
	red, err := client.quote(ctx, "rub", "rapira_top1_red")
	if err != nil {
		t.Fatal(err)
	}
	if red.BuyUnits != "9800" || red.SellUnits != "10000" {
		t.Fatalf("red %+v", red)
	}
	if _, err := client.quote(ctx, "kzt", "rapira_top1_red"); err != errUnsupported {
		t.Fatalf("kzt red %v", err)
	}
}

func TestBybitRoundsToCents(t *testing.T) {
	client := testClient(t)
	quote, err := client.quote(context.Background(), "rub", "bybit")
	if err != nil {
		t.Fatal(err)
	}
	// (100.10 + 100.20 + 100.33) / 3 = 100.21
	if quote.BuyUnits != "10021" || quote.SellUnits != "9900" {
		t.Fatalf("quote %+v", quote)
	}
}

func TestBinanceForOtherFiat(t *testing.T) {
	client := testClient(t)
	quote, err := client.quote(context.Background(), "kzt", "rapira")
	if err != nil {
		t.Fatal(err)
	}
	if quote.BuyUnits != "51000" || quote.SellUnits != "49000" {
		t.Fatalf("quote %+v", quote)
	}
}

func testClient(t *testing.T) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Side      string `json:"side"`
			TradeType string `json:"tradeType"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/rates":
			_, _ = w.Write([]byte(`{"data":[{"symbol":"USDT/RUB","close":90,"askPrice":91.5,"bidPrice":90.5}]}`))
		case "/plate":
			_, _ = w.Write([]byte(`{"ask":{"items":[{"price":100},{"price":102}]},"bid":{"items":[{"price":98},{"price":96}]}}`))
		case "/bybit":
			if body.Side == "0" {
				_, _ = w.Write([]byte(`{"result":{"items":[{"price":"99.00"},{"price":"99.00"},{"price":"99.00"}]}}`))
				return
			}
			_, _ = w.Write([]byte(`{"result":{"items":[{"price":"100.10"},{"price":"100.20"},{"price":"100.33"}]}}`))
		case "/binance":
			if body.TradeType == "SELL" {
				_, _ = w.Write([]byte(`{"data":[{"adv":{"price":"490"}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"adv":{"price":"510"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return &Client{
		HTTP:           server.Client(),
		BybitURL:       server.URL + "/bybit",
		RapiraRatesURL: server.URL + "/rates",
		PlateURL:       server.URL + "/plate",
		BinanceURL:     server.URL + "/binance",
	}
}
