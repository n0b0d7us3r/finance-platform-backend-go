package schedule

import "testing"

func TestTronInvoiceUnits(t *testing.T) {
	got, err := tronInvoiceUnits("1000000", 6)
	if err != nil {
		t.Fatal(err)
	}
	if got != "100" {
		t.Fatalf("got %s", got)
	}
}
