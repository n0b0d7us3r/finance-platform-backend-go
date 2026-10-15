package schedule

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const methodsKey = "currencies.price-parsers.methods-list"
const methodsTTL = 24 * time.Hour

// MethodCache stores the Bybit payment-method list.
type MethodCache interface {
	Load(ctx context.Context) (bool, error)
}

// BybitMethods fetches the public method list and keeps the previous value on failure.
type BybitMethods struct {
	Client *goredis.Client
	Prefix string
	HTTP   *http.Client
}

func (b BybitMethods) Load(ctx context.Context) (bool, error) {
	if b.Client == nil {
		return false, nil
	}
	body, err := b.fetch(ctx)
	if err != nil {
		return false, err
	}
	if err := b.Client.Set(ctx, b.Prefix+methodsKey, body, methodsTTL).Err(); err != nil {
		return false, err
	}
	return true, nil
}

func (b BybitMethods) fetch(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api2.bybit.com/fiat/otc/configuration/queryAllPaymentList", bytes.NewReader(nil))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "PostmanRuntime/7.42.0")
	client := b.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("bybit status %d", res.StatusCode)
	}
	var payload struct {
		RetMsg  string          `json:"ret_msg"`
		Result  json.RawMessage `json:"result"`
		ExtInfo json.RawMessage `json:"ext_info"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if payload.RetMsg != "SUCCESS" {
		return nil, fmt.Errorf("bybit %s", payload.ExtInfo)
	}
	if len(payload.Result) == 0 || string(payload.Result) == "null" {
		return nil, fmt.Errorf("bybit empty result")
	}
	return payload.Result, nil
}
