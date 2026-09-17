package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	Environment string
	HTTP        HTTPConfig
	Database    DatabaseConfig
	Auth        AuthBootstrapConfig
}

type DatabaseConfig struct {
	URL string
}

type HTTPConfig struct {
	Address      string
	PublicOrigin *url.URL
}

type AuthBootstrapConfig struct {
	Username string
	Password string
}

func Load() (Config, error) {
	environment := envOrDefault("APP_ENV", "development")
	databaseURL := envOrDefault("DATABASE_URL", "sqlite://data/controlpanel.db")
	address := envOrDefault("HTTP_ADDRESS", "127.0.0.1:8080")
	username := strings.TrimSpace(os.Getenv("ADMIN_USERNAME"))
	password := os.Getenv("ADMIN_PASSWORD")

	if (username == "") != (password == "") {
		return Config{}, errors.New("ADMIN_USERNAME and ADMIN_PASSWORD must be provided together")
	}
	if err := validateDatabaseURL(databaseURL); err != nil {
		return Config{}, err
	}

	var publicOrigin *url.URL
	if rawOrigin := strings.TrimSpace(os.Getenv("PUBLIC_ORIGIN")); rawOrigin != "" {
		parsed, err := url.Parse(rawOrigin)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return Config{}, errors.New("PUBLIC_ORIGIN must be an absolute http or https URL")
		}
		if parsed.Path != "" && parsed.Path != "/" {
			return Config{}, errors.New("PUBLIC_ORIGIN must not contain a path")
		}
		publicOrigin = parsed
	}
	if environment == "production" {
		if publicOrigin == nil || publicOrigin.Scheme != "https" {
			return Config{}, errors.New("production PUBLIC_ORIGIN must use https")
		}
	}

	return Config{
		Environment: environment,
		HTTP: HTTPConfig{
			Address:      address,
			PublicOrigin: publicOrigin,
		},
		Database: DatabaseConfig{URL: databaseURL},
		Auth: AuthBootstrapConfig{
			Username: username,
			Password: password,
		},
	}, nil
}

func validateDatabaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid DATABASE_URL: %w", err)
	}
	switch parsed.Scheme {
	case "sqlite":
		if parsed.Host == "" && parsed.Path == "" {
			return errors.New("sqlite DATABASE_URL requires a path")
		}
	case "mysql":
		if parsed.Host == "" || strings.TrimPrefix(parsed.Path, "/") == "" {
			return errors.New("mysql DATABASE_URL requires a host and database")
		}
	default:
		return errors.New("DATABASE_URL scheme must be sqlite or mysql")
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
