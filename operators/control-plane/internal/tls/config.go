/*
Copyright 2026 Fabio Caffarello.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package tls implements the operator-side TLS detection +
// credential plumbing for ADR-0032 service-to-service mTLS.
//
// The package is symmetric to the engine's `spectre_engine::tls`
// module (Rust): both read the same three env vars
// (`SPECTRE_TLS_{CERT,KEY,CA}_PATH`), classify the result into a
// `Mode` (Plaintext / Mutual), and reject partial states.
//
// The operator is a CLIENT in operator → engine, so this package
// builds gRPC ClientCredentials (`credentials.NewTLS` of a
// dynamically-reloading `*tls.Config`). The reloader caches the
// most recent file read for 30 seconds (ADR-0032 §5.1) so
// cert-manager rotations propagate without service restart.
package tls

import (
	"fmt"
	"os"
)

// Env var names — kept in one place so the engine + operator
// share the same contract.
const (
	CertPathEnv = "SPECTRE_TLS_CERT_PATH"
	KeyPathEnv  = "SPECTRE_TLS_KEY_PATH"
	CAPathEnv   = "SPECTRE_TLS_CA_PATH"
)

// Mode classifies the resolved TLS posture.
type Mode int

const (
	// ModePlaintext — all three env vars unset. The operator
	// dials engine over plaintext gRPC (v1alpha1 posture).
	ModePlaintext Mode = iota
	// ModeMutual — all three env vars set. The operator
	// presents its client certificate and verifies the engine's
	// server certificate against the trust bundle.
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
	Mode Mode
	// CertPath is the path to the client certificate (PEM).
	// Populated only when Mode == ModeMutual.
	CertPath string
	// KeyPath is the path to the client private key (PEM).
	KeyPath string
	// CAPath is the path to the peer trust bundle (PEM).
	CAPath string
}

// DetectMode resolves the TLS posture from process env. Symmetric
// to the engine's `TlsConfig::from_env`: all three vars set →
// Mutual, all three unset → Plaintext, partial → error.
//
// The chart wires all three together via
// `_helpers.tpl::spectre.tlsEnv`, so the partial-state error path
// surfaces only hand-rolled misconfigurations. The operator
// binary treats the error as fatal (exit 1) so the Pod's Events
// stream carries the misconfig back to `kubectl get events`.
func DetectMode() (Config, error) {
	return detectModeFromGetter(os.Getenv)
}

// detectModeFromGetter is the testable seam DetectMode wraps. It
// reads the three env vars via the supplied getter so unit tests
// can drive the parsing without mutating process env.
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
