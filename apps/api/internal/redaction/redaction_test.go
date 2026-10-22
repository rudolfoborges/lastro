package redaction_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/rudolfoborges/lastro/apps/api/internal/redaction"
)

func assertFinding(t *testing.T, findings []redaction.Finding, kind redaction.Kind, count int) {
	t.Helper()
	for _, f := range findings {
		if f.Kind == kind {
			if f.Count != count {
				t.Errorf("%s count = %d, want %d", kind, f.Count, count)
			}
			return
		}
	}
	if count > 0 {
		t.Errorf("findings = %+v, want %d %s", findings, count, kind)
	}
}

func TestCardNumbersKeepOnlyTheLastFourDigits(t *testing.T) {
	tests := map[string]string{
		"Compra no cartão 4111111111111111 aprovada":    "Compra no cartão ************1111 aprovada",
		"cartão 5555 5555 5555 4444 final 4444":         "cartão **** **** **** 4444 final 4444",
		"amex 3782-822463-10005":                        "amex ****-******-*0005",
		"6011111111111117 e 4111 1111 1111 1111 também": "************1117 e **** **** **** 1111 também",
	}
	for input, want := range tests {
		got := redaction.Text(input)
		if got.Text != want {
			t.Errorf("Text(%q) = %q, want %q", input, got.Text, want)
		}
	}
	assertFinding(t, redaction.Text("6011111111111117 e 4111 1111 1111 1111").Findings, redaction.KindCardNumber, 2)
}

func TestNumbersThatAreNotCardsStayIntact(t *testing.T) {
	for _, input := range []string{
		"Gastei R$ 327,39 no mercado",
		"pedido 4111111111111112",
		"boleto 23793381286000782713695000063305975520000370000",
		"telefone 11987654321",
		"último 4 dígitos 1234",
	} {
		got := redaction.Text(input)
		if got.Text != input || len(got.Findings) != 0 {
			t.Errorf("Text(%q) = %q, %+v, want unchanged", input, got.Text, got.Findings)
		}
	}
}

func TestValidCPFsAreMaskedCompletely(t *testing.T) {
	got := redaction.Text("Pix para CPF 529.982.247-25 e 52998224725")

	if got.Text != "Pix para CPF ***.***.***-** e ***********" {
		t.Errorf("Text() = %q", got.Text)
	}
	assertFinding(t, got.Findings, redaction.KindCPF, 2)

	glued := redaction.Text("CPF52998224725X")
	if glued.Text != "CPF***********X" {
		t.Errorf("Text(glued CPF) = %q", glued.Text)
	}

	invalid := redaction.Text("CPF 529.982.247-26 e 111.111.111-11 e 1529982247250")
	if len(invalid.Findings) != 0 {
		t.Errorf("invalid CPFs were redacted: %q", invalid.Text)
	}
}

func TestAuthenticationCodesPasswordsAndTokensAreDiscarded(t *testing.T) {
	tests := map[string]string{
		"Seu código de verificação é 482913":                     "Seu código de verificação é [redacted]",
		"Código: 4829-13 não compartilhe":                        "Código: [redacted] não compartilhe",
		"OTP 773311":                                             "OTP [redacted]",
		"o código era 482913":                                    "o código era [redacted]",
		"use o código de acesso enviado 1234":                    "use o código de acesso enviado [redacted]",
		"senha: abacaxi123":                                      "senha: [redacted]",
		"password is hunter2":                                    "password is [redacted]",
		"CVV 123":                                                "CVV [redacted]",
		"Authorization: Bearer abc.def.ghijklmnopqrstuv":         "Authorization: [redacted]",
		"chave lst_0123456789abcdef_secretsecret":                "chave [redacted]",
		"jwt eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0.c2lnbmF0dXJlMTIz": "jwt [redacted]",
	}
	for input, want := range tests {
		got := redaction.Text(input)
		if got.Text != want {
			t.Errorf("Text(%q) = %q, want %q", input, got.Text, want)
		}
		assertFinding(t, got.Findings, redaction.KindSecret, 1)
	}
}

