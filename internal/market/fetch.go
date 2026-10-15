package market

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"finance-platform/go/internal/money"

	"github.com/shopspring/decimal"
)

const (
	bybitURL       = "https://api2.bybit.com/fiat/otc/item/online"
	rapiraRatesURL = "https://api.rapira.net/open/market/rates"
	plateURL       = "https://api.rapira.net/market/exchange-plate-mini?symbol=USDT/RUB"
	binanceURL     = "https://p2p.binance.com/bapi/c2c/v2/friendly/c2c/adv/search"
	bybitAds       = 3
	plateDepth     = 5
)

var errEmpty = errors.New("empty price")
var errUnsupported = errors.New("market does not price this currency")

// Client loads the same sources as App\Services\Market\Utils\Parser.
type Client struct {
	HTTP           *http.Client
	BybitURL       string
	RapiraRatesURL string
	PlateURL       string
	BinanceURL     string
	Now            func() time.Time
}

// Failure is one source that did not return a quote.
type Failure struct {
	Currency string
	Market   string
	Err      string
}

// Result counts one refresh pass.
type Result struct {
	Saved   int
	Kept    int
	Skipped int
	Failed  int
	Errors  []Failure
}

// NewClient is the production Rapira, Bybit and Binance client.
func NewClient() *Client {
	return &Client{
		HTTP:           &http.Client{Timeout: 12 * time.Second},
		BybitURL:       bybitURL,
		RapiraRatesURL: rapiraRatesURL,
		PlateURL:       plateURL,
		BinanceURL:     binanceURL,
		Now:            time.Now,
	}
}

// Load fetches every catalogue currency and source.
// A failed source leaves the previous quote in place. A quote on hold is kept.
func (c *Client) Load(ctx context.Context, book Book) (Result, error) {
	if book == nil {
		return Result{}, errors.New("price store is nil")
	}
	type pair struct {
		currency string
		market   string
	}
	var pairs []pair
	for _, currency := range money.All() {
		for _, source := range Sources {
			pairs = append(pairs, pair{currency.Code(), source})
		}
	}

	var (
		mu     sync.Mutex
		result Result
		wg     sync.WaitGroup
		sem    = make(chan struct{}, 4)
	)
	for _, item := range pairs {
		wg.Add(1)
		sem <- struct{}{}
		go func(item pair) {
			defer wg.Done()
			defer func() { <-sem }()
			outcome, cause := c.loadOne(ctx, book, item.currency, item.market)
			mu.Lock()
			switch outcome {
			case outcomeSaved:
				result.Saved++
			case outcomeKept:
				result.Kept++
			case outcomeSkipped:
				result.Skipped++
			default:
				result.Failed++
				if cause != nil {
					result.Errors = append(result.Errors, Failure{Currency: item.currency, Market: item.market, Err: cause.Error()})
				}
			}
			mu.Unlock()
		}(item)
	}
	wg.Wait()
	return result, nil
}

type outcome int

const (
	outcomeFailed outcome = iota
	outcomeSaved
	outcomeKept
	outcomeSkipped
)

func (c *Client) loadOne(ctx context.Context, book Book, currency, marketName string) (outcome, error) {
	current, err := book.Get(ctx, currency, marketName)
	if err != nil && !errors.Is(err, ErrMiss) {
		return outcomeFailed, err
	}
	if err == nil && c.now().Before(current.HoldUntil) {
		return outcomeKept, nil
	}
	quote, err := c.quote(ctx, currency, marketName)
	if errors.Is(err, errUnsupported) {
		return outcomeSkipped, nil
	}
	if err != nil {
		return outcomeFailed, err
	}
	// The admin price may have been stored while this quote was in flight.
	held, err := book.Get(ctx, currency, marketName)
	if err != nil && !errors.Is(err, ErrMiss) {
		return outcomeFailed, err
	}
	if err == nil && c.now().Before(held.HoldUntil) {
		return outcomeKept, nil
	}
	if err := book.Put(ctx, currency, marketName, quote); err != nil {
		return outcomeFailed, err
	}
	return outcomeSaved, nil
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) quote(ctx context.Context, currency, marketName string) (Quote, error) {
	switch marketName {
	case "bybit":
		return c.bybit(ctx, currency)
	case "rapira_rates":
		return c.rapiraRates(ctx, currency)
	case "rapira":
		return c.rapiraPlate(ctx, currency)
	case "rapira_top1_plus10":
		return c.top1Plus10(ctx, currency)
	case "rapira_top1_red":
		return c.top1Red(ctx, currency)
	default:
		return Quote{}, errUnsupported
	}
}

