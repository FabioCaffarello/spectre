// SPDX-License-Identifier: Apache-2.0

// Package sessions implements the in-memory session registry for
// the curl-impersonate adapter.
//
// The contract mirrors the SessionManager shapes from the
// SeleniumBase (Python) and Playwright (TypeScript) adapters but
// is HTTP-only: each session owns a cookie-jar file path that
// curl uses to persist cookies across multiple Navigates. PR11
// implements `Initialize` + `Navigate`; `Close`, `Query`, and
// `Extract` arrive in PR12 and will reuse the same session
// records (and the response-cache field) without changes here.
//
// See ADR-0016 §4 for the cookie-jar architecture rationale.
// See ADR-0014 §4 for why the three drivers re-implement the
// SessionManager shape rather than sharing a common contract
// (premature abstraction; ADR-0014 deferred extraction to "after
// the third driver lands" — that's PR11; the surface area is now
// visible but the extraction itself remains a v1alpha2 candidate).
package sessions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CookieJarDir is where session-scoped cookie-jar files live.
// /tmp keeps the path short on macOS (ADR-0008's UDS path-length
// motivation carries forward here even though cookie-jar paths
// have no length restriction — keeping all session-scoped files
// under one short directory eases the startup-sweep cleanup).
const CookieJarDir = "/tmp"

// CookieJarPattern is the glob fragment for session-scoped cookie
// jars. The startup sweep deletes stale files matching this
// pattern before opening for business.
const CookieJarPattern = "spectre-curl-*.cookies"

// ErrUnknownSession is returned when an RPC references a
// session_id that Initialize did not produce. The gRPC handler
// maps this to CODE_INVALID_ARGUMENT (ADR-0009 §2 — strict
// session_id validation, carried over to every adapter).
var ErrUnknownSession = errors.New("unknown session_id")

// Session is the per-id record. Future RPCs (PR12+) extend this
// struct with a response cache (status_code, final_url, body)
// without touching the session lifecycle below.
type Session struct {
	ID            string
	CookieJarPath string
	Created       time.Time
}

// Manager owns the live session registry and is concurrency-safe
// for register / has / close calls. Concurrent Navigate against
// the *same* session is undefined and not protected — see
// ADR-0016 §4 (operators must serialise per-session calls; the
// engine's per-session linear executor satisfies this naturally).
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	dir      string

	// uuidFn is overridable for tests so deterministic ids land
	// in the registry; production code uses uuid.NewString.
	uuidFn func() string
}

// NewManager constructs a Manager whose cookie-jar files live
// under the default CookieJarDir.
func NewManager() *Manager {
	return newManagerIn(CookieJarDir)
}

func newManagerIn(dir string) *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
		dir:      dir,
		uuidFn:   uuid.NewString,
	}
}

// SweepStale removes any cookie-jar files matching the pattern
// in the manager's directory. Called once at adapter startup so
// crashed prior runs do not leak files into the new run's
// session namespace. Safe to call concurrently with Create —
// the manager owns the namespace, not the disk.
func (m *Manager) SweepStale() error {
	matches, err := filepath.Glob(filepath.Join(m.dir, CookieJarPattern))
	if err != nil {
		return fmt.Errorf("sweep stale jars: glob: %w", err)
	}
	for _, path := range matches {
		// Best-effort: a removal failure does not block startup.
		// The next Navigate against a stale file would either
		// reuse it (harmless — same prefix means same protocol
		// version) or overwrite it.
		_ = os.Remove(path)
	}
	return nil
}

// Create allocates a new session: a fresh UUIDv4 id and a
// cookie-jar path under the manager's directory. The jar file
// itself is not created — curl creates it on first response
// with cookies. Returns the registered Session.
func (m *Manager) Create() *Session {
	id := m.uuidFn()
	jarPath := filepath.Join(m.dir, fmt.Sprintf("spectre-curl-%s.cookies", id))
	session := &Session{
		ID:            id,
		CookieJarPath: jarPath,
		Created:       time.Now(),
	}
	m.mu.Lock()
	m.sessions[id] = session
	m.mu.Unlock()
	return session
}

// Has returns true if the session_id is registered. Used by RPC
// handlers to reject unknown ids before doing any work.
func (m *Manager) Has(id string) bool {
	m.mu.Lock()
	_, ok := m.sessions[id]
	m.mu.Unlock()
	return ok
}

// Get returns the Session record for an id, or
// ErrUnknownSession if the id was never registered.
func (m *Manager) Get(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return nil, ErrUnknownSession
	}
	return session, nil
}

// Close removes a session from the registry and deletes its
// cookie-jar file. Returns ErrUnknownSession if the id was not
// registered. PR11 does not implement the gRPC `Close` RPC for
// curl-impersonate (PR12 does), so this method is exercised
// today only by CloseAll on shutdown.
func (m *Manager) Close(id string) error {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return ErrUnknownSession
	}
	delete(m.sessions, id)
	m.mu.Unlock()

	// Remove outside the lock so a slow filesystem does not
	// block other RPCs. The session is already de-registered.
	_ = os.Remove(session.CookieJarPath)
	return nil
}

// CloseAll evicts every session and removes every cookie-jar
// file. Idempotent; called from the SIGTERM handler.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	jarPaths := make([]string, 0, len(m.sessions))
	for _, session := range m.sessions {
		jarPaths = append(jarPaths, session.CookieJarPath)
	}
	m.sessions = make(map[string]*Session)
	m.mu.Unlock()

	for _, path := range jarPaths {
		_ = os.Remove(path)
	}
}
