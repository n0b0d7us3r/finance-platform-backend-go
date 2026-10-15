package money

import (
	"errors"
)

const defaultPrecision = 50

var (
	// ErrCurrencyUnknown is Currency::__construct.
	ErrCurrencyUnknown = errors.New("currency not exists")
	// ErrCurrencyMismatch is Money::throwIfCurrencyNotEqualsToBase.
	ErrCurrencyMismatch = errors.New("currencies must be equal")
	// ErrCurrencySame is Money::convertBack.
	ErrCurrencySame = errors.New("currencies must not be equal")
	// ErrAmount is a rejected numeric string.
	ErrAmount = errors.New("invalid amount")
)

// Money stores an integer subunit amount.
type Money struct {
	units    string
	currency Currency
}

// FromPrecision builds money from a decimal string such as "100.50".
func FromPrecision(amount string, code string) (Money, error) {
	currency, err := New(code)
	if err != nil {
		return Money{}, err
	}
	units, err := precisionToUnits(amount, currency.Precision())
	if err != nil {
		return Money{}, err
	}
	return Money{units: units, currency: currency}, nil
}

// FromUnits builds money from an integer subunit string such as "10050".
func FromUnits(amount string, code string) (Money, error) {
	currency, err := New(code)
	if err != nil {
		return Money{}, err
	}
	if amount == "" {
		return Money{}, ErrAmount
	}
	return Money{units: amount, currency: currency}, nil
}

// Zero is 0 in the given currency.
func Zero(code string) (Money, error) {
	return FromPrecision("0", code)
}

// Precision is the decimal form, "100.50".
func (m Money) Precision() (string, error) {
	return unitsToPrecision(m.units, m.currency.Precision())
}

// Units is the subunit form, "10050".
func (m Money) Units() string {
	return m.units
}

// Beauty trims trailing zeros, "100.5".
func (m Money) Beauty() (string, error) {
	precision, err := m.Precision()
	if err != nil {
		return "", err
	}
	return beautifyPrecision(precision), nil
}

// Currency returns the currency of the amount.
func (m Money) Currency() Currency {
	return m.currency
}

// Add adds a decimal string or, via AddMoney, another amount in the same currency.
func (m Money) Add(amount string) (Money, error) {
	precision, err := m.Precision()
	if err != nil {
		return Money{}, err
	}
	sum, err := bcAdd(precision, amount)
	if err != nil {
		return Money{}, err
	}
	return FromPrecision(sum, m.currency.Code())
}

// AddMoney adds an amount in the same currency.
func (m Money) AddMoney(other Money) (Money, error) {
	if err := m.sameCurrency(other); err != nil {
		return Money{}, err
	}
	precision, err := other.Precision()
	if err != nil {
		return Money{}, err
	}
	return m.Add(precision)
}

// Sub subtracts a decimal string.
func (m Money) Sub(amount string) (Money, error) {
	precision, err := m.Precision()
	if err != nil {
		return Money{}, err
	}
	diff, err := bcSub(precision, amount)
	if err != nil {
		return Money{}, err
	}
	return FromPrecision(diff, m.currency.Code())
}

// SubMoney subtracts an amount in the same currency.
func (m Money) SubMoney(other Money) (Money, error) {
	if err := m.sameCurrency(other); err != nil {
		return Money{}, err
	}
	precision, err := other.Precision()
	if err != nil {
		return Money{}, err
	}
	return m.Sub(precision)
}

// Mul multiplies by a decimal string.
func (m Money) Mul(amount string) (Money, error) {
	precision, err := m.Precision()
	if err != nil {
		return Money{}, err
	}
	product, err := bcMul(precision, amount)
	if err != nil {
		return Money{}, err
	}
	return FromPrecision(product, m.currency.Code())
}

// Div divides by a decimal string.
func (m Money) Div(amount string) (Money, error) {
	precision, err := m.Precision()
	if err != nil {
		return Money{}, err
	}
	quot, err := bcDiv(precision, amount, defaultPrecision)
	if err != nil {
		return Money{}, err
	}
	return FromPrecision(quot, m.currency.Code())
}

