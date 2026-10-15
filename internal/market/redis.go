package market

import (
	"context"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// RedisBook stores quotes as hashes. Keys have no TTL: the last good price
// stays until a later successful fetch replaces it.
type RedisBook struct {
	Client *goredis.Client
}

func (b RedisBook) Get(ctx context.Context, currency, marketName string) (Quote, error) {
	values, err := b.Client.HGetAll(ctx, quoteKey(currency, marketName)).Result()
	if err != nil {
		return Quote{}, err
	}
	if len(values) == 0 || values["buy"] == "" {
		return Quote{}, ErrMiss
	}
	quote := Quote{BuyUnits: values["buy"], SellUnits: values["sell"]}
	if raw := values["hold"]; raw != "" && raw != "0" {
		sec, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return Quote{}, err
		}
		quote.HoldUntil = time.Unix(sec, 0)
	}
	return quote, nil
}

func (b RedisBook) Put(ctx context.Context, currency, marketName string, quote Quote) error {
	hold := "0"
	if !quote.HoldUntil.IsZero() {
		hold = strconv.FormatInt(quote.HoldUntil.Unix(), 10)
	}
	return b.Client.HSet(ctx, quoteKey(currency, marketName),
		"buy", quote.BuyUnits,
		"sell", quote.SellUnits,
		"hold", hold,
	).Err()
}

func quoteKey(currency, marketName string) string {
	return "price:" + strings.ToLower(currency) + ":" + strings.ToLower(marketName)
}
