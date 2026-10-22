package config_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/config"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://localhost/lastro"}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := config.Config{
		Environment:     config.Development,
		Port:            8080,
		DatabaseURL:     "postgres://localhost/lastro",
		LogLevel:        slog.LevelInfo,
		LogFormat:       config.LogFormatText,
		ShutdownTimeout: 15 * time.Second,
	}
	if cfg != want {
		t.Errorf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestLoadUsesJSONLogsInProduction(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"DATABASE_URL": "postgres://db/lastro",
		"LASTRO_ENV":   "production",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.LogFormat != config.LogFormatJSON {
		t.Errorf("LogFormat = %q, want json", cfg.LogFormat)
	}
}

func TestLoadReadsExplicitValues(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"DATABASE_URL":     "postgres://db/lastro",
		"LASTRO_ENV":       "test",
		"PORT":             "9090",
		"LOG_LEVEL":        "debug",
		"LOG_FORMAT":       "json",
		"SHUTDOWN_TIMEOUT": "3s",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := config.Config{
		Environment:     config.Test,
		Port:            9090,
		DatabaseURL:     "postgres://db/lastro",
		LogLevel:        slog.LevelDebug,
		LogFormat:       config.LogFormatJSON,
		ShutdownTimeout: 3 * time.Second,
	}
	if cfg != want {
		t.Errorf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestLoadReportsEveryInvalidValue(t *testing.T) {
	_, err := config.Load(env(map[string]string{
		"LASTRO_ENV":       "staging",
		"PORT":             "70000",
		"LOG_LEVEL":        "loud",
		"LOG_FORMAT":       "xml",
		"SHUTDOWN_TIMEOUT": "soon",
	}))
	if err == nil {
		t.Fatal("Load() error = nil, want configuration error")
	}

	for _, fragment := range []string{
		"DATABASE_URL is required",
		`LASTRO_ENV must be one of development, test or production: got "staging"`,
		`PORT must be an integer between 1 and 65535: got "70000"`,
		`LOG_LEVEL must be one of debug, info, warn or error: got "loud"`,
		`LOG_FORMAT must be json or text: got "xml"`,
		`SHUTDOWN_TIMEOUT must be a positive duration such as 15s: got "soon"`,
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("error %q does not mention %q", err, fragment)
		}
	}
}
