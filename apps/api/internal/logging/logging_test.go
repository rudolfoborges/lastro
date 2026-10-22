package logging_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/rudolfoborges/lastro/apps/api/internal/config"
	"github.com/rudolfoborges/lastro/apps/api/internal/logging"
)

func TestLogsNeverCarryCardNumbersOrCodes(t *testing.T) {
	for _, format := range []config.LogFormat{config.LogFormatJSON, config.LogFormatText} {
		var buf bytes.Buffer
		logger := logging.New(&buf, format, slog.LevelInfo)

		logger.Info("notification received", slog.String("text", "Compra no cartão 4111111111111111, código 482913"))

		if strings.Contains(buf.String(), "4111111111111111") || strings.Contains(buf.String(), "482913") {
			t.Errorf("%s log leaks secrets: %s", format, buf.String())
		}
	}
}
