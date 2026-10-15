package money

import "strings"

// precisionToUnits turns a decimal string into subunits.
// Extra fractional digits past divisibility are cut, not rounded.
func precisionToUnits(amount string, divisibility int) (string, error) {
	if amount == "" {
		return "", ErrAmount
	}
	negative := amount[0] == '-'
	if negative {
		amount = strings.TrimLeft(amount, "-")
		if amount == "" {
			return "", ErrAmount
		}
	}

	parts := strings.Split(amount, ".")
	whole := parts[0]
	frac := ""
	if len(parts) > 1 {
		frac = parts[1]
	}
	if len(frac) < divisibility {
		frac += strings.Repeat("0", divisibility-len(frac))
	}
	if divisibility > 0 {
		frac = frac[:divisibility]
	} else {
		frac = ""
	}

	combined := strings.TrimLeft(whole+frac, "0")
	if combined == "" {
		combined = "0"
	}
	if negative {
		combined = "-" + combined
	}
	return combined, nil
}

// unitsToPrecision turns subunits into a decimal string with the currency scale.
func unitsToPrecision(amount string, divisibility int) (string, error) {
	if amount == "-0" {
		amount = "0"
	}
	divisor := "1" + strings.Repeat("0", divisibility)
	return bcDiv(amount, divisor, int32(divisibility))
}

// beautifyPrecision trims trailing zeros from a decimal string.
func beautifyPrecision(amount string) string {
	if !strings.Contains(amount, ".") {
		return amount
	}
	amount = strings.TrimRight(amount, "0")
	amount = strings.TrimRight(amount, ".")
	if amount == "" || amount == "-" {
		return "0"
	}
	return amount
}
