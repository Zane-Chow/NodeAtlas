package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
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
	Backup      BackupConfig
	Console     ConsoleConfig
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

type BackupConfig struct {
	Directory string
}

type ConsoleConfig struct {
	AllowedPrivateCIDRs []string
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
	allowedPrivateCIDRs, err := loadConsoleAllowedPrivateCIDRs()
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
		Backup:  BackupConfig{Directory: envOrDefault("BACKUP_DIRECTORY", "data/backups")},
		Console: ConsoleConfig{AllowedPrivateCIDRs: allowedPrivateCIDRs},
	}, nil
}

func loadConsoleAllowedPrivateCIDRs() ([]string, error) {
	raw := strings.TrimSpace(os.Getenv("CONSOLE_ALLOWED_PRIVATE_CIDRS"))
	if raw == "" {
		return nil, nil
	}

	privateRoots := mustPrivateCIDRs()
	entries := strings.Split(raw, ",")
	result := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		value := strings.TrimSpace(entry)
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("CONSOLE_ALLOWED_PRIVATE_CIDRS contains invalid CIDR %q", value)
		}
		if !cidrWithinPrivateRoot(network, privateRoots) {
			return nil, fmt.Errorf("CONSOLE_ALLOWED_PRIVATE_CIDRS entry %q must be a private network", value)
		}
		canonical := network.String()
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}
	return result, nil
}

func mustPrivateCIDRs() []*net.IPNet {
	values := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"}
	result := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, _ := net.ParseCIDR(value)
		result = append(result, network)
	}
	return result
}

func cidrWithinPrivateRoot(candidate *net.IPNet, roots []*net.IPNet) bool {
	candidatePrefix, candidateBits := candidate.Mask.Size()
	for _, root := range roots {
		rootPrefix, rootBits := root.Mask.Size()
		if candidateBits == rootBits && candidatePrefix >= rootPrefix && root.Contains(candidate.IP) {
			return true
		}
	}
	return false
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
