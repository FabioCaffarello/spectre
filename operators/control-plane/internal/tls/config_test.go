/*
Copyright 2026 Fabio Caffarello.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

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
	if cfg.CertPath != "/etc/spectre/tls/tls.crt" {
		t.Errorf("CertPath: %q", cfg.CertPath)
	}
	if cfg.KeyPath != "/etc/spectre/tls/tls.key" {
		t.Errorf("KeyPath: %q", cfg.KeyPath)
	}
	if cfg.CAPath != "/etc/spectre/tls/ca.crt" {
		t.Errorf("CAPath: %q", cfg.CAPath)
	}
}

func TestDetectMode_Partial_IsError(t *testing.T) {
	_, err := detectModeFromGetter(makeGetter(map[string]string{
		CertPathEnv: "/etc/spectre/tls/tls.crt",
		KeyPathEnv:  "/etc/spectre/tls/tls.key",
		// CA deliberately unset
	}))
	if err == nil {
		t.Fatal("expected error for partial config")
	}
	if !strings.Contains(err.Error(), CAPathEnv) {
		t.Errorf("error should mention unset var %s, got: %s", CAPathEnv, err.Error())
	}
}

func TestDetectMode_EmptyString_IsUnset(t *testing.T) {
	// Empty string from os.Getenv is equivalent to unset; verify
	// it doesn't trigger the partial-config path on its own.
	cfg, err := detectModeFromGetter(makeGetter(map[string]string{
		CertPathEnv: "",
		KeyPathEnv:  "",
		CAPathEnv:   "",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.Mode != ModePlaintext {
		t.Errorf("expected ModePlaintext, got %v", cfg.Mode)
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
