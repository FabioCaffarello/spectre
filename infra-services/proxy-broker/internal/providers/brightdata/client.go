// SPDX-License-Identifier: Apache-2.0

// Package brightdata implements the BrightData provider for
// the proxy-broker. BrightData is wired end-to-end as the
// first real provider per ADR-0028 §5 criterion #6; the second
// provider (`stub`) is a placeholder satisfying the
// two-provider-design admission gate (criterion #2).
//
// BrightData's Super Proxy pattern doesn't require an API call
// per acquisition — proxy URLs are constructed from credentials
// + a per-session string embedded in the username, and the
// vendor side rotates exit IPs based on the session string and
// the zone's configured rotation policy. This keeps the
// `Acquire` path cheap (string formatting; no network) and
// `Release` a no-op (sessions expire on their own).
//
// Future enhancement (tracked in W5.1b): wire BrightData's
// zone-bw billing API (`https://api.brightdata.com/zone-bw/...`)
// so `BudgetStatus` can return real consumption figures rather
// than the synthetic placeholders the server currently returns.
package brightdata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/providers"
)

const (
	// ProviderName is the canonical string identifying this
	// provider in wire-level `Lease.provider`, observability,
	// and the broker's per-provider ban-tracking key prefix.
	ProviderName = "brightdata"

	// SuperProxyHost is BrightData's canonical Super Proxy
	// entrypoint. Constant across zones; the zone + session
	// information rides in the username field.
	SuperProxyHost = "brd.superproxy.io"

	// SuperProxyPort is BrightData's HTTPS-capable port.
	SuperProxyPort = 22225

	// DefaultLeaseTTL caps the broker's view of a BrightData
	// session lifetime. BrightData sessions don't have a hard
	// vendor-side expiry; this cap drives the broker's
	// re-Acquire cadence (sticky sessions get fresh IPs after
	// the cap regardless of provider behaviour).
	DefaultLeaseTTL = 10 * time.Minute
)

// Config is the BrightData provider configuration loaded from
// env at startup. The proxy-broker binary's `cmd/proxy-broker/
// main.go` (B.5) constructs this and passes it to `New`.
type Config struct {
	// Username — BrightData account username, typically
	// `lum-customer-<account>-zone-<zone>` for zone-scoped
	// accounts. Read from `BRIGHTDATA_USERNAME` env.
	Username string

	// Password — BrightData account password. Read from
	// `BRIGHTDATA_PASSWORD` env. Never logged; never
	// surfaced in error messages.
	Password string

	// Zone — BrightData zone name (overrides any zone embedded
	// in `Username`). Read from `BRIGHTDATA_ZONE` env. Empty
	// when the username already embeds the zone.
	Zone string
}

// Client is BrightData's provider implementation.
type Client struct {
	cfg Config
}

// New constructs a BrightData provider client. Returns an
// error when required credentials are missing; the broker's
// startup surfaces this as a fail-fast misconfiguration.
func New(cfg Config) (*Client, error) {
	if cfg.Username == "" {
		return nil, errors.New("brightdata: BRIGHTDATA_USERNAME is required")
	}
	if cfg.Password == "" {
		return nil, errors.New("brightdata: BRIGHTDATA_PASSWORD is required")
	}
	return &Client{cfg: cfg}, nil
}

// Name implements `providers.Provider`.
func (c *Client) Name() string { return ProviderName }

// Acquire implements `providers.Provider`. Constructs a Super
// Proxy URL with the session string embedded in the username.
// Sticky leases get a stable session ID for the lease lifetime;
// non-sticky leases get a fresh session every Acquire (rotating
// behaviour from the vendor side).
func (c *Client) Acquire(_ context.Context, req providers.AcquireRequest) (*providers.Lease, error) {
	leaseID := uuid.New().String()
	sessionID := sessionFromLease(leaseID, req.Sticky)

	username := c.buildUsername(sessionID, req)
	proxyURL := fmt.Sprintf("http://%s:%s@%s:%d",
		username, c.cfg.Password, SuperProxyHost, SuperProxyPort)

	return &providers.Lease{
		LeaseID:   leaseID,
		ProxyURL:  proxyURL,
		Provider:  ProviderName,
		Region:    req.Region,
		ExpiresAt: time.Now().Add(DefaultLeaseTTL),
	}, nil
}

// Release implements `providers.Provider`. No-op — BrightData
// sessions expire vendor-side without a release API.
func (c *Client) Release(_ context.Context, _ *providers.Lease) error {
	return nil
}

// buildUsername composes the BrightData Super Proxy username
// from the account name + session + per-acquire constraints.
// Format follows BrightData's documented `-zone-<z>-session-<s>
// [-country-<cc>]` suffixing.
func (c *Client) buildUsername(sessionID string, req providers.AcquireRequest) string {
	parts := []string{c.cfg.Username}
	if c.cfg.Zone != "" {
		parts = append(parts, "zone-"+c.cfg.Zone)
	}
	parts = append(parts, "session-"+sessionID)
	if req.Region != "" {
		parts = append(parts, "country-"+strings.ToLower(req.Region))
	}
	return strings.Join(parts, "-")
}

// sessionFromLease derives the BrightData session string from
// the lease ID. Sticky leases use the lease ID directly so
// every Acquire-for-the-same-lease produces the same session
// string (and thus the same exit IP from vendor side). Non-
// sticky leases get a fresh random suffix on every call so
// the vendor rotates the exit IP per request.
func sessionFromLease(leaseID string, sticky bool) string {
	if sticky {
		// Strip dashes — BrightData session identifiers are
		// alphanumeric per their docs; a UUID's hex without
		// dashes satisfies that and stays under their length
		// cap.
		return strings.ReplaceAll(leaseID, "-", "")
	}
	// Fresh session per acquire = vendor rotates IP per
	// request. Use the lease ID's first 8 hex chars + a
	// nanosecond suffix for uniqueness across rapid
	// successive non-sticky Acquires.
	prefix := strings.ReplaceAll(leaseID, "-", "")
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano()&0xffffff)
}
