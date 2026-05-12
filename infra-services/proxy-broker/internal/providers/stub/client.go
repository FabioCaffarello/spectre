// SPDX-License-Identifier: Apache-2.0

// Package stub is a placeholder second provider satisfying
// ADR-0028 §5 criterion #2 (two-provider design proof).
//
// The protocol abstraction is exercised by both BrightData
// (real provider, vendor-issued credentials) and stub
// (hardcoded URLs from config). The cross-provider unit test
// in `providers_test.go` runs the same scenarios against
// both — the test passing proves the `Provider` interface
// absorbs vendor variation per the admission gate.
//
// TODO(W5.1b): Replace with a real second provider (Oxylabs
// or Smartproxy candidates per ADR-0028 §4.1 known-providers
// list). Wave 4 pilot data (§1 acquisition-layer questionnaire)
// will inform the second-provider pick; until then the stub
// satisfies the admission gate without committing to a vendor
// before the pilot signal arrives.
//
// Tracked in: `docs/v1alpha2-audit.md` W5.1 row.
package stub

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/providers"
)

const (
	// ProviderName identifies the stub in wire-level
	// `Lease.provider`, observability, and the
	// per-(provider, domain) ban-tracking key prefix.
	ProviderName = "stub"

	// DefaultLeaseTTL caps stub lease lifetime. Short enough
	// that local-dev re-Acquires exercise the rotation path
	// without waiting; long enough that a single ScrapeJob
	// completes within one lease.
	DefaultLeaseTTL = 5 * time.Minute
)

// Config holds the hardcoded URL list the stub rotates
// through. Loaded from env (`SPECTRE_PROXY_BROKER_STUB_URLS`,
// comma-separated) by the broker's main.
type Config struct {
	// URLs is the pool the stub round-robins through. Each
	// `Acquire` advances the cursor; non-sticky leases return
	// the next URL; sticky leases stay on the same URL until
	// `Release`.
	URLs []string
}

// Client is the stub provider implementation.
type Client struct {
	urls   []string
	cursor atomic.Uint64
}

// New constructs a stub provider client. Returns an error
// when the URL list is empty — a stub with no URLs is a
// misconfiguration the broker surfaces fail-fast at startup.
func New(cfg Config) (*Client, error) {
	if len(cfg.URLs) == 0 {
		return nil, errors.New("stub: URL list must be non-empty " +
			"(set SPECTRE_PROXY_BROKER_STUB_URLS)")
	}
	// Defensive copy so callers can mutate the slice without
	// changing client state.
	urls := make([]string, len(cfg.URLs))
	copy(urls, cfg.URLs)
	return &Client{urls: urls}, nil
}

// Name implements `providers.Provider`.
func (c *Client) Name() string { return ProviderName }

// Acquire implements `providers.Provider`. Returns the next
// URL in the configured pool. Sticky leases get the cursor's
// current value snapshot at acquire time; non-sticky leases
// advance the cursor first so each acquire pulls a different
// URL.
func (c *Client) Acquire(_ context.Context, req providers.AcquireRequest) (*providers.Lease, error) {
	var idx uint64
	if req.Sticky {
		// Sticky: don't advance the cursor; the same URL is
		// returned for every Acquire-for-this-position. The
		// next non-sticky Acquire will move past it.
		idx = c.cursor.Load() % uint64(len(c.urls))
	} else {
		// Non-sticky: round-robin.
		idx = c.cursor.Add(1) % uint64(len(c.urls))
	}
	return &providers.Lease{
		LeaseID:   uuid.New().String(),
		ProxyURL:  c.urls[idx],
		Provider:  ProviderName,
		Region:    req.Region,
		ExpiresAt: time.Now().Add(DefaultLeaseTTL),
	}, nil
}

// Release implements `providers.Provider`. Pure no-op for the
// stub — no vendor-side state to update.
func (c *Client) Release(_ context.Context, _ *providers.Lease) error {
	return nil
}

// String aids debugging — returns a non-secret summary of
// the stub's pool.
func (c *Client) String() string {
	return fmt.Sprintf("stub-provider(urls=%d)", len(c.urls))
}
