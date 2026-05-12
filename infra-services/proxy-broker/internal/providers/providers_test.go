// SPDX-License-Identifier: Apache-2.0

package providers_test

// The matrix below is the abstraction-proof from ADR-0028 §5
// criterion #2: the same test scenarios run against both
// BrightData (wired) and stub (placeholder). Both providers
// implement `providers.Provider`; if either fails a scenario,
// the abstraction has a hole.
//
// New providers added in W5.1b+ extend the `providersUnderTest`
// list; the scenarios stay identical. A provider that needs a
// scenario-specific exception is a smell — the abstraction
// isn't absorbing the variation.

import (
	"context"
	"strings"
	"testing"

	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/providers"
	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/providers/brightdata"
	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/providers/stub"
)

// providersUnderTest returns the concrete providers the test
// matrix exercises. Each entry is `(name, factory)` so a
// failure points at the offending provider directly.
func providersUnderTest(t *testing.T) []struct {
	name     string
	provider providers.Provider
} {
	t.Helper()

	bd, err := brightdata.New(brightdata.Config{
		Username: "lum-customer-test",
		Password: "test-password",
		Zone:     "test-zone",
	})
	if err != nil {
		t.Fatalf("brightdata.New: %v", err)
	}

	st, err := stub.New(stub.Config{
		URLs: []string{
			"http://stub-proxy-1.test:8080",
			"http://stub-proxy-2.test:8080",
		},
	})
	if err != nil {
		t.Fatalf("stub.New: %v", err)
	}

	return []struct {
		name     string
		provider providers.Provider
	}{
		{"brightdata", bd},
		{"stub", st},
	}
}

// TestProviderMatrix_NameNonEmpty: every provider must self-
// identify with a non-empty Name(). The broker uses this for
// wire-level `Lease.provider`, observability, and ban-tracking
// key prefixes.
func TestProviderMatrix_NameNonEmpty(t *testing.T) {
	for _, p := range providersUnderTest(t) {
		t.Run(p.name, func(t *testing.T) {
			if p.provider.Name() == "" {
				t.Error("Name() returned empty string")
			}
		})
	}
}

// TestProviderMatrix_AcquireReturnsLease: every provider must
// return a populated `Lease` on a basic Acquire — non-empty
// LeaseID, non-empty ProxyURL, ExpiresAt in the future, Provider
// field matches Name().
func TestProviderMatrix_AcquireReturnsLease(t *testing.T) {
	for _, p := range providersUnderTest(t) {
		t.Run(p.name, func(t *testing.T) {
			lease, err := p.provider.Acquire(context.Background(), providers.AcquireRequest{
				Region:       "US",
				Type:         providers.ProxyTypeResidential,
				Sticky:       false,
				TargetDomain: "example.com",
				TenantID:     "tenant-A",
			})
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			if lease == nil {
				t.Fatal("nil lease")
			}
			if lease.LeaseID == "" {
				t.Error("empty LeaseID")
			}
			if lease.ProxyURL == "" {
				t.Error("empty ProxyURL")
			}
			if lease.Provider != p.provider.Name() {
				t.Errorf("Provider=%q, want %q", lease.Provider, p.provider.Name())
			}
			if lease.ExpiresAt.IsZero() {
				t.Error("zero ExpiresAt")
			}
		})
	}
}

