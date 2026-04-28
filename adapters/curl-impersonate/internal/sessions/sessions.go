// SPDX-License-Identifier: Apache-2.0

// Package sessions implements the in-memory session registry for
// the curl-impersonate adapter, backed by Redis-resident metadata
// for the §5 restart-invalidation contract.
//
// Each session owns a cookie-jar file path that curl uses to
// persist cookies across multiple Navigates (ADR-0016 §4). PR12
// added the cached *goquery.Document per session for Query and
// Extract; R4.3 (this revision) externalises the session
// metadata to Redis under ``session:curl-impersonate:<id>`` per
// ADR-0023 §4 and adds the ``adapter_instance_id`` validation
// path the gRPC server uses to surface foreign-instance sessions
// as gRPC ``UNAVAILABLE``.
//
// The local registry remains the source of truth for cookie-jar
// paths and parsed documents; Redis is the source of truth for
// session existence (and adapter ownership). Together they
// implement the §5 contract:
//
//   - Local entry exists + Redis has the session for this
//     instance → RPC proceeds.
//   - Local entry missing + Redis has the session for a foreign
//     instance → RPC fails with ``UNAVAILABLE`` (the §5 restart
//     invalidation case).
//   - Redis has no entry → ``CODE_INVALID_ARGUMENT`` (unknown
//     session_id).
//
// See ADR-0014 §4 for why the three drivers re-implement the
// SessionManager shape rather than sharing a common contract;
// ADR-0023 §8 extends the same reasoning to per-language Redis
// libraries.
package sessions

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"

	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/elements"
	redisx "github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/redis"
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

// Session is the per-id record. Document is the parsed HTML
// cached from the most recent successful Navigate, or nil before
// the first Navigate.
type Session struct {
	ID            string
	CookieJarPath string
	Created       time.Time
	Document      *goquery.Document
}

// ValidationKind enumerates the result of Manager.Validate.
type ValidationKind int

const (
	// ValidationOK — Redis has the session and the stored
	// adapter_instance_id matches the manager's. The gRPC
	// handler proceeds with the RPC.
	ValidationOK ValidationKind = iota
	// ValidationUnknown — Redis has no entry for the id (never
	// created or TTL-expired). The gRPC handler returns
	// CODE_INVALID_ARGUMENT.
	ValidationUnknown
	// ValidationDifferentInstance — Redis has the session but
	// it belongs to a different adapter instance (the §5
	// restart-invalidation case). The gRPC handler returns
	// codes.Unavailable.
	ValidationDifferentInstance
)

// Validation is the typed result of Manager.Validate.
type Validation struct {
	Kind              ValidationKind
	Metadata          *redisx.SessionMetadata
	StoredInstanceID  string
}

// Manager owns the live session registry and is concurrency-safe
// for register / has / close calls. Concurrent Navigate against
// the *same* session is undefined and not protected — ADR-0016
// §4 (operators must serialise per-session calls; the engine's
// per-session linear executor satisfies this naturally).
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	dir      string
	registry *elements.Registry
	redis    *redisx.Client
	// instanceID is the per-process UUID (or env-var override)
	// stamped on every session metadata document. ADR-0023 §5.
	instanceID string

	// uuidFn is overridable for tests so deterministic ids land
	// in the registry; production code uses uuid.NewString.
	uuidFn func() string
}

// NewManager constructs a Manager whose cookie-jar files live
// under the default CookieJarDir.
func NewManager(redis *redisx.Client, instanceID string) *Manager {
	return newManagerIn(CookieJarDir, redis, instanceID)
}

func newManagerIn(dir string, redis *redisx.Client, instanceID string) *Manager {
	return &Manager{
		sessions:   make(map[string]*Session),
		dir:        dir,
		registry:   elements.NewRegistry(),
		redis:      redis,
		instanceID: instanceID,
		uuidFn:     uuid.NewString,
	}
}

// AdapterInstanceID exposes the manager's per-process UUID for
// tests and diagnostics.
func (m *Manager) AdapterInstanceID() string {
	return m.instanceID
}

// SweepStale removes any cookie-jar files matching the pattern
// in the manager's directory. Called once at adapter startup so
// crashed prior runs do not leak files into the new run's
// session namespace.
func (m *Manager) SweepStale() error {
	matches, err := filepath.Glob(filepath.Join(m.dir, CookieJarPattern))
	if err != nil {
		return fmt.Errorf("sweep stale jars: glob: %w", err)
	}
	for _, path := range matches {
		_ = os.Remove(path)
	}
	return nil
}

