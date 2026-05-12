// SPDX-License-Identifier: Apache-2.0

package tls

import (
	"crypto/tls"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func makeReloader(reads *atomic.Int32, now *time.Time) *ReloadingCredentials {
	return &ReloadingCredentials{
		certPath: "/fake/cert.crt",
		keyPath:  "/fake/key.key",
		interval: 30 * time.Second,
		timeNow: func() time.Time {
			return *now
		},
		readFiles: func(_, _ string) (*tls.Certificate, error) {
			reads.Add(1)
			return &tls.Certificate{Certificate: [][]byte{{0x42}}}, nil
		},
	}
}

func TestReloadingCredentials_FreshReadAfterInit(t *testing.T) {
	var reads atomic.Int32
	now := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
	r := makeReloader(&reads, &now)

	cert, err := r.currentCert()
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if cert == nil {
		t.Fatal("expected cert")
	}
	if got := reads.Load(); got != 1 {
		t.Errorf("expected 1 read, got %d", got)
	}
}

func TestReloadingCredentials_CacheHitWithinInterval(t *testing.T) {
	var reads atomic.Int32
	now := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
	r := makeReloader(&reads, &now)

	if _, err := r.currentCert(); err != nil {
		t.Fatalf("first: %v", err)
	}
	now = now.Add(15 * time.Second)
	if _, err := r.currentCert(); err != nil {
		t.Fatalf("cached: %v", err)
	}
	if got := reads.Load(); got != 1 {
		t.Errorf("expected cache hit (1 read), got %d", got)
	}
}

func TestReloadingCredentials_ReloadAfterInterval(t *testing.T) {
	var reads atomic.Int32
	now := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
	r := makeReloader(&reads, &now)

	if _, err := r.currentCert(); err != nil {
		t.Fatalf("first: %v", err)
	}
	now = now.Add(31 * time.Second)
	if _, err := r.currentCert(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reads.Load(); got != 2 {
		t.Errorf("expected 2 reads after interval, got %d", got)
	}
}

func TestReloadingCredentials_ReadFailurePropagates(t *testing.T) {
	now := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
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
}

func TestNewServerCredentials_Plaintext_ReturnsNil(t *testing.T) {
	creds, err := NewServerCredentials(Config{Mode: ModePlaintext})
	if err != nil {
		t.Fatalf("plaintext: %v", err)
	}
	if creds != nil {
		t.Errorf("plaintext should return nil creds, got %v", creds)
	}
}
