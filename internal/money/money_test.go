package money

import "testing"

func TestPrecisionRoundTrip(t *testing.T) {
	tests := []struct {
		in      string
		units   string
		precise string
		beauty  string
	}{
		{in: "100.50", units: "10050", precise: "100.50", beauty: "100.5"},
		{in: "100.5", units: "10050", precise: "100.50", beauty: "100.5"},
		{in: "0.5", units: "50", precise: "0.50", beauty: "0.5"},
		{in: "0", units: "0", precise: "0.00", beauty: "0"},
		{in: "-1.2", units: "-120", precise: "-1.20", beauty: "-1.2"},
		{in: "100.505", units: "10050", precise: "100.50", beauty: "100.5"},
		{in: "10", units: "1000", precise: "10.00", beauty: "10"},
		{in: "0.001", units: "0", precise: "0.00", beauty: "0"},
		{in: "-0.50", units: "-50", precise: "-0.50", beauty: "-0.5"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := FromPrecision(tt.in, "RUB")
			if err != nil {
				t.Fatal(err)
			}
			if got.Units() != tt.units {
				t.Fatalf("units %s, want %s", got.Units(), tt.units)
			}
			precise, err := got.Precision()
			if err != nil {
				t.Fatal(err)
			}
			if precise != tt.precise {
				t.Fatalf("precision %s, want %s", precise, tt.precise)
			}
			beauty, err := got.Beauty()
			if err != nil {
				t.Fatal(err)
			}
			if beauty != tt.beauty {
				t.Fatalf("beauty %s, want %s", beauty, tt.beauty)
			}
		})
	}
}

func TestArithmeticMatchesBCMathScale(t *testing.T) {
	base, err := FromPrecision("100.50", "rub")
	if err != nil {
		t.Fatal(err)
	}
	added, err := base.Add("0.005")
	if err != nil {
		t.Fatal(err)
	}
	if added.Units() != "10050" {
		t.Fatalf("add units %s", added.Units())
	}
	subbed, err := base.Sub("0.005")
	if err != nil {
		t.Fatal(err)
	}
	if subbed.Units() != "10049" {
		t.Fatalf("sub units %s", subbed.Units())
	}
	product, err := base.Mul("1.5")
	if err != nil {
		t.Fatal(err)
	}
	if product.Units() != "15075" {
		t.Fatalf("mul units %s", product.Units())
	}
	hundred, err := FromPrecision("100", "RUB")
	if err != nil {
		t.Fatal(err)
	}
	quot, err := hundred.Div("3")
	if err != nil {
		t.Fatal(err)
	}
	if quot.Units() != "3333" {
		t.Fatalf("div units %s", quot.Units())
	}
	precise, err := quot.Precision()
	if err != nil {
		t.Fatal(err)
	}
	if precise != "33.33" {
		t.Fatalf("div precision %s", precise)
	}
}

func TestCompareAndCurrencyGuard(t *testing.T) {
	left, err := FromPrecision("100.50", "RUB")
	if err != nil {
		t.Fatal(err)
	}
	same, err := FromPrecision("100.5", "RUB")
	if err != nil {
		t.Fatal(err)
	}
	equal, err := left.Equal(same)
	if err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Fatal("100.50 and 100.5 differ")
	}
	other, err := FromPrecision("1", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := left.GreaterThan(other); err != ErrCurrencyMismatch {
		t.Fatalf("got %v", err)
	}
	positive, err := left.GreaterThanZero()
	if err != nil || !positive {
		t.Fatalf("positive %v %v", positive, err)
	}
}

func TestCatalogueSkipsBase(t *testing.T) {
	all := All()
	if len(all) != 13 {
		t.Fatalf("len %d", len(all))
	}
	if all[0].Code() != "rub" || all[0].Subunit() != 100 {
		t.Fatalf("first %s", all[0].Code())
	}
	for _, item := range all {
		if item.Code() == "usdt" {
			t.Fatal("usdt is base and must stay out of getAll")
		}
	}
	if _, err := New("nope"); err != ErrCurrencyUnknown {
		t.Fatalf("got %v", err)
	}
	usdt, err := New("usdt")
	if err != nil {
		t.Fatal(err)
	}
	if !usdt.Base() || usdt.Code() != "usdt" {
		t.Fatalf("usdt %+v", usdt)
	}
}
