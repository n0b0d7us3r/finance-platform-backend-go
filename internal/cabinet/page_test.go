package cabinet

import "testing"

func TestBlock(t *testing.T) {
	got := Block(Page{Number: 2, Size: 15}, 40, nil)
	meta := got["meta"].(map[string]any)
	if meta["current_page"] != 2 || meta["last_page"] != 3 || meta["total"] != 40 {
		t.Fatalf("%#v", meta)
	}
	if len(got["data"].([]map[string]any)) != 0 {
		t.Fatal("nil rows became a nil data field")
	}
}
