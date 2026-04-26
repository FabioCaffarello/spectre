// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/curlx"
)

func TestResolveSocketPathFromFlag(t *testing.T) {
	t.Setenv("SPECTRE_DRIVER_SOCKET", "")
	got, err := resolveSocketPath([]string{"--socket=/tmp/spectre-curl.sock"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "/tmp/spectre-curl.sock" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveSocketPathFlagWinsOverEnv(t *testing.T) {
	t.Setenv("SPECTRE_DRIVER_SOCKET", "/tmp/from-env.sock")
	got, err := resolveSocketPath([]string{"--socket=/tmp/from-flag.sock"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "/tmp/from-flag.sock" {
		t.Fatalf("flag must win; got %q", got)
	}
}

func TestResolveSocketPathFromEnv(t *testing.T) {
	t.Setenv("SPECTRE_DRIVER_SOCKET", "/tmp/from-env.sock")
	got, err := resolveSocketPath(nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "/tmp/from-env.sock" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveSocketPathRequired(t *testing.T) {
	t.Setenv("SPECTRE_DRIVER_SOCKET", "")
	_, err := resolveSocketPath(nil)
	if err == nil {
		t.Fatal("expected error when no socket path is provided")
	}
	if !strings.Contains(err.Error(), "no socket path") {
		t.Fatalf("expected helpful message, got %v", err)
	}
}

func TestResolveSocketPathRejectsRelative(t *testing.T) {
	t.Setenv("SPECTRE_DRIVER_SOCKET", "")
	_, err := resolveSocketPath([]string{"--socket=relative.sock"})
	if err == nil {
		t.Fatal("expected error for relative path")
	}
	if !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("expected absolute-path error, got %v", err)
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

func TestEnsureParentCreatesDir(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nested", "child", "d.sock")
	if err := ensureParent(target); err != nil {
		t.Fatalf("ensureParent: %v", err)
	}
	// A second call must succeed.
	if err := ensureParent(target); err != nil {
		t.Fatalf("ensureParent (second call): %v", err)
	}
}

func TestEnsureParentTolerates(t *testing.T) {
	if err := ensureParent("name"); err != nil {
		t.Fatalf("ensureParent on bare filename: %v", err)
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
