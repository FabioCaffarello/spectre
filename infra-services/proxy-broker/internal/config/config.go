// SPDX-License-Identifier: Apache-2.0

// Package config loads the proxy-broker's runtime configuration
// from environment variables. Single source of truth for
// `cmd/proxy-broker/main.go` — keeps env-name + default-value
// + env-presence-validation in one place so future flag /
// file-based loaders (W5.x) layer over this without churn.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	EnvListenAddr   = "SPECTRE_PROXY_BROKER_LISTEN_ADDR"
	EnvRedisURL     = "SPECTRE_PROXY_BROKER_REDIS_URL"
	EnvMetricsPort  = "SPECTRE_METRICS_PORT"
	EnvOTLPEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"

	// TLS env contract mirrors W3.3 (engine + operator + 3
	// adapters): all three set → mutual TLS; all three unset
	// → plaintext; partial → fail-fast.
	EnvTLSCertPath = "SPECTRE_TLS_CERT_PATH"
	EnvTLSKeyPath  = "SPECTRE_TLS_KEY_PATH"
	EnvTLSCAPath   = "SPECTRE_TLS_CA_PATH"

	// Stub provider configuration.
	EnvStubEnabled = "SPECTRE_PROXY_BROKER_STUB_ENABLED"
	EnvStubURLs    = "SPECTRE_PROXY_BROKER_STUB_URLS"

	// BrightData provider configuration.
	EnvBDUsername = "BRIGHTDATA_USERNAME"
	EnvBDPassword = "BRIGHTDATA_PASSWORD"
	EnvBDZone     = "BRIGHTDATA_ZONE"

	defaultListenAddr  = ":8094"
	defaultRedisURL    = "redis://127.0.0.1:6379/1"
	defaultMetricsPort = 9090
)

// Config is the resolved runtime configuration.
type Config struct {
	ListenAddr   string
	RedisURL     string
	MetricsPort  int
	OTLPEndpoint string

	TLSMode     TLSMode
	TLSCertPath string
	TLSKeyPath  string
	TLSCAPath   string

	StubEnabled bool
	StubURLs    []string

	BrightDataEnabled  bool
	BrightDataUsername string
	BrightDataPassword string
	BrightDataZone     string
}

// TLSMode classifies the resolved TLS posture.
type TLSMode int

const (
	TLSModePlaintext TLSMode = iota
	TLSModeMutual
)

func (m TLSMode) String() string {
	switch m {
	case TLSModePlaintext:
		return "plaintext"
	case TLSModeMutual:
		return "mutual"
	default:
		return "unknown"
	}
}

// Load reads the process env and returns a resolved Config.
// Returns an error for invalid combinations (partial TLS,
// invalid metrics port, etc.); the broker's main fail-fasts
// rather than starting with a bad config.
func Load() (*Config, error) {
	cfg := &Config{
		ListenAddr:   envOrDefault(EnvListenAddr, defaultListenAddr),
		RedisURL:     envOrDefault(EnvRedisURL, defaultRedisURL),
		OTLPEndpoint: os.Getenv(EnvOTLPEndpoint),
	}

	port, err := envPort(EnvMetricsPort, defaultMetricsPort)
	if err != nil {
		return nil, err
	}
	cfg.MetricsPort = port

	if err := loadTLS(cfg); err != nil {
		return nil, err
	}
	if err := loadProviders(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func loadTLS(cfg *Config) error {
	cert := os.Getenv(EnvTLSCertPath)
	key := os.Getenv(EnvTLSKeyPath)
	ca := os.Getenv(EnvTLSCAPath)
	set := 0
	for _, v := range []string{cert, key, ca} {
		if v != "" {
			set++
		}
	}
	switch set {
	case 0:
		cfg.TLSMode = TLSModePlaintext
		return nil
	case 3:
		cfg.TLSMode = TLSModeMutual
		cfg.TLSCertPath = cert
		cfg.TLSKeyPath = key
		cfg.TLSCAPath = ca
		return nil
	default:
		return fmt.Errorf("config: TLS env partial — exactly all three of %s / %s / %s must be set together (mTLS) or all unset (plaintext)",
			EnvTLSCertPath, EnvTLSKeyPath, EnvTLSCAPath)
	}
}

func loadProviders(cfg *Config) error {
	cfg.StubEnabled = envBool(EnvStubEnabled)
	if cfg.StubEnabled {
		raw := os.Getenv(EnvStubURLs)
		if raw == "" {
			return errors.New("config: stub enabled but " + EnvStubURLs + " is empty")
		}
		urls := strings.Split(raw, ",")
		out := make([]string, 0, len(urls))
		for _, u := range urls {
			u = strings.TrimSpace(u)
			if u != "" {
				out = append(out, u)
			}
		}
		cfg.StubURLs = out
	}

	user := os.Getenv(EnvBDUsername)
	pass := os.Getenv(EnvBDPassword)
	if user != "" && pass != "" {
		cfg.BrightDataEnabled = true
		cfg.BrightDataUsername = user
		cfg.BrightDataPassword = pass
		cfg.BrightDataZone = os.Getenv(EnvBDZone)
	}

	if !cfg.StubEnabled && !cfg.BrightDataEnabled {
		return errors.New("config: no provider enabled — set " + EnvStubEnabled +
			"=true (with " + EnvStubURLs + ") OR provide " + EnvBDUsername +
			" + " + EnvBDPassword)
	}
	return nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envPort(key string, def int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer, got %q: %w", key, raw, err)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("config: %s must be in [1, 65535], got %d", key, n)
	}
	return n, nil
}

func envBool(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}
