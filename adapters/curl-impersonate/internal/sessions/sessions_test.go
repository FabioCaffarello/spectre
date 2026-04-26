// SPDX-License-Identifier: Apache-2.0

package sessions

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	m := newManagerIn(dir)
	// Deterministic UUIDs for assertions.
	counter := 0
	m.uuidFn = func() string {
		counter++
		return "test-id-" + string(rune('a'+counter-1))
	}
	return m
}

func TestCreateAllocatesIDAndJarPath(t *testing.T) {
	m := newTestManager(t)
	s := m.Create()
	if s.ID != "test-id-a" {
		t.Fatalf("expected deterministic id 'test-id-a', got %q", s.ID)
	}
	if !strings.HasSuffix(s.CookieJarPath, "spectre-curl-test-id-a.cookies") {
		t.Fatalf("jar path: %q", s.CookieJarPath)
	}
	if s.Created.IsZero() {
		t.Fatal("Created timestamp must be set")
	}
}

func TestHasGetUnknown(t *testing.T) {
	m := newTestManager(t)
	if m.Has("nope") {
		t.Fatal("Has should be false for unregistered id")
	}
	if _, err := m.Get("nope"); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("expected ErrUnknownSession, got %v", err)
	}
	s := m.Create()
	if !m.Has(s.ID) {
		t.Fatal("Has should be true after Create")
	}
	got, err := m.Get(s.ID)
	if err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if got.ID != s.ID {
		t.Fatalf("Get returned wrong session: %v", got)
	}
}

func TestCloseRemovesSessionAndJarFile(t *testing.T) {
	m := newTestManager(t)
	s := m.Create()
	// Simulate curl having written a cookie-jar file for this
	// session.
	if err := os.WriteFile(s.CookieJarPath, []byte("# cookies\n"), 0o600); err != nil {
		t.Fatalf("write jar: %v", err)
	}

	if err := m.Close(s.ID); err != nil {
		t.Fatalf("Close err: %v", err)
	}
	if m.Has(s.ID) {
		t.Fatal("Has should be false after Close")
	}
	if _, err := os.Stat(s.CookieJarPath); !os.IsNotExist(err) {
		t.Fatalf("jar file should be gone, stat err: %v", err)
	}

	// Idempotent: a second Close returns ErrUnknownSession.
	if err := m.Close(s.ID); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("second Close should return ErrUnknownSession, got %v", err)
	}
}

func TestCloseAllRemovesEveryJar(t *testing.T) {
	m := newTestManager(t)
	a := m.Create()
	b := m.Create()
	for _, p := range []string{a.CookieJarPath, b.CookieJarPath} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	m.CloseAll()

	if m.Has(a.ID) || m.Has(b.ID) {
		t.Fatal("CloseAll must evict every session")
	}
	for _, p := range []string{a.CookieJarPath, b.CookieJarPath} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("jar %q should be gone, stat err: %v", p, err)
		}
	}

	// Idempotent: calling again is a no-op.
	m.CloseAll()
}

func TestSweepStaleRemovesPriorRunFiles(t *testing.T) {
	dir := t.TempDir()
	m := newManagerIn(dir)

	stale := filepath.Join(dir, "spectre-curl-prior.cookies")
	keep := filepath.Join(dir, "unrelated.txt")
	for _, p := range []string{stale, keep} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := m.SweepStale(); err != nil {
		t.Fatalf("SweepStale: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale jar should be removed, stat err: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("unrelated file must survive sweep: %v", err)
	}
}
