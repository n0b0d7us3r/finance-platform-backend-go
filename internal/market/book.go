package market

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Sources is the order MarketService walks after the merchant's own market.
var Sources = []string{
	"bybit",
	"rapira_rates",
	"rapira",
	"rapira_top1_plus10",
	"rapira_top1_red",
}

// RefreshEvery matches app:update-p2p-prices.
const RefreshEvery = time.Minute

// ManualHold keeps an admin price in place while a test run is using it.
const ManualHold = 15 * time.Minute

// ErrMiss is an absent quote.
var ErrMiss = errors.New("price miss")

// Quote is one currency on one market, stored as subunit strings.
type Quote struct {
	BuyUnits  string
	SellUnits string
	HoldUntil time.Time
}

// Book is the persistent price store. A failed fetch must not delete the previous quote.
type Book interface {
	Get(ctx context.Context, currency, market string) (Quote, error)
	Put(ctx context.Context, currency, market string, quote Quote) error
}

// BuyUnits returns the buy price in subunits.
// preferred is the merchant market. Empty starts at bybit, then the other sources.
// A total miss is "0", same as MarketService::getBuyPrice.
func BuyUnits(ctx context.Context, book Book, currency, preferred string) (string, error) {
	if preferred == "" {
		preferred = "bybit"
	}
	order := []string{preferred}
	for _, source := range Sources {
		if source != preferred {
			order = append(order, source)
		}
	}
	for _, source := range order {
		quote, err := book.Get(ctx, currency, source)
		if errors.Is(err, ErrMiss) {
			continue
		}
		if err != nil {
			return "", err
		}
		if quote.BuyUnits == "" || quote.BuyUnits == "0" {
			continue
		}
		return quote.BuyUnits, nil
	}
	return "0", nil
}

// SellUnits returns the sell price in subunits, with the same source order as BuyUnits.
func SellUnits(ctx context.Context, book Book, currency, preferred string) (string, error) {
	if preferred == "" {
		preferred = "bybit"
	}
	order := []string{preferred}
	for _, source := range Sources {
		if source != preferred {
			order = append(order, source)
		}
	}
	for _, source := range order {
		quote, err := book.Get(ctx, currency, source)
		if errors.Is(err, ErrMiss) {
			continue
		}
		if err != nil {
			return "", err
		}
		if quote.SellUnits == "" || quote.SellUnits == "0" {
			continue
		}
		return quote.SellUnits, nil
	}
	return "0", nil
}

// MemBook is an in-memory book for tests.
type MemBook struct {
	mu     sync.Mutex
	quotes map[string]Quote
}

func (b *MemBook) Get(_ context.Context, currency, marketName string) (Quote, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	quote, ok := b.quotes[currency+":"+marketName]
	if !ok {
		return Quote{}, ErrMiss
	}
	return quote, nil
}

func (b *MemBook) Put(_ context.Context, currency, marketName string, quote Quote) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.quotes == nil {
		b.quotes = map[string]Quote{}
	}
	b.quotes[currency+":"+marketName] = quote
	return nil
}
