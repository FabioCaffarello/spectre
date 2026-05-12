// SPDX-License-Identifier: Apache-2.0

// Package providers holds the proxy-broker's vendor-integration
// surface. Each concrete provider lives in its own subpackage
// implementing the `Provider` interface; the server layer holds
// a registry of providers and selects one per `Acquire` request.
//
// Provider responsibilities are intentionally minimal:
//
//  1. Construct or retrieve a proxy URL given constraints.
//  2. Notify on release (some billing-aware providers track
//     the active-lease count; others ignore Release entirely).
//
// State management (cooldowns, ban tracking, health scores) is
// the broker's responsibility, not the provider's. Keeping the
// abstraction this narrow is the deliberate design choice from
// ADR-0028 §5 criterion #2 — the protocol surface must absorb
// vendor variation, which means vendor code stays small.
package providers

import (
	"context"
	"time"
)

// ProxyType mirrors the wire-level ProxyType enum so providers
// can switch on it without importing the generated proto stubs
// (keeps each subpackage proto-free; the server layer translates
// proto → providers.ProxyType once at the boundary).
type ProxyType string

const (
	ProxyTypeUnspecified ProxyType = ""
	ProxyTypeResidential ProxyType = "residential"
	ProxyTypeDatacenter  ProxyType = "datacenter"
	ProxyTypeMobile      ProxyType = "mobile"
	ProxyTypeISP         ProxyType = "isp"
)

// AcquireRequest carries the same constraints as the wire-level
// AcquireRequest but in provider-internal types.
type AcquireRequest struct {
	// Region as ISO 3166-1 alpha-2 (e.g. "US"); empty = any.
	Region string
	// Type defaults to Residential when Unspecified.
	Type ProxyType
	// Sticky requests same exit IP for the lease lifetime.
	Sticky bool
	// TargetDomain drives per-domain cooldown coordination
	// (provider doesn't see ban state — the broker filters
	// before calling Acquire — but the provider may use it
	// for vendor-side session affinity hints).
	TargetDomain string
	// TenantID for per-tenant accounting.
	TenantID string
}

// Lease is the provider's internal lease shape. The server
// layer translates this back to the wire-level Lease message.
type Lease struct {
	// LeaseID is provider-issued and opaque to the broker.
	// Used in Release calls so the provider can locate the
	// underlying session.
	LeaseID string
	// ProxyURL the consumer dials. Includes credentials when
	// the provider uses embedded auth (BrightData superproxy
	// pattern); may also be a clean URL when the provider
	// uses out-of-band auth (header-based, IP-allowlisted).
	ProxyURL string
	// Provider name (matches `Provider.Name()`).
	Provider string
	// Region observed at acquisition; may be empty when the
	// provider doesn't expose per-IP geo.
	Region string
	// ExpiresAt is the lease's vendor-side expiry. The broker
	// caps this at its own configured maximum.
	ExpiresAt time.Time
}

// Provider is the abstraction every concrete proxy provider
// implements. The interface is the proof point for ADR-0028 §5
// criterion #2: both BrightData (wired) and stub (placeholder)
// implement this interface, and the broker's test matrix runs
// the same scenarios against both.
type Provider interface {
	// Name returns the provider's identifier (e.g. "brightdata",
	// "stub"). Used for the wire-level `Lease.provider` field +
	// per-provider observability + the per-(provider, domain)
	// ban-tracking key prefix.
	Name() string

	// Acquire returns a proxy lease matching the constraints, or
	// an error indicating why no proxy could be acquired. The
	// implementation should NOT consult cooldown / ban state —
	// the broker filters proxies before calling Acquire.
	Acquire(ctx context.Context, req AcquireRequest) (*Lease, error)

	// Release notifies the provider that a lease is no longer
	// needed. Idempotent — re-releasing or releasing an unknown
	// lease must not error. Some providers (BrightData
	// per-session billing) decrement counters here; others
	// (the stub) treat it as a no-op.
	Release(ctx context.Context, lease *Lease) error
}
