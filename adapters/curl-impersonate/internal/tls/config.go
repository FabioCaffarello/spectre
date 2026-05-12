// SPDX-License-Identifier: Apache-2.0

// Package tls implements the curl-impersonate adapter's
// server-side TLS detection + credential plumbing for ADR-0032
// service-to-service mTLS (W3.4 second auth PR).
//
// Symmetric to the operator's `internal/tls` package landed in
// W3.3 — same env-var contract (`SPECTRE_TLS_{CERT,KEY,CA}_PATH`),
// same Mode classification, same partial-state fail-fast. The
// server-side wiring differs: the adapter REQUIRES client
// certificates (the engine is the only authorised caller per
// ADR-0032 §4.2), and the dynamic reload hook is
// `tls.Config.GetCertificate` (server-side cert lookup) rather
// than `GetClientCertificate` (client-side).
package tls

import (
	"fmt"
	"os"
)

// Env var names — identical to the operator's W3.3 contract so
// every Spectre service reads the same three vars.
const (
	CertPathEnv = "SPECTRE_TLS_CERT_PATH"
	KeyPathEnv  = "SPECTRE_TLS_KEY_PATH"
	CAPathEnv   = "SPECTRE_TLS_CA_PATH"
)

// Mode classifies the resolved TLS posture.
type Mode int

const (
	// ModePlaintext — all three env vars unset. The adapter
	// binds plaintext gRPC (v1alpha1 posture).
	ModePlaintext Mode = iota
	// ModeMutual — all three env vars set. The adapter
	// requires client certificates and verifies them against
	// the trust bundle.
	ModeMutual
)

func (m Mode) String() string {
	switch m {
	case ModePlaintext:
		return "plaintext"
	case ModeMutual:
		return "mutual"
	default:
		return fmt.Sprintf("Mode(%d)", int(m))
	}
}

// Config captures the resolved mode + the paths to read PEM
// material from when Mode is Mutual.
type Config struct {
	Mode     Mode
	CertPath string
	KeyPath  string
	CAPath   string
}

// DetectMode resolves the TLS posture from process env.
// Symmetric to the operator's `tls.DetectMode`.
func DetectMode() (Config, error) {
	return detectModeFromGetter(os.Getenv)
}

func detectModeFromGetter(getenv func(string) string) (Config, error) {
	cert := getenv(CertPathEnv)
	key := getenv(KeyPathEnv)
	ca := getenv(CAPathEnv)

	setCount := 0
	for _, v := range []string{cert, key, ca} {
		if v != "" {
			setCount++
		}
	}

	switch setCount {
	case 0:
		return Config{Mode: ModePlaintext}, nil
	case 3:
		return Config{
			Mode:     ModeMutual,
			CertPath: cert,
			KeyPath:  key,
			CAPath:   ca,
		}, nil
	default:
		return Config{}, partialConfigError(cert, key, ca)
	}
}

func partialConfigError(cert, key, ca string) error {
	set := []string{}
	unset := []string{}
	for _, kv := range []struct {
		name, value string
	}{
		{CertPathEnv, cert},
		{KeyPathEnv, key},
		{CAPathEnv, ca},
	} {
		if kv.value != "" {
			set = append(set, kv.name)
		} else {
			unset = append(unset, kv.name)
		}
	}
	return fmt.Errorf(
		"tls: partial env config — %v set, %v unset; all three of %s, %s, %s must be set together (mTLS) or all unset (plaintext)",
		set, unset, CertPathEnv, KeyPathEnv, CAPathEnv,
	)
}
