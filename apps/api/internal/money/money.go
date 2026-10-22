package money

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxIntegerDigits = 15
	minInt64         = -1 << 63
)

var (
	ErrCurrencyMismatch = errors.New("amounts in different currencies cannot be combined")
	ErrOverflow         = errors.New("amount is out of range")

	canonicalAmount = regexp.MustCompile(`^(-?)(0|[1-9][0-9]*)(?:\.([0-9]+))?$`)
)

type Money struct {
	Minor    int64
	Currency string
}

type ParseError struct {
	Amount  string
	Message string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("amount %q %s", e.Amount, e.Message)
}

func Parse(amount, currency string) (Money, error) {
	exponent, err := exponentOf(currency)
	if err != nil {
		return Money{}, err
	}
	match := canonicalAmount.FindStringSubmatch(amount)
	if match == nil {
		return Money{}, &ParseError{Amount: amount, Message: `must be a decimal such as "327.39": digits, an optional leading minus and a dot before the cents, without currency symbols, spaces or thousands separators`}
	}
	sign, integer, fraction := match[1], match[2], match[3]
	if len(fraction) > exponent {
		return Money{}, &ParseError{Amount: amount, Message: fmt.Sprintf("has more than %d decimal places for %s", exponent, currency)}
	}
	if len(integer) > maxIntegerDigits {
		return Money{}, &ParseError{Amount: amount, Message: "is too large"}
	}
	minor, err := strconv.ParseInt(integer+fraction+strings.Repeat("0", exponent-len(fraction)), 10, 64)
	if err != nil {
		return Money{}, &ParseError{Amount: amount, Message: "is too large"}
	}
	if sign == "-" {
		minor = -minor
	}
	return Money{Minor: minor, Currency: currency}, nil
}

func New(minor int64, currency string) (Money, error) {
	if _, err := exponentOf(currency); err != nil {
		return Money{}, err
	}
	return Money{Minor: minor, Currency: currency}, nil
}

func (m Money) Add(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, fmt.Errorf("%w: %s and %s", ErrCurrencyMismatch, m.Currency, other.Currency)
	}
	sum := m.Minor + other.Minor
	if (other.Minor > 0 && sum < m.Minor) || (other.Minor < 0 && sum > m.Minor) {
		return Money{}, ErrOverflow
	}
	return Money{Minor: sum, Currency: m.Currency}, nil
}

func (m Money) Sub(other Money) (Money, error) {
	if other.Minor == minInt64 {
		return Money{}, ErrOverflow
	}
	return m.Add(Money{Minor: -other.Minor, Currency: other.Currency})
}

func (m Money) IsZero() bool {
	return m.Minor == 0
}

func (m Money) IsNegative() bool {
	return m.Minor < 0
}

func (m Money) Decimal() string {
	sign, integer, fraction := m.parts()
	if fraction == "" {
		return sign + integer
	}
	return sign + integer + "." + fraction
}

func (m Money) Format() string {
	sign, integer, fraction := m.parts()
	number := groupThousands(integer)
	if fraction != "" {
		number += "," + fraction
	}
	return sign + symbolOf(m.Currency) + " " + number
}

func (m Money) String() string {
	return m.Decimal() + " " + m.Currency
}

func (m Money) parts() (sign, integer, fraction string) {
	exponent, _ := exponentOf(m.Currency)
	magnitude := strings.TrimPrefix(strconv.FormatInt(m.Minor, 10), "-")
	if len(magnitude) <= exponent {
		magnitude = strings.Repeat("0", exponent-len(magnitude)+1) + magnitude
	}
	cut := len(magnitude) - exponent
	if m.Minor < 0 {
		sign = "-"
	}
	return sign, magnitude[:cut], magnitude[cut:]
}

func groupThousands(digits string) string {
	var b strings.Builder
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(digit)
	}
	return b.String()
}