func (c *Client) bybit(ctx context.Context, currency string) (Quote, error) {
	buy, err := c.bybitSide(ctx, currency, true)
	if err != nil {
		return Quote{}, err
	}
	sell, err := c.bybitSide(ctx, currency, false)
	if err != nil {
		return Quote{}, err
	}
	return unitsQuote(buy, sell, currency)
}

func (c *Client) bybitSide(ctx context.Context, currency string, buy bool) (decimal.Decimal, error) {
	side := "0"
	if buy {
		side = "1"
	}
	payload := map[string]any{
		"userId":     "",
		"tokenId":    "USDT",
		"currencyId": strings.ToUpper(currency),
		"payment":    []any{},
		"side":       side,
		"size":       strconvI(bybitAds),
		"page":       "1",
		"amount":     "",
		"authMaker":  false,
		"canTrade":   false,
	}
	var response struct {
		Result struct {
			Items []struct {
				Price json.Number `json:"price"`
			} `json:"items"`
		} `json:"result"`
	}
	if err := c.postJSON(ctx, c.BybitURL, payload, map[string]string{"Accept": "*/*"}, &response); err != nil {
		return decimal.Decimal{}, err
	}
	numbers := make([]json.Number, 0, len(response.Result.Items))
	for _, item := range response.Result.Items {
		numbers = append(numbers, item.Price)
	}
	if len(numbers) > bybitAds {
		numbers = numbers[:bybitAds]
	}
	return meanNumbers(numbers, true)
}

