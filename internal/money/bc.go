package money

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// bcScale is the division scale for money, 50 digits.
const bcScale int32 = 50

func bcAdd(a, b string) (string, error) {
	av, bv, err := parsePair(a, b)
	if err != nil {
		return "", err
	}
	return av.Add(bv).Truncate(bcScale).String(), nil
}

func bcSub(a, b string) (string, error) {
	av, bv, err := parsePair(a, b)
	if err != nil {
		return "", err
	}
	return av.Sub(bv).Truncate(bcScale).String(), nil
}

func bcMul(a, b string) (string, error) {
	av, bv, err := parsePair(a, b)
	if err != nil {
		return "", err
	}
	// Extra digits are cut. Money division does not round.
	return av.Mul(bv).Truncate(bcScale).String(), nil
}

func bcDiv(a, b string, scale int32) (string, error) {
	av, bv, err := parsePair(a, b)
	if err != nil {
		return "", err
	}
	if bv.Sign() == 0 {
		return "", fmt.Errorf("divide by zero: %w", ErrAmount)
	}
	// Extra digits then truncate, so the last kept digit is not rounded up.
	quot := av.DivRound(bv, scale+8).Truncate(scale)
	return quot.StringFixed(scale), nil
}

func bcCmp(a, b string) (int, error) {
	av, bv, err := parsePair(a, b)
	if err != nil {
		return 0, err
	}
	return av.Truncate(bcScale).Cmp(bv.Truncate(bcScale)), nil
}

func parsePair(a, b string) (decimal.Decimal, decimal.Decimal, error) {
	av, err := decimal.NewFromString(a)
	if err != nil {
		return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("%w: %s", ErrAmount, a)
	}
	bv, err := decimal.NewFromString(b)
	if err != nil {
		return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("%w: %s", ErrAmount, b)
	}
	return av, bv, nil
}
