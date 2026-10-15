package money

import "strings"

// Currency is a code from the platform catalogue.
type Currency struct {
	code string
}

// New rejects a code that is not in the catalogue.
// FR-style message matches Currency::__construct: "Currency not exists".
func New(code string) (Currency, error) {
	upper := strings.ToUpper(strings.TrimSpace(code))
	if _, ok := byCode[upper]; !ok {
		return Currency{}, ErrCurrencyUnknown
	}
	return Currency{code: upper}, nil
}

// IsKnown reports whether code is in the catalogue.
func IsKnown(code string) bool {
	_, ok := byCode[strings.ToUpper(strings.TrimSpace(code))]
	return ok
}

// Code is the lowercase code stored on orders and returned by V1.
func (c Currency) Code() string {
	return strings.ToLower(c.code)
}

// Precision is the number of decimal places (2 for every catalogue currency).
func (c Currency) Precision() int {
	return byCode[c.code].precision
}

// Subunit is the number of minor units in one major unit.
func (c Currency) Subunit() int {
	return byCode[c.code].subunit
}

// Symbol is the display symbol.
func (c Currency) Symbol() string {
	return byCode[c.code].symbol
}

// Name is the display name.
func (c Currency) Name() string {
	return byCode[c.code].name
}

// Base reports the platform unit. USDT is the only base currency.
func (c Currency) Base() bool {
	return byCode[c.code].base
}

// All returns non-base currencies in catalogue order.
// USDT is omitted, same as Currency::getAll.
func All() []Currency {
	out := make([]Currency, 0, len(catalogue))
	for _, item := range catalogue {
		if item.base {
			continue
		}
		out = append(out, Currency{code: item.code})
	}
	return out
}

type currencySpec struct {
	code      string
	precision int
	subunit   int
	base      bool
	symbol    string
	name      string
}

// Catalogue order is the public currency list. UZS is not included.
var catalogue = []currencySpec{
	{code: "RUB", precision: 2, subunit: 100, symbol: "₽", name: "Российский рубль"},
	{code: "KZT", precision: 2, subunit: 100, symbol: "₸", name: "Казахстанский тенге"},
	{code: "BYN", precision: 2, subunit: 100, symbol: "Br", name: "Белорусский рубль"},
	{code: "EUR", precision: 2, subunit: 100, symbol: "€", name: "Е́вро"},
	{code: "TJS", precision: 2, subunit: 100, symbol: "с", name: "Сомони́"},
	{code: "KGS", precision: 2, subunit: 100, symbol: "Сом", name: "Сом"},
	{code: "UAH", precision: 2, subunit: 100, symbol: "₴", name: "Гривна"},
	{code: "USD", precision: 2, subunit: 100, symbol: "$", name: "Доллар США"},
	{code: "AZN", precision: 2, subunit: 100, symbol: "₼", name: "Мана́т"},
	{code: "THB", precision: 2, subunit: 100, symbol: "฿", name: "Тайский бат"},
	{code: "TRY", precision: 2, subunit: 100, symbol: "₺", name: "Турецкая лира"},
	{code: "IDR", precision: 2, subunit: 100, symbol: "Rp", name: "Рупия"},
	{code: "AED", precision: 2, subunit: 100, symbol: "د. إ.", name: "Дирха́м ОАЭ"},
	{code: "USDT", precision: 2, subunit: 100, base: true, symbol: "USDT", name: "Tether (USDT)"},
}

var byCode = func() map[string]currencySpec {
	out := make(map[string]currencySpec, len(catalogue))
	for _, item := range catalogue {
		out[item.code] = item
	}
	return out
}()
