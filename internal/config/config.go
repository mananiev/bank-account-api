// Package config loads service configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
)

// Config holds all runtime settings for the API.
type Config struct {
	DatabaseURL string
	HTTPAddr    string
	LogLevel    slog.Level
}

// Load reads configuration from the environment.
// DATABASE_URL is required; HTTP_ADDR defaults to ":8080" and LOG_LEVEL to "info".
func Load() (Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (Config, error) {
	cfg := Config{
		DatabaseURL: getenv("DATABASE_URL"),
		HTTPAddr:    getenv("HTTP_ADDR"),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":8080"
	}

	if lvl := getenv("LOG_LEVEL"); lvl != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(lvl)); err != nil {
			return Config{}, fmt.Errorf("invalid LOG_LEVEL %q: %w", lvl, err)
		}
	}

	return cfg, nil
}
