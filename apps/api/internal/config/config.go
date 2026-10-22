package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Environment string

const (
	Development Environment = "development"
	Test        Environment = "test"
	Production  Environment = "production"
)

type LogFormat string

const (
	LogFormatJSON LogFormat = "json"
	LogFormatText LogFormat = "text"
)

type Config struct {
	Environment     Environment
	Port            int
	DatabaseURL     string
	LogLevel        slog.Level
	LogFormat       LogFormat
	ShutdownTimeout time.Duration
	RateLimits      RateLimits
	// Only enable behind a proxy that overwrites X-Real-IP and X-Forwarded-For,
	// otherwise clients can spoof the address used for rate limiting.
	TrustProxyHeaders bool
	PublicBaseURL     *url.URL
	OAuth             OAuth
}

type OAuth struct {
	RedirectHosts []string
	MetadataHosts []string
}

type RateLimits struct {
	PerKeyPerMinute          int
	UnauthenticatedPerMinute int
}

func Load(getenv func(string) string) (Config, error) {
	r := reader{getenv: getenv}
	cfg := Config{
		Environment:     r.environment("LASTRO_ENV", Development),
		Port:            r.port("PORT", 8080),
		DatabaseURL:     r.required("DATABASE_URL"),
		LogLevel:        r.logLevel("LOG_LEVEL", slog.LevelInfo),
		ShutdownTimeout: r.duration("SHUTDOWN_TIMEOUT", 15*time.Second),
		RateLimits: RateLimits{
			PerKeyPerMinute:          r.positiveInt("RATE_LIMIT_PER_MINUTE", 120),
			UnauthenticatedPerMinute: r.positiveInt("UNAUTHENTICATED_RATE_LIMIT_PER_MINUTE", 10),
		},
		TrustProxyHeaders: r.boolean("TRUST_PROXY_HEADERS", false),
		OAuth: OAuth{
			RedirectHosts: r.hosts("OAUTH_REDIRECT_HOSTS", []string{"chatgpt.com"}),
			MetadataHosts: r.hosts("OAUTH_METADATA_HOSTS", []string{"chatgpt.com"}),
		},
	}
	cfg.PublicBaseURL = r.baseURL("PUBLIC_BASE_URL", cfg.Environment, cfg.Port)
	cfg.LogFormat = r.logFormat("LOG_FORMAT", defaultLogFormat(cfg.Environment))
	if err := errors.Join(r.errs...); err != nil {
		return Config{}, fmt.Errorf("invalid configuration:\n%w", err)
	}
	return cfg, nil
}

func defaultLogFormat(env Environment) LogFormat {
	if env == Production {
		return LogFormatJSON
	}
	return LogFormatText
}

type reader struct {
	getenv func(string) string
	errs   []error
}

func (r *reader) fail(format string, args ...any) {
	r.errs = append(r.errs, fmt.Errorf("  - "+format, args...))
}

func (r *reader) lookup(key string) (string, bool) {
	value := strings.TrimSpace(r.getenv(key))
	return value, value != ""
}

func (r *reader) required(key string) string {
	value, ok := r.lookup(key)
	if !ok {
		r.fail("%s is required", key)
	}
	return value
}

func (r *reader) environment(key string, fallback Environment) Environment {
	value, ok := r.lookup(key)
	if !ok {
		return fallback
	}
	switch env := Environment(value); env {
	case Development, Test, Production:
		return env
	default:
		r.fail("%s must be one of development, test or production: got %q", key, value)
		return fallback
	}
}

func (r *reader) port(key string, fallback int) int {
	value, ok := r.lookup(key)
	if !ok {
		return fallback
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		r.fail("%s must be an integer between 1 and 65535: got %q", key, value)
		return fallback
	}
	return port
}

func (r *reader) positiveInt(key string, fallback int) int {
	value, ok := r.lookup(key)
	if !ok {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		r.fail("%s must be a positive integer: got %q", key, value)
		return fallback
	}
	return n
}

func (r *reader) boolean(key string, fallback bool) bool {
	value, ok := r.lookup(key)
	if !ok {
		return fallback
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		r.fail("%s must be true or false: got %q", key, value)
		return fallback
	}
	return b
}

func (r *reader) duration(key string, fallback time.Duration) time.Duration {
	value, ok := r.lookup(key)
	if !ok {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		r.fail("%s must be a positive duration such as 15s: got %q", key, value)
		return fallback
	}
	return d
}

func (r *reader) logLevel(key string, fallback slog.Level) slog.Level {
	value, ok := r.lookup(key)
	if !ok {
		return fallback
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		r.fail("%s must be one of debug, info, warn or error: got %q", key, value)
		return fallback
	}
	return level
}

func (r *reader) baseURL(key string, env Environment, port int) *url.URL {
	value, ok := r.lookup(key)
	if !ok {
		if env == Production {
			r.fail("%s is required in production: the public origin Case uses, such as https://lastro.example.com", key)
			return nil
		}
		value = fmt.Sprintf("http://localhost:%d", port)
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		r.fail("%s must be an origin such as https://lastro.example.com, without path or query: got %q", key, value)
		return nil
	}
	if u.Scheme != "https" && (env == Production || u.Scheme != "http") {
		r.fail("%s must use https in production: got %q", key, value)
		return nil
	}
	u.Path = ""
	return u
}

func (r *reader) hosts(key string, fallback []string) []string {
	value, ok := r.lookup(key)
	if !ok {
		return fallback
	}
	var hosts []string
	for _, host := range strings.Split(value, ",") {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" || strings.ContainsAny(host, "/:@ ") {
			r.fail("%s must be a comma-separated list of host names: got %q", key, value)
			return fallback
		}
		hosts = append(hosts, host)
	}
	return hosts
}

func (r *reader) logFormat(key string, fallback LogFormat) LogFormat {
	value, ok := r.lookup(key)
	if !ok {
		return fallback
	}
	switch format := LogFormat(value); format {
	case LogFormatJSON, LogFormatText:
		return format
	default:
		r.fail("%s must be json or text: got %q", key, value)
		return fallback
	}
}