// DivMoney divides by an amount in the same currency.
func (m Money) DivMoney(other Money) (Money, error) {
	if err := m.sameCurrency(other); err != nil {
		return Money{}, err
	}
	precision, err := other.Precision()
	if err != nil {
		return Money{}, err
	}
	return m.Div(precision)
}

// Abs returns the absolute value.
func (m Money) Abs() (Money, error) {
	precision, err := m.Precision()
	if err != nil {
		return Money{}, err
	}
	if precision == "" || precision[0] != '-' {
		return m, nil
	}
	return FromPrecision(precision[1:], m.currency.Code())
}

// Convert divides by a rate in the same currency and re-denominates the units.
func (m Money) Convert(rate Money, code string) (Money, error) {
	if m.currency.Code() != rate.currency.Code() {
		return Money{}, ErrCurrencyMismatch
	}
	quot, err := m.DivMoney(rate)
	if err != nil {
		return Money{}, err
	}
	return FromUnits(quot.units, code)
}

// ConvertBack multiplies by a rate in another currency and re-denominates the units.
func (m Money) ConvertBack(rate Money, code string) (Money, error) {
	if m.currency.Code() == rate.currency.Code() {
		return Money{}, ErrCurrencySame
	}
	precision, err := rate.Precision()
	if err != nil {
		return Money{}, err
	}
	product, err := m.Mul(precision)
	if err != nil {
		return Money{}, err
	}
	return FromUnits(product.units, code)
}

// GreaterThan compares precision strings in the same currency.
func (m Money) GreaterThan(other Money) (bool, error) {
	cmp, err := m.cmp(other)
	if err != nil {
		return false, err
	}
	return cmp > 0, nil
}

// LessThan compares precision strings in the same currency.
func (m Money) LessThan(other Money) (bool, error) {
	cmp, err := m.cmp(other)
	if err != nil {
		return false, err
	}
	return cmp < 0, nil
}

// Equal reports equal amounts in the same currency.
func (m Money) Equal(other Money) (bool, error) {
	cmp, err := m.cmp(other)
	if err != nil {
		return false, err
	}
	return cmp == 0, nil
}

// GreaterOrEqual reports greater or equal amounts.
func (m Money) GreaterOrEqual(other Money) (bool, error) {
	cmp, err := m.cmp(other)
	if err != nil {
		return false, err
	}
	return cmp >= 0, nil
}

// LessOrEqual reports less or equal amounts.
func (m Money) LessOrEqual(other Money) (bool, error) {
	cmp, err := m.cmp(other)
	if err != nil {
		return false, err
	}
	return cmp <= 0, nil
}

// GreaterThanZero reports a positive amount.
func (m Money) GreaterThanZero() (bool, error) {
	zero, err := Zero(m.currency.Code())
	if err != nil {
		return false, err
	}
	return m.GreaterThan(zero)
}

// LessThanZero reports a negative amount.
func (m Money) LessThanZero() (bool, error) {
	zero, err := Zero(m.currency.Code())
	if err != nil {
		return false, err
	}
	return m.LessThan(zero)
}

// EqualZero reports a zero amount.
func (m Money) EqualZero() (bool, error) {
	zero, err := Zero(m.currency.Code())
	if err != nil {
		return false, err
	}
	return m.Equal(zero)
}

func (m Money) cmp(other Money) (int, error) {
	if err := m.sameCurrency(other); err != nil {
		return 0, err
	}
	left, err := m.Precision()
	if err != nil {
		return 0, err
	}
	right, err := other.Precision()
	if err != nil {
		return 0, err
	}
	return bcCmp(left, right)
}

func (m Money) sameCurrency(other Money) error {
	if m.currency.Code() != other.currency.Code() {
		return ErrCurrencyMismatch
	}
	return nil
}
