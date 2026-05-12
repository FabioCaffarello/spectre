/*
Copyright 2026 Fabio Caffarello.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package tls

import (
	"crypto/tls"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// makeReloader builds a ReloadingCredentials with a fake clock
// and a fake file reader so reload behavior can be exercised
// without touching disk.
func makeReloader(readCalls *atomic.Int32, now *time.Time) *ReloadingCredentials {
	return &ReloadingCredentials{
		certPath: "/fake/cert.crt",
		keyPath:  "/fake/key.key",
		interval: 30 * time.Second,
		timeNow: func() time.Time {
			return *now
		},
		readFiles: func(_, _ string) (*tls.Certificate, error) {
			readCalls.Add(1)
			return &tls.Certificate{Certificate: [][]byte{{0x42}}}, nil
		},
	}
}

func TestReloadingCredentials_FreshReadAfterInit(t *testing.T) {
	var reads atomic.Int32
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	r := makeReloader(&reads, &now)

	cert, err := r.currentCert()
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if cert == nil {
		t.Fatal("expected cert, got nil")
	}
	if got := reads.Load(); got != 1 {
		t.Errorf("expected 1 read, got %d", got)
	}
}

func TestReloadingCredentials_CacheHitWithinInterval(t *testing.T) {
	var reads atomic.Int32
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	r := makeReloader(&reads, &now)

	if _, err := r.currentCert(); err != nil {
		t.Fatalf("first read: %v", err)
	}
	// Advance clock inside the 30s window — should NOT reload.
	now = now.Add(15 * time.Second)
	if _, err := r.currentCert(); err != nil {
		t.Fatalf("cached read: %v", err)
	}
	if got := reads.Load(); got != 1 {
		t.Errorf("expected cache hit (1 read), got %d", got)
	}
}

func TestReloadingCredentials_ReloadAfterInterval(t *testing.T) {
	var reads atomic.Int32
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	r := makeReloader(&reads, &now)

	if _, err := r.currentCert(); err != nil {
		t.Fatalf("first read: %v", err)
	}
	// Advance past the 30s window — must reload.
	now = now.Add(31 * time.Second)
	if _, err := r.currentCert(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reads.Load(); got != 2 {
		t.Errorf("expected 2 reads after interval, got %d", got)
	}
}

func TestReloadingCredentials_ReadFailurePropagates(t *testing.T) {
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	r := &ReloadingCredentials{
		certPath: "/missing/cert",
		keyPath:  "/missing/key",
		interval: 30 * time.Second,
		timeNow:  func() time.Time { return now },
		readFiles: func(_, _ string) (*tls.Certificate, error) {
			return nil, errors.New("read failed")
		},
	}
	_, err := r.currentCert()
	if err == nil {
		t.Fatal("expected error to propagate")
	}
	if err.Error() != "read failed" {
		t.Errorf("expected propagated error, got %q", err.Error())
	}
}

func TestNewClientCredentials_Plaintext(t *testing.T) {
	creds, err := NewClientCredentials(Config{Mode: ModePlaintext}, "spectre-engine.spectre-system.svc")
	if err != nil {
		t.Fatalf("plaintext path returned error: %v", err)
	}
	// Insecure credentials report security protocol "insecure".
	if creds.Info().SecurityProtocol != "insecure" {
		t.Errorf("expected insecure credentials, got %q", creds.Info().SecurityProtocol)
	}
}
