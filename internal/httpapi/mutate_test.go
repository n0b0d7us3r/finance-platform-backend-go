package httpapi

import (
	"encoding/json"
	"testing"
)

func TestUserEditReadsCabinetForm(t *testing.T) {
	raw := map[string]json.RawMessage{
		"name":                           json.RawMessage(`"Ada"`),
		"email":                          json.RawMessage(`"ada@demo.local"`),
		"role_id":                        json.RawMessage(`"2"`),
		"promo_code":                     json.RawMessage(`""`),
		"trader_category_id":             json.RawMessage(`"4"`),
		"banned":                         json.RawMessage(`false`),
		"payouts_enabled":                json.RawMessage(`true`),
		"stop_traffic":                   json.RawMessage(`false`),
		"referral_commission_percentage": json.RawMessage(`1.5`),
		"trader_commission_rate":         json.RawMessage(`""`),
		"target_reserve_amount":          json.RawMessage(`""`),
	}
	edit, fields := userEdit(raw)
	if len(fields) > 0 || !edit.CategorySet || edit.CategoryClear || edit.CategoryID != 4 || !edit.PromoSet || edit.Promo != "" || !edit.TraderRateClear || edit.ReserveSet || edit.RoleID != 2 {
		t.Fatalf("form edit %+v fields %v", edit, fields)
	}
	partial, fields := userEdit(map[string]json.RawMessage{
		"is_online":    json.RawMessage(`false`),
		"stop_traffic": json.RawMessage(`true`),
	})
	if len(fields) > 0 || partial.CategorySet || partial.NameSet || partial.Online == nil || *partial.Online || partial.Stop == nil || !*partial.Stop {
		t.Fatalf("partial edit %+v fields %v", partial, fields)
	}
}

func TestFlexBoolAcceptsCabinetFlags(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
		ok   bool
	}{
		{`1`, true, true},
		{`0`, false, true},
		{`true`, true, true},
		{`false`, false, true},
		{`"1"`, true, true},
		{`null`, false, false},
	}
	for _, tc := range cases {
		got, ok := flexBool(json.RawMessage(tc.raw))
		if ok != tc.ok || got != tc.want {
			t.Fatalf("%s: got %v %v", tc.raw, got, ok)
		}
	}
	online := flagPointer(json.RawMessage(`1`))
	payout := flagPointer(json.RawMessage(`false`))
	if online == nil || *online != 1 || payout == nil || *payout != 0 {
		t.Fatal("cabinet toggle payload was not accepted")
	}
}
