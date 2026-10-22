package config

import (
	"errors"
	"fmt"
	"log/slog"
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
}

func Load(getenv func(string) string) (Config, error) {
	r := reader{getenv: getenv}
	cfg := Config{
		Environment:     r.environment("LASTRO_ENV", Development),
		Port:            r.port("PORT", 8080),
		DatabaseURL:     r.required("DATABASE_URL"),
		LogLevel:        r.logLevel("LOG_LEVEL", slog.LevelInfo),
		ShutdownTimeout: r.duration("SHUTDOWN_TIMEOUT", 15*time.Second),
	}
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
