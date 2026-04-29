// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/curlx"
)

func TestResolvePortFromEnv(t *testing.T) {
	t.Setenv(portEnvVar, "8093")
	got, err := resolvePort()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != 8093 {
		t.Fatalf("got %d, want 8093", got)
	}
}

func TestResolvePortAcceptsZero(t *testing.T) {
	// Port 0 lets the kernel assign a free ephemeral port. The
	// conformance harness pre-allocates a port instead of using
	// this path, but the parser must not reject it.
	t.Setenv(portEnvVar, "0")
	got, err := resolvePort()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}

func TestResolvePortRequired(t *testing.T) {
	t.Setenv(portEnvVar, "")
	_, err := resolvePort()
	if err == nil {
		t.Fatal("expected error when env var is unset")
	}
	if !strings.Contains(err.Error(), portEnvVar) {
		t.Fatalf("expected error to name the env var, got %v", err)
	}
}

func TestResolvePortRejectsNonInteger(t *testing.T) {
	t.Setenv(portEnvVar, "abc")
	_, err := resolvePort()
	if err == nil {
		t.Fatal("expected error for non-integer value")
	}
	if !strings.Contains(err.Error(), "port number") {
		t.Fatalf("expected port-number error, got %v", err)
	}
}

func TestResolvePortRejectsOutOfRange(t *testing.T) {
	t.Setenv(portEnvVar, "70000")
	_, err := resolvePort()
	if err == nil {
		t.Fatal("expected error for out-of-range port")
	}
	if !strings.Contains(err.Error(), "between 0 and 65535") {
		t.Fatalf("expected range error, got %v", err)
	}
}

func TestResolveVariantDefault(t *testing.T) {
	t.Setenv("SPECTRE_CURL_VARIANT", "")
	got := resolveVariant()
	if got != curlx.DefaultVariant {
		t.Fatalf("got %q want %q", got, curlx.DefaultVariant)
	}
}

func TestResolveVariantOverride(t *testing.T) {
	t.Setenv("SPECTRE_CURL_VARIANT", "curl_firefox117")
	got := resolveVariant()
	if got != "curl_firefox117" {
		t.Fatalf("env var override must apply; got %q", got)
	}
}

// Sanity check that the package's identity constants are
// populated — keeps the embedded build identity discoverable.
func TestIdentityConstants(t *testing.T) {
	if binaryName == "" || version == "" || protocolVersion == "" {
		t.Fatal("identity constants must all be non-empty")
	}
}

// Package-level smoke that curlx.Fetch is the symbol main wires
// into the gRPC server. A future refactor that renames it fails
// here.
func TestFetcherSymbolStable(_ *testing.T) {
	_ = curlx.Fetch
}
