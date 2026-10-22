package money_test

import (
	"errors"
	"testing"

	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

func TestParseAcceptsCanonicalDecimals(t *testing.T) {
	tests := []struct {
		amount   string
		currency string
		want     int64
	}{
		{"327.39", "BRL", 32739},
		{"1200", "BRL", 120000},
		{"1200.5", "BRL", 120050},
		{"0.01", "BRL", 1},
		{"0", "BRL", 0},
		{"-30.00", "BRL", -3000},
		{"999999999999999.99", "BRL", 99999999999999999},
		{"19.99", "USD", 1999},
		{"1500", "CLP", 1500},
		{"1500", "JPY", 1500},
	}
	for _, tt := range tests {
		t.Run(tt.amount+" "+tt.currency, func(t *testing.T) {
			got, err := money.Parse(tt.amount, tt.currency)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got.Minor != tt.want || got.Currency != tt.currency {
				t.Errorf("Parse() = %+v, want %d %s", got, tt.want, tt.currency)
			}
		})
	}
}

func TestParseRejectsEverythingButTheCanonicalForm(t *testing.T) {
	tests := map[string]string{
		"brazilian format":       "327,39",
		"currency symbol":        "R$ 327,39",
		"thousands separator":    "1.200,00",
		"comma thousands":        "1,200.00",
		"surrounding spaces":     " 327.39 ",
		"plus sign":              "+327.39",
		"leading zero":           "0327.39",
		"trailing dot":           "327.",
		"leading dot":            ".39",
		"too many decimals":      "327.391",
		"empty":                  "",
		"words":                  "trezentos",
		"too many digits":        "1234567890123456.00",
		"exponent notation":      "3.2739e2",
		"decimals on zero cents": "1500.5",
	}
	for name, amount := range tests {
		t.Run(name, func(t *testing.T) {
			currency := "BRL"
			if name == "decimals on zero cents" {
				currency = "JPY"
			}
			_, err := money.Parse(amount, currency)
			var parseErr *money.ParseError
			if !errors.As(err, &parseErr) {
				t.Errorf("Parse(%q) error = %v, want a ParseError", amount, err)
			}
		})
	}
}

func TestParseRejectsUnknownCurrencies(t *testing.T) {
	for _, currency := range []string{"", "brl", "R$", "XYZ"} {
		if _, err := money.Parse("1.00", currency); !errors.Is(err, money.ErrUnsupportedCurrency) {
			t.Errorf("Parse(%q) error = %v, want ErrUnsupportedCurrency", currency, err)
		}
	}
}

func TestFormatUsesBrazilianConventions(t *testing.T) {
	tests := []struct {
		minor    int64
		currency string
		want     string
	}{
		{32739, "BRL", "R$ 327,39"},
		{120000, "BRL", "R$ 1.200,00"},
		{123456789, "BRL", "R$ 1.234.567,89"},
		{5, "BRL", "R$ 0,05"},
		{0, "BRL", "R$ 0,00"},
		{-3000, "BRL", "-R$ 30,00"},
		{1999, "USD", "US$ 19,99"},
		{1500000, "CLP", "CLP 1.500.000"},
		{1050, "EUR", "€ 10,50"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := (money.Money{Minor: tt.minor, Currency: tt.currency}).Format(); got != tt.want {
				t.Errorf("Format() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecimalRoundTripsThroughParse(t *testing.T) {
	for _, amount := range []string{"327.39", "0.05", "-30.00", "1200.00"} {
		parsed, err := money.Parse(amount, "BRL")
		if err != nil {
			t.Fatal(err)
		}
		if got := parsed.Decimal(); got != amount {
			t.Errorf("Parse(%q).Decimal() = %q", amount, got)
		}
	}
	if got := (money.Money{Minor: 1500, Currency: "JPY"}).Decimal(); got != "1500" {
		t.Errorf("JPY Decimal() = %q, want 1500", got)
	}
}

func TestAddAndSubKeepTheCurrency(t *testing.T) {
	purchase := money.Money{Minor: 10000, Currency: "BRL"}
	refund := money.Money{Minor: 3000, Currency: "BRL"}

	net, err := purchase.Sub(refund)
	if err != nil || net != (money.Money{Minor: 7000, Currency: "BRL"}) {
		t.Errorf("Sub() = %+v, %v, want 70.00 BRL", net, err)
	}
	total, err := purchase.Add(refund)
	if err != nil || total.Minor != 13000 {
		t.Errorf("Add() = %+v, %v, want 130.00 BRL", total, err)
	}
}

func TestCombiningDifferentCurrenciesFailsWithoutConversion(t *testing.T) {
	brl := money.Money{Minor: 10000, Currency: "BRL"}
	usd := money.Money{Minor: 2000, Currency: "USD"}

	if _, err := brl.Add(usd); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Errorf("Add(BRL, USD) error = %v, want ErrCurrencyMismatch", err)
	}
	if _, err := brl.Sub(usd); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Errorf("Sub(BRL, USD) error = %v, want ErrCurrencyMismatch", err)
	}
}

func TestAddDetectsOverflow(t *testing.T) {
	huge := money.Money{Minor: 1<<63 - 1, Currency: "BRL"}
	if _, err := huge.Add(money.Money{Minor: 1, Currency: "BRL"}); !errors.Is(err, money.ErrOverflow) {
		t.Errorf("Add() error = %v, want ErrOverflow", err)
	}
	if _, err := huge.Sub(money.Money{Minor: -1 << 63, Currency: "BRL"}); !errors.Is(err, money.ErrOverflow) {
		t.Errorf("Sub(min) error = %v, want ErrOverflow", err)
	}
}

func TestFormatHandlesTheSmallestAmount(t *testing.T) {
	if got := (money.Money{Minor: -1 << 63, Currency: "BRL"}).Decimal(); got != "-92233720368547758.08" {
		t.Errorf("Decimal(min) = %q", got)
	}
}