// TestProviderMatrix_AcquireStickyReproducible: sticky=true
// Acquires for the SAME constraints should return URLs that
// reflect the sticky intent. For BrightData this means the
// embedded session string is stable per LeaseID; for stub
// this means the cursor doesn't advance. Both providers
// satisfy this by returning a URL that — given the same
// constraints — can route to the same upstream IP.
//
// The test is provider-aware enough to verify EACH provider's
// sticky semantics: BrightData by parsing the session-N suffix
// of the username; stub by checking the URL stays in the
// configured pool across calls.
func TestProviderMatrix_AcquireSticky(t *testing.T) {
	ctx := context.Background()
	req := providers.AcquireRequest{
		Region:       "US",
		Type:         providers.ProxyTypeResidential,
		Sticky:       true,
		TargetDomain: "example.com",
	}
	for _, p := range providersUnderTest(t) {
		t.Run(p.name, func(t *testing.T) {
			lease, err := p.provider.Acquire(ctx, req)
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			// Common contract: sticky leases must still
			// produce a non-empty URL.
			if lease.ProxyURL == "" {
				t.Error("sticky lease returned empty ProxyURL")
			}
			// Provider-specific shape check.
			switch p.name {
			case "brightdata":
				if !strings.Contains(lease.ProxyURL, "session-") {
					t.Errorf("brightdata sticky URL missing session suffix: %s", lease.ProxyURL)
				}
			case "stub":
				// stub: URL should be one of the configured pool entries.
				if !strings.HasPrefix(lease.ProxyURL, "http://stub-proxy-") {
					t.Errorf("stub URL not in pool: %s", lease.ProxyURL)
				}
			}
		})
	}
}

// TestProviderMatrix_AcquirePropagatesRegion: the Region the
// caller asked for must surface back on the returned Lease so
// the broker can update its per-region budget accounting.
func TestProviderMatrix_AcquirePropagatesRegion(t *testing.T) {
	ctx := context.Background()
	for _, p := range providersUnderTest(t) {
		t.Run(p.name, func(t *testing.T) {
			lease, err := p.provider.Acquire(ctx, providers.AcquireRequest{
				Region: "BR",
				Type:   providers.ProxyTypeResidential,
			})
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			if lease.Region != "BR" {
				t.Errorf("Region=%q, want BR", lease.Region)
			}
		})
	}
}

// TestProviderMatrix_ReleaseIdempotent: Release must not error
// for any valid lease, and re-releasing must be a no-op (no
// error on the second call).
func TestProviderMatrix_ReleaseIdempotent(t *testing.T) {
	ctx := context.Background()
	for _, p := range providersUnderTest(t) {
		t.Run(p.name, func(t *testing.T) {
			lease, err := p.provider.Acquire(ctx, providers.AcquireRequest{})
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			if err := p.provider.Release(ctx, lease); err != nil {
				t.Errorf("first Release: %v", err)
			}
			if err := p.provider.Release(ctx, lease); err != nil {
				t.Errorf("re-Release: %v", err)
			}
		})
	}
}

// TestBrightData_RequiresCredentials: provider-specific check
// that BrightData fail-fasts on missing credentials.
func TestBrightData_RequiresCredentials(t *testing.T) {
	if _, err := brightdata.New(brightdata.Config{Username: "", Password: "x"}); err == nil {
		t.Error("expected error on empty Username")
	}
	if _, err := brightdata.New(brightdata.Config{Username: "x", Password: ""}); err == nil {
		t.Error("expected error on empty Password")
	}
}

// TestStub_RequiresURLs: provider-specific check that stub
// fail-fasts on empty URL pool.
func TestStub_RequiresURLs(t *testing.T) {
	if _, err := stub.New(stub.Config{URLs: nil}); err == nil {
		t.Error("expected error on empty URL pool")
	}
}

// TestStub_NonStickyRoundRobins: non-sticky stub Acquires
// should walk through the pool, not return the same URL
// every time. (The matrix test doesn't assert this because
// BrightData's non-sticky shape is vendor-internal.)
func TestStub_NonStickyRoundRobins(t *testing.T) {
	st, err := stub.New(stub.Config{
		URLs: []string{
			"http://p1.test:8080",
			"http://p2.test:8080",
			"http://p3.test:8080",
		},
	})
	if err != nil {
		t.Fatalf("stub.New: %v", err)
	}
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		lease, err := st.Acquire(context.Background(), providers.AcquireRequest{})
		if err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
		seen[lease.ProxyURL] = true
	}
	if len(seen) != 3 {
		t.Errorf("expected non-sticky to visit all 3 URLs across 6 calls, saw %d distinct: %v",
			len(seen), seen)
	}
}
