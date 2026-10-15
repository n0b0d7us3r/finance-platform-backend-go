package schedule

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"finance-platform/go/internal/flow"

	"github.com/shopspring/decimal"
)

const usdtContract = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"

// Deposits polls TronGrid for recent trust deposits. An empty key skips the call.
type Deposits struct {
	Key    string
	Orders *flow.Service
	Client *http.Client
}

func (d Deposits) Enabled() bool {
	return strings.TrimSpace(d.Key) != "" && d.Orders != nil
}

type pendingDeposit struct {
	id       int64
	units    string
	created  time.Time
	walletID int64
	balance  string
}

func (d Deposits) Check(ctx context.Context, db *sql.DB, now time.Time) (int, error) {
	if db == nil {
		return 0, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, amount, created_at, wallet_id, balance_type
		FROM invoices
		WHERE type = 'deposit' AND status = 'pending' AND balance_type = 'trust' AND currency = 'usdt'
		  AND wallet_id IS NOT NULL AND created_at >= ?`, now.Add(-40*time.Minute))
	if err != nil {
		return 0, err
	}
	var pending []pendingDeposit
	for rows.Next() {
		var item pendingDeposit
		if err := rows.Scan(&item.id, &item.units, &item.created, &item.walletID, &item.balance); err != nil {
			rows.Close()
			return 0, err
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(pending) == 0 {
		return 0, nil
	}
	address, err := platformWallet(ctx, db)
	if err != nil {
		return 0, err
	}
	if address == "" {
		return 0, nil
	}
	transfers, err := d.transfers(ctx, address)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, transfer := range transfers {
		units, err := tronInvoiceUnits(transfer.value, transfer.decimals)
		if err != nil {
			continue
		}
		for _, invoice := range pending {
			if invoice.units != units || transfer.at.Before(invoice.created) {
				continue
			}
			if err := d.Orders.SettleDeposit(ctx, invoice.id, transfer.id, invoice.units, invoice.balance, invoice.walletID); err != nil {
				return done, err
			}
			done++
		}
	}
	return done, nil
}

func platformWallet(ctx context.Context, db *sql.DB) (string, error) {
	var value sql.NullString
	err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE `key` = 'platform_wallet' ORDER BY id LIMIT 1").Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(value.String), nil
}

type chainTransfer struct {
	id       string
	value    string
	decimals int
	at       time.Time
}

func (d Deposits) transfers(ctx context.Context, address string) ([]chainTransfer, error) {
	query := url.Values{}
	query.Set("only_confirmed", "true")
	query.Set("limit", "20")
	query.Set("contract_address", usdtContract)
	query.Set("only_to", "true")
	endpoint := "https://api.trongrid.io/v1/accounts/" + url.PathEscape(address) + "/transactions/trc20?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("TRON-PRO-API-KEY", d.Key)
	req.Header.Set("Accept", "application/json")
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("trongrid status %d", res.StatusCode)
	}
	var payload struct {
		Data []struct {
			Value         string `json:"value"`
			TransactionID string `json:"transaction_id"`
			BlockTime     int64  `json:"block_timestamp"`
			TokenInfo     struct {
				Decimals int `json:"decimals"`
			} `json:"token_info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	out := make([]chainTransfer, 0, len(payload.Data))
	for _, item := range payload.Data {
		out = append(out, chainTransfer{
			id:       item.TransactionID,
			value:    item.Value,
			decimals: item.TokenInfo.Decimals,
			at:       time.UnixMilli(item.BlockTime),
		})
	}
	return out, nil
}

// tronInvoiceUnits repeats the deposit command: divide the chain value by 10^(decimals-2)
// and keep the whole number.
func tronInvoiceUnits(raw string, tokenDecimals int) (string, error) {
	value, err := decimal.NewFromString(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	actual := value.Div(decimal.New(1, int32(tokenDecimals-2))).Truncate(0)
	return actual.String(), nil
}