// Create allocates a new session: a fresh UUIDv4 id, a cookie-
// jar path under the manager's directory, and a Redis metadata
// write stamped with the manager's instanceID. Order matters:
// the local registry is updated only after a successful Redis
// write so a Redis failure leaves the manager unaware of the id
// and a retry produces a fresh write rather than the appearance
// of a registered-but-not-stored session.
//
// Returns the created Session or an error if Redis is
// unreachable (the gRPC handler maps the error to
// codes.Unavailable).
func (m *Manager) Create(ctx context.Context) (*Session, error) {
	id := m.uuidFn()
	jarPath := filepath.Join(m.dir, fmt.Sprintf("spectre-curl-%s.cookies", id))
	now := nowUTCISO()
	metadata := redisx.SessionMetadata{
		SessionID:         id,
		Adapter:           redisx.AdapterName,
		AdapterInstanceID: m.instanceID,
		CreatedAt:         now,
		LastActiveAt:      now,
		Metadata: map[string]any{
			"cookie_jar_path": jarPath,
		},
	}
	if err := m.redis.SetSession(ctx, id, metadata); err != nil {
		return nil, fmt.Errorf("redis SET session %s: %w", id, err)
	}
	session := &Session{
		ID:            id,
		CookieJarPath: jarPath,
		Created:       time.Now(),
	}
	m.mu.Lock()
	m.sessions[id] = session
	m.mu.Unlock()
	return session, nil
}

// Has returns true if the session_id is registered locally.
func (m *Manager) Has(id string) bool {
	m.mu.Lock()
	_, ok := m.sessions[id]
	m.mu.Unlock()
	return ok
}

// Get returns the Session record for an id, or
// ErrUnknownSession if the id was never registered locally.
func (m *Manager) Get(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return nil, ErrUnknownSession
	}
	return session, nil
}

// Validate looks up the session in Redis and compares the
// stored adapter_instance_id against the manager's instanceID.
// Refreshes last_active_at and the TTL on the OK path.
//
// The handler maps Validation.Kind as follows:
//
//   - ValidationOK              → proceed
//   - ValidationUnknown         → CODE_INVALID_ARGUMENT (unknown session_id)
//   - ValidationDifferentInstance → codes.Unavailable
//
// Redis errors (other than the missing-key case) propagate as
// the second return value; the handler maps them to
// codes.Unavailable so the conformance suite's restart-
// invalidation test pattern works against transient Redis
// failures the same way it works against actual instance
// mismatch.
func (m *Manager) Validate(ctx context.Context, id string) (Validation, error) {
	metadata, err := m.redis.GetSession(ctx, id)
	if err != nil {
		return Validation{}, err
	}
	if metadata == nil {
		return Validation{Kind: ValidationUnknown}, nil
	}
	if metadata.AdapterInstanceID != m.instanceID {
		return Validation{
			Kind:             ValidationDifferentInstance,
			StoredInstanceID: metadata.AdapterInstanceID,
		}, nil
	}
	metadata.LastActiveAt = nowUTCISO()
	if err := m.redis.SetSession(ctx, id, *metadata); err != nil {
		// Last-write-wins per phase prompt §4.5: a refresh
		// failure still proceeds; the next RPC will retry.
		// Log and continue rather than fail the validation.
		log.Printf("redis refresh failed for session %s: %v", id, err)
	}
	return Validation{Kind: ValidationOK, Metadata: metadata}, nil
}

// Close removes a session from the local registry, clears its
// ElementRegistry entry, deletes its cookie-jar file, and best-
// effort deletes the Redis key. Returns ErrUnknownSession if the
// id was not registered locally. Per phase prompt §4.6 the Redis
// delete is best-effort: failures are logged and the local
// teardown continues. The TTL is the safety net for the rare
// delete failure.
func (m *Manager) Close(ctx context.Context, id string) error {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return ErrUnknownSession
	}
	delete(m.sessions, id)
	m.mu.Unlock()

	m.registry.ForgetSession(id)
	if err := m.redis.DeleteSession(ctx, id); err != nil {
		log.Printf("redis delete failed for session %s: %v", id, err)
	}
	_ = os.Remove(session.CookieJarPath)
	return nil
}

// CloseAll evicts every session, clears every ElementRegistry
// entry, and removes every cookie-jar file. Idempotent; called
// from the SIGTERM handler. Does not enumerate Redis keys for
// deletion — restart invalidation handles abandoned keys via
// TTL expiry (ADR-0023 §5).
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
// generation counter so prior ElementRefs are invalidated.
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

// Document returns the cached *goquery.Document for a session,
// or nil when the session has had no successful Navigate yet.
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
// input order.
func (m *Manager) Allocate(id string, sel *goquery.Selection) []string {
	return m.registry.Allocate(id, sel)
}

// LookupElement resolves a UUID to an elements.Lookup whose
// Status distinguishes ok / stale / unknown.
func (m *Manager) LookupElement(id, refID string) elements.Lookup {
	return m.registry.Lookup(id, refID)
}

// CurrentGeneration returns the session's element-generation
// counter; useful for tests and diagnostics.
func (m *Manager) CurrentGeneration(id string) int {
	return m.registry.CurrentGeneration(id)
}

// nowUTCISO mirrors the Playwright (TS) and SeleniumBase
// (Python) timestamp formats so the JSON document round-trips
// identically through any adapter.
func nowUTCISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}
