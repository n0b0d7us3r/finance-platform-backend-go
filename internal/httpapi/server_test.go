package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

func TestTraderCabinetPreflight(t *testing.T) {
	handler := NewHandler(Deps{})
	request := httptest.NewRequest(http.MethodOptions, "/api/frontend/trader/payment-details?page=1", nil)
	request.Header.Set("Origin", "http://127.0.0.1:3002")
	request.Header.Set("Access-Control-Request-Method", "GET")
	request.Header.Set("Access-Control-Request-Headers", "access-token,authorization,content-type")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status %d", recorder.Code)
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:3002" {
		t.Fatalf("origin %q", recorder.Header().Get("Access-Control-Allow-Origin"))
	}

	foreign := httptest.NewRequest(http.MethodOptions, "/api/frontend/trader/currencies", nil)
	foreign.Header.Set("Origin", "https://evil.example")
	foreign.Header.Set("Access-Control-Request-Method", "GET")
	blocked := httptest.NewRecorder()
	handler.ServeHTTP(blocked, foreign)
	if blocked.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("foreign origin %q", blocked.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestUp(t *testing.T) {
	response := call(t, NewHandler(Deps{}), http.MethodGet, "/up", "")
	if response.Code != http.StatusOK || response.Body.String() != "ok" {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
}

func TestCurrenciesRequireToken(t *testing.T) {
	handler := NewHandler(Deps{Tokens: allowToken{ok: false}})
	response := call(t, handler, http.MethodGet, "/api/v1/currencies", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status %d", response.Code)
	}
	if response.Body.String() != `{"success":false,"message":"Invalid Access Token."}` {
		t.Fatalf("body %s", response.Body.String())
	}
}

func TestCurrenciesMatchCatalogue(t *testing.T) {
	handler := NewHandler(Deps{Tokens: allowToken{ok: true, id: 7}})
	response := call(t, handler, http.MethodGet, "/api/v1/currencies", "secret")
	if response.Code != http.StatusOK {
		t.Fatalf("status %d body %s", response.Code, response.Body.String())
	}
	want, err := os.ReadFile("testdata/currencies.json")
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, response.Body.Bytes(), want) {
		t.Fatalf("body\n%s\nwant\n%s", response.Body.String(), want)
	}
}

func jsonEqual(t *testing.T, got, want []byte) bool {
	t.Helper()
	var left, right any
	if err := json.Unmarshal(got, &left); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &right); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(left, right)
}

func TestOrderUUIDMiss(t *testing.T) {
	handler := NewHandler(Deps{Tokens: allowToken{ok: true, id: 3}})
	response := call(t, handler, http.MethodGet, "/api/v1/order/not-a-uuid", "secret")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status %d body %s", response.Code, response.Body.String())
	}
	if response.Body.String() != `{"message":"No query results for model [App\\Models\\Order]."}` {
		t.Fatalf("body %s", response.Body.String())
	}
}

func TestReady(t *testing.T) {
	okHandler := NewHandler(Deps{MySQL: pingOK{}, Redis: pingOK{}, PriceRedis: pingOK{}})
	response := call(t, okHandler, http.MethodGet, "/ready", "")
	if response.Code != http.StatusOK {
		t.Fatalf("ready %d %s", response.Code, response.Body.String())
	}

	down := NewHandler(Deps{MySQL: pingFail{}, Redis: pingOK{}})
	response = call(t, down, http.MethodGet, "/ready", "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("down %d %s", response.Code, response.Body.String())
	}
}

func call(t *testing.T, handler http.Handler, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	if token != "" {
		request.Header.Set("Access-Token", token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

type allowToken struct {
	ok bool
	id int64
}

func (a allowToken) Resolve(context.Context, string) (int64, bool, error) {
	return a.id, a.ok, nil
}

type pingOK struct{}

func (pingOK) Ping(context.Context) error { return nil }

type pingFail struct{}

func (pingFail) Ping(context.Context) error { return context.DeadlineExceeded }
