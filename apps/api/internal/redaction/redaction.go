package redaction

import (
	"regexp"
	"slices"
	"strings"
)

type Kind string

const (
	KindCardNumber Kind = "card_number"
	KindCPF        Kind = "cpf"
	KindSecret     Kind = "secret"
)

const Placeholder = "[redacted]"

type Finding struct {
	Kind  Kind `json:"kind"`
	Count int  `json:"count"`
}

type Result struct {
	Text     string
	Findings []Finding
}

var (
	cpfCandidate = regexp.MustCompile(`\d{3}\.?\d{3}\.?\d{3}-?\d{2}`)

	labeledSecret = regexp.MustCompile(`(?i)\b(` +
		`c[oó]digo(?:\s+de\s+(?:verifica[cç][aã]o|seguran[cç]a|acesso|autentica[cç][aã]o|confirma[cç][aã]o))?` +
		`|verification\s+code|security\s+code|one[- ]time\s+(?:code|password)` +
		`|otp|token|senha|password|passcode|pin|cvv|cvc` +
		`)\b(\s*(?:[:=]|é|eh|is)?\s*)([A-Za-z0-9][A-Za-z0-9-]{2,})`)
	numericCode = regexp.MustCompile(`(?i)\b(` +
		`c[oó]digo|code|otp|token|senha|password|passcode|pin|cvv|cvc` +
		`)\b((?:\s+[^\s\d]{1,12}){0,3}\s*[:=]?\s*)(\d[\d-]{2,11})\b`)
	bearerSecret  = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{10,}`)
	issuedSecret  = regexp.MustCompile(`\b(?:lst|lat|lrt|inv)_[A-Za-z0-9_-]{10,}`)
	jwtLikeSecret = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)
)

func Text(text string) Result {
	r := &redactor{counts: map[Kind]int{}}
	text = r.secrets(text)
	text = r.cards(text)
	text = r.cpfs(text)
	return Result{Text: text, Findings: r.findings()}
}

func Merge(results ...[]Finding) []Finding {
	counts := map[Kind]int{}
	for _, findings := range results {
		for _, f := range findings {
			counts[f.Kind] += f.Count
		}
	}
	return toFindings(counts)
}

type redactor struct {
	counts map[Kind]int
}

func (r *redactor) secrets(text string) string {
	text = numericCode.ReplaceAllStringFunc(text, func(match string) string {
		parts := numericCode.FindStringSubmatch(match)
		r.counts[KindSecret]++
		return parts[1] + parts[2] + Placeholder
	})
	text = labeledSecret.ReplaceAllStringFunc(text, func(match string) string {
		parts := labeledSecret.FindStringSubmatch(match)
		label, separator, value := parts[1], parts[2], parts[3]
		if strings.TrimSpace(separator) == "" && !strings.ContainsAny(value, "0123456789") {
			return match
		}
		r.counts[KindSecret]++
		return label + separator + Placeholder
	})
	for _, pattern := range []*regexp.Regexp{bearerSecret, issuedSecret, jwtLikeSecret} {
		text = pattern.ReplaceAllStringFunc(text, func(string) string {
			r.counts[KindSecret]++
			return Placeholder
		})
	}
	return text
}

const (
	minCardDigits = 13
	maxCardDigits = 19
)

func (r *redactor) cards(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		if !startsNumber(text, i) {
			b.WriteByte(text[i])
			i++
			continue
		}
		end, ok := cardEndingAt(text, i)
		if !ok {
			b.WriteByte(text[i])
			i++
			continue
		}
		r.counts[KindCardNumber]++
		b.WriteString(maskAllButLast(text[i:end], 4))
		i = end
	}
	return b.String()
}

func startsNumber(text string, i int) bool {
	return isDigit(text[i]) && (i == 0 || !isDigit(text[i-1]))
}

func cardEndingAt(text string, i int) (int, bool) {
	var digits []byte
	best := -1
	for j := i; j < len(text) && len(digits) < maxCardDigits; j++ {
		c := text[j]
		if !isDigit(c) {
			if (c == ' ' || c == '-') && j+1 < len(text) && isDigit(text[j+1]) && isDigit(text[j-1]) {
				continue
			}
			break
		}
		digits = append(digits, c)
		atBoundary := j+1 == len(text) || !isDigit(text[j+1])
		if atBoundary && len(digits) >= minCardDigits && luhnValid(string(digits)) {
			best = j + 1
		}
	}
	return best, best >= 0
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func (r *redactor) cpfs(text string) string {
	var b strings.Builder
	last := 0
	for _, loc := range cpfCandidate.FindAllStringIndex(text, -1) {
		start, end := loc[0], loc[1]
		standalone := (start == 0 || !isDigit(text[start-1])) && (end == len(text) || !isDigit(text[end]))
		if !standalone || !cpfValid(onlyDigits(text[start:end])) {
			continue
		}
		r.counts[KindCPF]++
		b.WriteString(text[last:start])
		b.WriteString(maskAllButLast(text[start:end], 0))
		last = end
	}
	b.WriteString(text[last:])
	return b.String()
}

func (r *redactor) findings() []Finding {
	return toFindings(r.counts)
}

func toFindings(counts map[Kind]int) []Finding {
	findings := make([]Finding, 0, len(counts))
	for kind, count := range counts {
		if count > 0 {
			findings = append(findings, Finding{Kind: kind, Count: count})
		}
	}
	slices.SortFunc(findings, func(a, b Finding) int { return strings.Compare(string(a.Kind), string(b.Kind)) })
	return findings
}

func maskAllButLast(value string, keep int) string {
	total := len(onlyDigits(value))
	seen := 0
	masked := []byte(value)
	for i, c := range masked {
		if c < '0' || c > '9' {
			continue
		}
		seen++
		if seen <= total-keep {
			masked[i] = '*'
		}
	}
	return string(masked)
}

func onlyDigits(value string) string {
	var b strings.Builder
	for _, c := range value {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func luhnValid(digits string) bool {
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

func cpfValid(digits string) bool {
	if len(digits) != 11 || strings.Count(digits, digits[:1]) == 11 {
		return false
	}
	return cpfCheckDigit(digits[:9], 10) == digits[9] && cpfCheckDigit(digits[:10], 11) == digits[10]
}

func cpfCheckDigit(digits string, weight int) byte {
	sum := 0
	for _, c := range digits {
		sum += int(c-'0') * weight
		weight--
	}
	rest := sum * 10 % 11
	if rest == 10 {
		rest = 0
	}
	return byte('0' + rest)
}
