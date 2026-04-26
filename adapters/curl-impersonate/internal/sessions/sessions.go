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

	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"

	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/elements"
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

// Session is the per-id record. PR12 adds Document, the parsed
// HTML cached from the most recent successful Navigate. Query
// resolves selectors against this document; Extract reads field
// values off element references whose generation matches the
// session's current generation in the ElementRegistry. The
// document is replaced (not mutated) on every Navigate; the
// generation bump that accompanies the replacement invalidates
// every prior ElementRef. ADR-0017 §3 records the lifecycle.
type Session struct {
	ID            string
	CookieJarPath string
	Created       time.Time

	// Document is the parsed HTML from the most recent successful
	// Navigate, or nil before the first Navigate. The session
	// manager's mutex guards access; handlers should obtain it
	// via Manager.Document(id).
	Document *goquery.Document
}

// Manager owns the live session registry and is concurrency-safe
// for register / has / close calls. Concurrent Navigate against
// the *same* session is undefined and not protected — see
// ADR-0016 §4 (operators must serialise per-session calls; the
// engine's per-session linear executor satisfies this naturally).
//
// PR12 adds the ElementRegistry as a Manager-owned field. Handlers
// reach element-related state through Manager methods rather than
// the registry directly; that keeps the contract identical to the
// SeleniumBase SessionManager (sessions.py) and Playwright
// SessionManager (sessions.ts).
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	dir      string
	registry *elements.Registry

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
		registry: elements.NewRegistry(),
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

// Close removes a session from the registry, clears its
// ElementRegistry entry, and deletes its cookie-jar file. Returns
// ErrUnknownSession if the id was not registered. The
// ElementRegistry is forgotten before the cookie-jar is removed
// so a late Lookup cannot resolve to a Selection from the
// document of a session that is mid-teardown.
func (m *Manager) Close(id string) error {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return ErrUnknownSession
	}
	delete(m.sessions, id)
	m.mu.Unlock()

	// ForgetSession is concurrency-safe internally and does not
	// share the manager's mutex.
	m.registry.ForgetSession(id)

	// Remove outside the lock so a slow filesystem does not
	// block other RPCs. The session is already de-registered.
	_ = os.Remove(session.CookieJarPath)
	return nil
}

// CloseAll evicts every session, clears every ElementRegistry
// entry, and removes every cookie-jar file. Idempotent; called
// from the SIGTERM handler.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	jarPaths := make([]string, 0, len(m.sessions))
	for id, session := range m.sessions {
		ids = append(ids, id)
		jarPaths = append(jarPaths, session.CookieJarPath)
	}
	m.sessions = make(map[string]*Session)
	m.mu.Unlock()

	for _, id := range ids {
		m.registry.ForgetSession(id)
	}
	for _, path := range jarPaths {
		_ = os.Remove(path)
	}
}

// SetDocument caches the parsed HTML for a session and bumps the
// generation counter so prior ElementRefs are invalidated. The
// generation bump is part of SetDocument so callers cannot
// accidentally cache a new document without invalidating refs
// against the old one — ADR-0010 §1's strict-invalidation
// contract is what motivates the coupling.
//
// Returns ErrUnknownSession when the id is not registered.
func (m *Manager) SetDocument(id string, doc *goquery.Document) error {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return ErrUnknownSession
	}
	session.Document = doc
	m.mu.Unlock()

	m.registry.BumpGeneration(id)
	return nil
}

// Document returns the cached *goquery.Document for a session, or
// nil when the session has had no successful Navigate yet. Does
// not return ErrUnknownSession — callers (Query, Extract) check
// Has first; the dual return would just force an extra error
// branch in already-validated code paths.
func (m *Manager) Document(id string) *goquery.Document {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return nil
	}
	return session.Document
}

// Allocate stores each node in the selection under a fresh UUID
// at the session's current generation and returns the ids in
// input order. Delegates to the ElementRegistry.
func (m *Manager) Allocate(id string, sel *goquery.Selection) []string {
	return m.registry.Allocate(id, sel)
}

// LookupElement resolves a UUID to an elements.Lookup whose Status
// distinguishes ok / stale / unknown.
func (m *Manager) LookupElement(id, refID string) elements.Lookup {
	return m.registry.Lookup(id, refID)
}

// CurrentGeneration returns the session's element-generation
// counter; useful for tests and diagnostics.
func (m *Manager) CurrentGeneration(id string) int {
	return m.registry.CurrentGeneration(id)
}
