package cascade

import "testing"

func TestSelectFlagAndOrder(t *testing.T) {
	terminals := []Terminal{
		{ID: 2, Rate: 12.5, Min: "1000", Max: "9999", DetailTypes: []string{"card"}, Trust: "10"},
		{ID: 1, Rate: 9, Min: "100", Max: "500000", DetailTypes: []string{"card"}, Trust: "10"},
		{ID: 3, Rate: 1, Min: "100", Max: "500000", DetailTypes: []string{"phone"}, Trust: "10"},
		{ID: 4, Rate: 1, Min: "100", Max: "500000", DetailTypes: []string{"card"}, Trust: "0"},
	}
	if got := Select(false, terminals, "1500.00", "card"); len(got) != 0 {
		t.Fatalf("disabled flag returned %d terminals", len(got))
	}
	got := Select(true, terminals, "1500.00", "card")
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("order = %#v", ids(got))
	}
	if Select(true, terminals, "50", "card") != nil {
		t.Fatal("amount below every minimum was kept")
	}
}

func TestParseDetailID(t *testing.T) {
	if _, _, err := ParseDetailID("", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ParseDetailID("", true); err == nil {
		t.Fatal("enabled flag accepted an empty id")
	}
	if _, _, err := ParseDetailID("abc", false); err == nil {
		t.Fatal("non-numeric id was accepted")
	}
	if _, _, err := ParseDetailID("0", true); err == nil {
		t.Fatal("zero id was accepted")
	}
	id, check, err := ParseDetailID("6", true)
	if err != nil || !check || id != 6 {
		t.Fatalf("id=%d check=%v err=%v", id, check, err)
	}
}

func ids(terminals []Terminal) []int64 {
	var out []int64
	for _, terminal := range terminals {
		out = append(out, terminal.ID)
	}
	return out
}
