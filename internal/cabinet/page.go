package cabinet

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"finance-platform/go/internal/money"
)

// Page is one Laravel resource page: data plus meta.current_page.
type Page struct {
	Number int
	Size   int
}

// FromRequest reads page and per_page. Size falls back when the query is empty or out of range.
func FromRequest(r *http.Request, fallback int) Page {
	page := atoi(r.URL.Query().Get("page"), 1)
	size := atoi(r.URL.Query().Get("per_page"), fallback)
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = fallback
	}
	if size < 1 {
		size = 15
	}
	return Page{Number: page, Size: size}
}

// Filter reads filters[name] the way the cabinet tables send it.
func Filter(r *http.Request, name string) string {
	return strings.TrimSpace(r.URL.Query().Get("filters[" + name + "]"))
}

func (p Page) offset() int {
	return (p.Number - 1) * p.Size
}

// Block is the paginated object nested under a list key.
func Block(page Page, total int, rows []map[string]any) map[string]any {
	if rows == nil {
		rows = []map[string]any{}
	}
	last := 1
	if total > 0 {
		last = (total + page.Size - 1) / page.Size
	}
	return map[string]any{
		"data": rows,
		"meta": map[string]any{
			"current_page": page.Number,
			"last_page":    last,
			"per_page":     page.Size,
			"total":        total,
		},
	}
}

func atoi(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func beauty(units, code string) string {
	if strings.TrimSpace(units) == "" {
		units = "0"
	}
	if code == "" {
		code = "usdt"
	}
	amount, err := money.FromUnits(units, code)
	if err != nil {
		return units
	}
	text, err := amount.Beauty()
	if err != nil {
		return units
	}
	return text
}

func clock(value time.Time, loc *time.Location) string {
	if loc != nil {
		value = value.In(loc)
	}
	return value.Format("2006-01-02 15:04:05")
}

func day(value time.Time, loc *time.Location) string {
	if loc != nil {
		value = value.In(loc)
	}
	return value.Format("2006-01-02")
}
