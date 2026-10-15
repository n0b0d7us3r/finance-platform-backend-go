package market

import (
	"context"
	"testing"
	"time"
)

func TestBuyUnitsFallsThroughMarkets(t *testing.T) {
	book := &MemBook{}
	if err := book.Put(context.Background(), "rub", "rapira", Quote{BuyUnits: "8800", SellUnits: "8700"}); err != nil {
		t.Fatal(err)
	}
	got, err := BuyUnits(context.Background(), book, "rub", "bybit")
	if err != nil {
		t.Fatal(err)
	}
	if got != "8800" {
		t.Fatalf("got %s", got)
	}
}

func TestBuyUnitsPrefersMerchantMarket(t *testing.T) {
	book := &MemBook{}
	ctx := context.Background()
	if err := book.Put(ctx, "rub", "bybit", Quote{BuyUnits: "10000"}); err != nil {
		t.Fatal(err)
	}
	if err := book.Put(ctx, "rub", "rapira", Quote{BuyUnits: "9100"}); err != nil {
		t.Fatal(err)
	}
	got, err := BuyUnits(ctx, book, "rub", "rapira")
	if err != nil {
		t.Fatal(err)
	}
	if got != "9100" {
		t.Fatalf("got %s", got)
	}
}

func TestBuyUnitsMissIsZero(t *testing.T) {
	got, err := BuyUnits(context.Background(), &MemBook{}, "usd", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "0" {
		t.Fatalf("got %s", got)
	}
}

func TestLoadKeepsHeldQuote(t *testing.T) {
	book := &MemBook{}
	ctx := context.Background()
	held := Quote{BuyUnits: "10000", SellUnits: "10100", HoldUntil: time.Now().Add(time.Hour)}
	if err := book.Put(ctx, "rub", "bybit", held); err != nil {
		t.Fatal(err)
	}
	client := testClient(t)
	result, err := client.Load(ctx, book)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kept < 1 {
		t.Fatalf("kept %d", result.Kept)
	}
	quote, err := book.Get(ctx, "rub", "bybit")
	if err != nil {
		t.Fatal(err)
	}
	if quote.BuyUnits != "10000" {
		t.Fatalf("held buy replaced with %s", quote.BuyUnits)
	}
}