func TestWordsAfterSecretLabelsStayWhenNothingLooksLikeACode(t *testing.T) {
	for _, input := range []string{"esqueci a senha do banco", "paguei o pin pad", "código pix copia e cola"} {
		if got := redaction.Text(input); got.Text != input {
			t.Errorf("Text(%q) = %q, want unchanged", input, got.Text)
		}
	}
}

func TestValuesRedactsNestedStringsAndCountsFindings(t *testing.T) {
	payload := map[string]any{
		"raw_text": "cartão 4111111111111111 código 123456",
		"amount":   "327.39",
		"items":    []any{"CPF 529.982.247-25", 42},
		"nested":   map[string]any{"note": "token: abcdef123456"},
	}

	redacted, findings := redaction.Values(payload)

	if redacted["raw_text"] != "cartão ************1111 código [redacted]" || redacted["amount"] != "327.39" {
		t.Errorf("redacted = %+v", redacted)
	}
	if items := redacted["items"].([]any); items[0] != "CPF ***.***.***-**" || items[1] != 42 {
		t.Errorf("items = %+v", items)
	}
	assertFinding(t, findings, redaction.KindCardNumber, 1)
	assertFinding(t, findings, redaction.KindCPF, 1)
	assertFinding(t, findings, redaction.KindSecret, 2)
	if payload["raw_text"] != "cartão 4111111111111111 código 123456" {
		t.Error("Values() changed its input")
	}
}

func TestLogHandlerRedactsMessagesAttributesAndErrors(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(redaction.NewHandler(slog.NewJSONHandler(&buf, nil))).With(slog.String("card", "4111111111111111"))

	logger.Info("charged 5555555555554444",
		slog.Group("request", slog.String("body", "senha: segredo123")),
		slog.Any("error", errors.New("token lst_0123456789abcdef_secretsecret rejected")),
		slog.Int("status", 400),
	)

	logs := buf.String()
	for _, secret := range []string{"4111111111111111", "5555555555554444", "segredo123", "lst_0123456789abcdef"} {
		if strings.Contains(logs, secret) {
			t.Errorf("log contains %q: %s", secret, logs)
		}
	}
	if !strings.Contains(logs, `"status":400`) || !strings.Contains(logs, "************4444") {
		t.Errorf("log lost non-secret data: %s", logs)
	}
}

func TestOnlyGoogleWalletNotificationsAreAllowed(t *testing.T) {
	if !redaction.AllowedNotificationPackage("com.google.android.apps.walletnfcrel") {
		t.Error("Google Wallet package rejected")
	}
	for _, pkg := range []string{"com.whatsapp", "com.nu.production", ""} {
		if redaction.AllowedNotificationPackage(pkg) {
			t.Errorf("package %q allowed", pkg)
		}
	}
}

func FuzzTextNeverLeaksValidCardNumbers(f *testing.F) {
	f.Add("411111111111111", "Compra aprovada no cartão ", " em MERCADO")
	f.Add("555555555555444", "", "")
	f.Add("601111111111111", "código ", " ok")
	f.Fuzz(func(t *testing.T, seed, prefix, suffix string) {
		digits := onlyDigits(seed)
		if len(digits) < 12 || len(digits) > 18 {
			t.Skip()
		}
		pan := digits + luhnCheckDigit(digits)
		if strings.ContainsAny(prefix+suffix, "0123456789") {
			t.Skip()
		}

		got := redaction.Text(prefix + pan + suffix)

		if strings.Contains(got.Text, pan) {
			t.Errorf("Text() leaked %s: %q", pan, got.Text)
		}
	})
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

func luhnCheckDigit(digits string) string {
	sum := 0
	double := true
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
	return string(rune('0' + (10-sum%10)%10))
}