func (c *Client) rapiraRates(ctx context.Context, currency string) (Quote, error) {
	if currency != "rub" {
		return c.binancePair(ctx, currency)
	}
	var response struct {
		Data []struct {
			Symbol string      `json:"symbol"`
			Close  json.Number `json:"close"`
			Ask    json.Number `json:"askPrice"`
			Bid    json.Number `json:"bidPrice"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, c.RapiraRatesURL, &response); err != nil {
		return Quote{}, err
	}
	for _, rate := range response.Data {
		if rate.Symbol != "USDT/RUB" {
			continue
		}
		ask, err := numberOr(rate.Ask, rate.Close)
		if err != nil {
			return Quote{}, err
		}
		bid, err := numberOr(rate.Bid, rate.Close)
		if err != nil {
			return Quote{}, err
		}
		return unitsQuote(ask, bid, currency)
	}
	return Quote{}, errEmpty
}

func (c *Client) rapiraPlate(ctx context.Context, currency string) (Quote, error) {
	if currency != "rub" {
		return c.binancePair(ctx, currency)
	}
	ask, bid, err := c.plate(ctx)
	if err != nil {
		return Quote{}, err
	}
	buy, err := meanNumbers(ask, false)
	if err != nil {
		return Quote{}, err
	}
	sell, err := meanNumbers(bid, false)
	if err != nil {
		return Quote{}, err
	}
	return unitsQuote(buy, sell, currency)
}

func (c *Client) top1Plus10(ctx context.Context, currency string) (Quote, error) {
	if currency != "rub" {
		return Quote{}, errUnsupported
	}
	ask, bid, err := c.plate(ctx)
	if err != nil {
		return Quote{}, err
	}
	if len(ask) == 0 || len(bid) == 0 {
		return Quote{}, errEmpty
	}
	buy, err := decimal.NewFromString(ask[0].String())
	if err != nil {
		return Quote{}, err
	}
	sell, err := decimal.NewFromString(bid[0].String())
	if err != nil {
		return Quote{}, err
	}
	extra := decimal.NewFromInt(10)
	return unitsQuote(buy.Add(extra), sell.Add(extra), currency)
}

func (c *Client) top1Red(ctx context.Context, currency string) (Quote, error) {
	if currency != "rub" {
		return Quote{}, errUnsupported
	}
	ask, bid, err := c.plate(ctx)
	if err != nil {
		return Quote{}, err
	}
	if len(ask) == 0 || len(bid) == 0 {
		return Quote{}, errEmpty
	}
	// RapiraTop1RedParser stores the bid as the buy price and the ask as the sell price.
	buy, err := decimal.NewFromString(bid[0].String())
	if err != nil {
		return Quote{}, err
	}
	sell, err := decimal.NewFromString(ask[0].String())
	if err != nil {
		return Quote{}, err
	}
	return unitsQuote(buy, sell, currency)
}

func (c *Client) plate(ctx context.Context) ([]json.Number, []json.Number, error) {
	var response struct {
		Ask struct {
			Items []struct {
				Price json.Number `json:"price"`
			} `json:"items"`
		} `json:"ask"`
		Bid struct {
			Items []struct {
				Price json.Number `json:"price"`
			} `json:"items"`
		} `json:"bid"`
	}
	if err := c.getJSON(ctx, c.PlateURL, &response); err != nil {
		return nil, nil, err
	}
	if len(response.Ask.Items) == 0 || len(response.Bid.Items) == 0 {
		return nil, nil, errEmpty
	}
	ask := make([]json.Number, 0, plateDepth)
	bid := make([]json.Number, 0, plateDepth)
	for i, item := range response.Ask.Items {
		if i >= plateDepth {
			break
		}
		ask = append(ask, item.Price)
	}
	for i, item := range response.Bid.Items {
		if i >= plateDepth {
			break
		}
		bid = append(bid, item.Price)
	}
	return ask, bid, nil
}

func (c *Client) binancePair(ctx context.Context, currency string) (Quote, error) {
	buy, err := c.binance(ctx, currency, "BUY")
	if err != nil {
		return Quote{}, err
	}
	sell, err := c.binance(ctx, currency, "SELL")
	if err != nil {
		return Quote{}, err
	}
	return unitsQuote(buy, sell, currency)
}

func (c *Client) binance(ctx context.Context, currency, tradeType string) (decimal.Decimal, error) {
	payload := map[string]any{
		"page":          1,
		"rows":          plateDepth,
		"payTypes":      []any{},
		"asset":         "USDT",
		"tradeType":     tradeType,
		"fiat":          strings.ToUpper(currency),
		"publisherType": nil,
	}
	var response struct {
		Data []struct {
			Adv struct {
				Price json.Number `json:"price"`
			} `json:"adv"`
		} `json:"data"`
	}
	headers := map[string]string{
		"Content-Type": "application/json",
		"User-Agent":   "Mozilla/5.0",
	}
	if err := c.postJSON(ctx, c.BinanceURL, payload, headers, &response); err != nil {
		return decimal.Decimal{}, err
	}
	numbers := make([]json.Number, 0, len(response.Data))
	for _, item := range response.Data {
		numbers = append(numbers, item.Adv.Price)
	}
	return meanNumbers(numbers, false)
}

func (c *Client) getJSON(ctx context.Context, url string, dest any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	return c.do(request, dest)
}

func (c *Client) postJSON(ctx context.Context, url string, payload any, headers map[string]string, dest any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	return c.do(request, dest)
}

func (c *Client) do(request *http.Request, dest any) error {
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("price http %d", response.StatusCode)
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return err
	}
	return nil
}

func unitsQuote(buy, sell decimal.Decimal, currency string) (Quote, error) {
	buyMoney, err := money.FromPrecision(buy.String(), currency)
	if err != nil {
		return Quote{}, err
	}
	sellMoney, err := money.FromPrecision(sell.String(), currency)
	if err != nil {
		return Quote{}, err
	}
	return Quote{BuyUnits: buyMoney.Units(), SellUnits: sellMoney.Units()}, nil
}

func meanNumbers(numbers []json.Number, round bool) (decimal.Decimal, error) {
	if len(numbers) == 0 {
		return decimal.Decimal{}, errEmpty
	}
	sum := decimal.Zero
	for _, number := range numbers {
		value, err := decimal.NewFromString(number.String())
		if err != nil {
			return decimal.Decimal{}, err
		}
		sum = sum.Add(value)
	}
	avg := sum.Div(decimal.NewFromInt(int64(len(numbers))))
	if round {
		return avg.Round(2), nil
	}
	return avg, nil
}

func numberOr(primary, fallback json.Number) (decimal.Decimal, error) {
	if primary.String() != "" {
		return decimal.NewFromString(primary.String())
	}
	if fallback.String() != "" {
		return decimal.NewFromString(fallback.String())
	}
	return decimal.Decimal{}, errEmpty
}

func strconvI(value int) string {
	return fmt.Sprintf("%d", value)
}
