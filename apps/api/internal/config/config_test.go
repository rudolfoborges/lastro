package config_test

import (
	"log/slog"
	"net/url"
	"reflect"
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
		RateLimits:      config.RateLimits{PerKeyPerMinute: 120, UnauthenticatedPerMinute: 10},
		PublicBaseURL:   &url.URL{Scheme: "http", Host: "localhost:8080"},
		OAuth:           config.OAuth{RedirectHosts: []string{"chatgpt.com"}, MetadataHosts: []string{"chatgpt.com"}},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestLoadUsesJSONLogsInProduction(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"DATABASE_URL":    "postgres://db/lastro",
		"LASTRO_ENV":      "production",
		"PUBLIC_BASE_URL": "https://lastro.code04.dev",
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

		"RATE_LIMIT_PER_MINUTE":                 "30",
		"UNAUTHENTICATED_RATE_LIMIT_PER_MINUTE": "5",
		"TRUST_PROXY_HEADERS":                   "true",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := config.Config{
		Environment:       config.Test,
		Port:              9090,
		DatabaseURL:       "postgres://db/lastro",
		LogLevel:          slog.LevelDebug,
		LogFormat:         config.LogFormatJSON,
		ShutdownTimeout:   3 * time.Second,
		RateLimits:        config.RateLimits{PerKeyPerMinute: 30, UnauthenticatedPerMinute: 5},
		TrustProxyHeaders: true,
		PublicBaseURL:     &url.URL{Scheme: "http", Host: "localhost:9090"},
		OAuth:             config.OAuth{RedirectHosts: []string{"chatgpt.com"}, MetadataHosts: []string{"chatgpt.com"}},
	}
	if !reflect.DeepEqual(cfg, want) {
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

		"RATE_LIMIT_PER_MINUTE": "0",
		"TRUST_PROXY_HEADERS":   "maybe",
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
		`RATE_LIMIT_PER_MINUTE must be a positive integer: got "0"`,
		`TRUST_PROXY_HEADERS must be true or false: got "maybe"`,
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("error %q does not mention %q", err, fragment)
		}
	}
}

func TestPublicBaseURLIsRequiredInProductionAndMustBeAnOrigin(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://localhost/lastro"}

	cfg, err := config.Load(func(key string) string { return base[key] })
	if err != nil || cfg.PublicBaseURL.String() != "http://localhost:8080" {
		t.Errorf("development default = %v, %v", cfg.PublicBaseURL, err)
	}

	for name, value := range map[string]string{
		"missing in production": "",
		"http in production":    "http://lastro.code04.dev",
		"with a path":           "https://lastro.code04.dev/api",
		"with a query":          "https://lastro.code04.dev?x=1",
	} {
		t.Run(name, func(t *testing.T) {
			vars := map[string]string{"DATABASE_URL": "postgres://localhost/lastro", "LASTRO_ENV": "production", "PUBLIC_BASE_URL": value}
			if _, err := config.Load(func(key string) string { return vars[key] }); err == nil || !strings.Contains(err.Error(), "PUBLIC_BASE_URL") {
				t.Errorf("Load() error = %v, want a PUBLIC_BASE_URL error", err)
			}
		})
	}

	vars := map[string]string{"DATABASE_URL": "postgres://localhost/lastro", "LASTRO_ENV": "production", "PUBLIC_BASE_URL": "https://lastro.code04.dev/", "OAUTH_REDIRECT_HOSTS": "chatgpt.com, Example.com"}
	cfg, err = config.Load(func(key string) string { return vars[key] })
	if err != nil || cfg.PublicBaseURL.String() != "https://lastro.code04.dev" || cfg.OAuth.RedirectHosts[1] != "example.com" {
		t.Errorf("production = %+v, %v", cfg, err)
	}
}
