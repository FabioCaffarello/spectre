// SPDX-License-Identifier: Apache-2.0

package tls

import (
	"strings"
	"testing"
)

func makeGetter(env map[string]string) func(string) string {
	return func(k string) string {
		return env[k]
	}
}

func TestDetectMode_AllUnset_IsPlaintext(t *testing.T) {
	cfg, err := detectModeFromGetter(makeGetter(map[string]string{}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.Mode != ModePlaintext {
		t.Errorf("expected ModePlaintext, got %v", cfg.Mode)
	}
}

func TestDetectMode_AllSet_IsMutual(t *testing.T) {
	cfg, err := detectModeFromGetter(makeGetter(map[string]string{
		CertPathEnv: "/etc/spectre/tls/tls.crt",
		KeyPathEnv:  "/etc/spectre/tls/tls.key",
		CAPathEnv:   "/etc/spectre/tls/ca.crt",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.Mode != ModeMutual {
		t.Errorf("expected ModeMutual, got %v", cfg.Mode)
	}
}

func TestDetectMode_Partial_IsError(t *testing.T) {
	_, err := detectModeFromGetter(makeGetter(map[string]string{
		CertPathEnv: "/etc/spectre/tls/tls.crt",
		KeyPathEnv:  "/etc/spectre/tls/tls.key",
	}))
	if err == nil {
		t.Fatal("expected error for partial config")
	}
	if !strings.Contains(err.Error(), CAPathEnv) {
		t.Errorf("error should mention unset var %s, got: %s", CAPathEnv, err.Error())
	}
}

func TestMode_String(t *testing.T) {
	if ModePlaintext.String() != "plaintext" {
		t.Errorf("plaintext: got %q", ModePlaintext.String())
	}
	if ModeMutual.String() != "mutual" {
		t.Errorf("mutual: got %q", ModeMutual.String())
	}
}
