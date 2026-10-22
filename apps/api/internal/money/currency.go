package money

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

var ErrUnsupportedCurrency = errors.New("unsupported currency")

type currency struct {
	exponent int
	symbol   string
}

var currencies = map[string]currency{
	"ARS": {exponent: 2},
	"AUD": {exponent: 2, symbol: "AU$"},
	"BRL": {exponent: 2, symbol: "R$"},
	"CAD": {exponent: 2, symbol: "CA$"},
	"CHF": {exponent: 2},
	"CLP": {exponent: 0},
	"CNY": {exponent: 2},
	"COP": {exponent: 2},
	"EUR": {exponent: 2, symbol: "€"},
	"GBP": {exponent: 2, symbol: "£"},
	"JPY": {exponent: 0, symbol: "JP¥"},
	"KRW": {exponent: 0},
	"MXN": {exponent: 2},
	"PEN": {exponent: 2},
	"PYG": {exponent: 0},
	"USD": {exponent: 2, symbol: "US$"},
	"UYU": {exponent: 2},
}

func SupportedCurrencies() []string {
	codes := make([]string, 0, len(currencies))
	for code := range currencies {
		codes = append(codes, code)
	}
	slices.Sort(codes)
	return codes
}

func ValidCurrency(code string) error {
	_, err := exponentOf(code)
	return err
}

func exponentOf(code string) (int, error) {
	c, ok := currencies[code]
	if !ok {
		return 0, fmt.Errorf("%w %q: use an ISO 4217 code among %s", ErrUnsupportedCurrency, code, strings.Join(SupportedCurrencies(), ", "))
	}
	return c.exponent, nil
}

func symbolOf(code string) string {
	if symbol := currencies[code].symbol; symbol != "" {
		return symbol
	}
	return code
}
