package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Environment string
	HTTP        HTTPConfig
	Database    DatabaseConfig
	Auth        AuthBootstrapConfig
	Secrets     SecretConfig
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

type SecretConfig struct {
	CredentialKeys   map[int][]byte
	ActiveKeyVersion int
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
	credentialKeys, activeKeyVersion, err := loadCredentialKeys()
	if err != nil {
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
		Secrets: SecretConfig{
			CredentialKeys:   credentialKeys,
			ActiveKeyVersion: activeKeyVersion,
		},
	}, nil
}

func loadCredentialKeys() (map[int][]byte, int, error) {
	rawKeys := strings.TrimSpace(os.Getenv("CREDENTIAL_KEYS"))
	if rawKeys == "" {
		return nil, 0, errors.New("CREDENTIAL_KEYS is required")
	}
	rawActive := strings.TrimSpace(os.Getenv("CREDENTIAL_ACTIVE_KEY_VERSION"))
	activeVersion, err := strconv.Atoi(rawActive)
	if err != nil || activeVersion < 1 {
		return nil, 0, errors.New("CREDENTIAL_ACTIVE_KEY_VERSION must be a positive integer")
	}

	keys := make(map[int][]byte)
	for _, entry := range strings.Split(rawKeys, ",") {
		rawVersion, encoded, ok := strings.Cut(strings.TrimSpace(entry), ":")
		if !ok {
			return nil, 0, errors.New("CREDENTIAL_KEYS entries must use version:base64 format")
		}
		version, err := strconv.Atoi(strings.TrimSpace(rawVersion))
		if err != nil || version < 1 {
			return nil, 0, errors.New("credential key versions must be positive integers")
		}
		if _, duplicate := keys[version]; duplicate {
			return nil, 0, fmt.Errorf("credential key version %d is duplicated", version)
		}
		key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(encoded))
		if err != nil || len(key) != 32 {
			return nil, 0, fmt.Errorf("credential key version %d must be base64-encoded 32 bytes", version)
		}
		keys[version] = key
	}
	if _, ok := keys[activeVersion]; !ok {
		return nil, 0, errors.New("active credential key version is not configured")
	}
	return keys, activeVersion, nil
}

func validateDatabaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("DATABASE_URL is malformed")
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
