package cashout

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"finance-platform/go/internal/cascade"
)

func TestOpenAndParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/merchants/transactions/create-manual" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret-key" {
			t.Errorf("authorization was forwarded wrong")
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"amount":1500`)) || !bytes.Contains(body, []byte(`"paymentMethod":"CARD"`)) || !bytes.Contains(body, []byte(`"currency":"RUB"`)) {
			t.Fatalf("request %s", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"status":        "pending",
				"transactionId": "tx-1",
				"paymentDetails": map[string]any{
					"cardNumber": "5555444433331111",
					"cardHolder": "CASCADE HOLDER",
				},
			},
		})
	}))
	defer server.Close()
	settings, _ := json.Marshal(map[string]string{"api_url": server.URL, "api_key": "secret-key"})
	quote, err := Open(context.Background(), server.Client(), cascade.Terminal{
		Integration: "DEMO_CARD",
		Settings:    settings,
		Retries:     1,
		TimeoutMS:   1000,
	}, cascade.Request{OrderUUID: "order-1", Amount: "1500.00", Currency: "rub", DetailType: "card"})
	if err != nil {
		t.Fatal(err)
	}
	if quote.Detail != "5555444433331111" || quote.Initials != "CASCADE HOLDER" || quote.ProviderOrderID != "tx-1" {
		t.Fatalf("%#v", quote)
	}
	if _, err := Open(context.Background(), nil, cascade.Terminal{Integration: "DEMO_SBP"}, cascade.Request{}); err != cascade.ErrSkip {
		t.Fatalf("sbp err %v", err)
	}

	raw := []byte(`{"event":"transaction.status_changed","transactionId":"tx-1","merchantTransactionId":"order-1","status":"completed"}`)
	parsed, err := Parse(raw, "", "", "")
	if err != nil || parsed.Action != "success" || parsed.Sub != "successfully_paid" || parsed.OrderUUID != "order-1" {
		t.Fatalf("%#v %v", parsed, err)
	}
	mac := hmac.New(sha256.New, []byte("hook"))
	_, _ = mac.Write(raw)
	header := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if _, err := Parse(raw, "", header, "hook"); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(raw, "", "sha256=00", "hook"); !SignatureError(err) {
		t.Fatal(err)
	}
	if _, err := Parse([]byte(`{"event":"other"}`), "", "", ""); !UnknownEvent(err) {
		t.Fatal(err)
	}
	pending := []byte(`{"event":"transaction.status_changed","transactionId":"tx-1","merchantTransactionId":"order-1","status":"pending"}`)
	parsed, err = Parse(pending, "transaction.status_changed", "", "")
	if err != nil || parsed.Action != "" {
		t.Fatalf("pending %#v %v", parsed, err)
	}
}
