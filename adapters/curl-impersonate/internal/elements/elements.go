// SPDX-License-Identifier: Apache-2.0

// Package elements implements the ElementRef registry for the
// curl-impersonate adapter.
//
// Query allocates UUIDv4 ids for each *goquery.Selection node it
// returns; Extract looks the id up to recover the selection.
// Refs are scoped to a session and tagged with a generation
// counter that bumps on every Navigate. An Extract against a ref
// whose generation does not match the session's current
// generation is rejected with CODE_INVALID_ARGUMENT — see
// ADR-0010 §1 (strict invalidation, fail-loud over fail-silent),
// carried forward to curl-impersonate by ADR-0017 §3.
//
// The registry intentionally mirrors the SeleniumBase Python
// (elements.py) and Playwright TypeScript (elements.ts) shapes
// byte-for-byte. Three drivers, three stored handle types
// (Locator, WebElement, *goquery.Selection), one contract:
// allocate-on-Query, lookup-on-Extract, generation-tagged. The
// curl-impersonate adapter has no mid-generation staleness
// failure mode (the parsed document is immutable), so the
// page-state-change distinction from ADR-0015 §2 is structurally
// a no-op here. ADR-0017 §3 records that as a positive
// consequence of the static-HTML model.
package elements

import (
	"sync"

	"github.com/PuerkitoBio/goquery"
	"github.com/google/uuid"
)

// LookupStatus describes the result of a Lookup.
//
//   - StatusOK      — the ref is valid; Selection is populated.
//   - StatusUnknown — the id was never issued for this session,
//     or the session has been forgotten.
//   - StatusStale   — the id was issued in an earlier generation
//     that has since been invalidated by a Navigate.
type LookupStatus int

const (
	StatusOK LookupStatus = iota
	StatusUnknown
	StatusStale
)

// Lookup is the result of Registry.Lookup. The Selection is
// populated only when Status == StatusOK.
type Lookup struct {
	Status    LookupStatus
	Selection *goquery.Selection
}

type elementRef struct {
	selection  *goquery.Selection
	generation int
}

type sessionEntry struct {
	generation int
	refs       map[string]elementRef
}

// Registry is a per-session UUID → *goquery.Selection store with
// generation tagging. Concurrency-safe; the gRPC handlers run
// each request on its own goroutine and may race on a shared
// registry instance even when the per-session call serialisation
// rule from ADR-0016 §4 is honoured.
type Registry struct {
	mu       sync.Mutex
	sessions map[string]*sessionEntry

	// uuidFn is overridable for tests so deterministic ids land
	// in the registry; production code uses uuid.NewString.
	uuidFn func() string
}

// NewRegistry constructs an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		sessions: make(map[string]*sessionEntry),
		uuidFn:   uuid.NewString,
	}
}

// CurrentGeneration returns the generation counter for a session,
// or zero when the session has no entry yet. Mirrors the
// SeleniumBase ElementRegistry.current_generation contract.
func (r *Registry) CurrentGeneration(sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.sessions[sessionID]
	if !ok {
		return 0
	}
	return entry.generation
}

// BumpGeneration increments the generation counter for a session.
//
// Prior refs are *not* removed — they remain in the map so a
// subsequent Lookup can distinguish stale (id was issued in an
// earlier generation) from unknown (id was never issued for this
// session). Entries are dropped only when the session is
// forgotten via ForgetSession. Calling BumpGeneration on a
// session that has no entry yet creates one at generation 1 —
// matches the Python behaviour from ADR-0010 § registry section.
func (r *Registry) BumpGeneration(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.sessions[sessionID]
	if !ok {
		r.sessions[sessionID] = &sessionEntry{
			generation: 1,
			refs:       make(map[string]elementRef),
		}
		return
	}
	entry.generation++
}

// ForgetSession removes the session's entry entirely. Called on
// Close. Idempotent.
func (r *Registry) ForgetSession(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, sessionID)
}

// Allocate stores each non-nil node in the selection under a
// fresh UUIDv4 id at the session's current generation, returning
// the ids in input order. The selection is split into single-node
// selections so each ElementRef refers to exactly one node, which
// keeps the Extract mapping table from ADR-0017 §5 unambiguous.
func (r *Registry) Allocate(sessionID string, sel *goquery.Selection) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.sessions[sessionID]
	if !ok {
		entry = &sessionEntry{
			generation: 0,
			refs:       make(map[string]elementRef),
		}
		r.sessions[sessionID] = entry
	}
	if sel == nil {
		return nil
	}
	ids := make([]string, 0, sel.Length())
	sel.Each(func(_ int, single *goquery.Selection) {
		id := r.uuidFn()
		entry.refs[id] = elementRef{
			selection:  single,
			generation: entry.generation,
		}
		ids = append(ids, id)
	})
	return ids
}

// Lookup resolves a UUID to a Lookup whose Status distinguishes
// ok / stale / unknown. See LookupStatus for the semantic split.
func (r *Registry) Lookup(sessionID, refID string) Lookup {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.sessions[sessionID]
	if !ok {
		return Lookup{Status: StatusUnknown}
	}
	ref, ok := entry.refs[refID]
	if !ok {
		return Lookup{Status: StatusUnknown}
	}
	if ref.generation != entry.generation {
		return Lookup{Status: StatusStale}
	}
	return Lookup{Status: StatusOK, Selection: ref.selection}
}
