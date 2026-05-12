// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	proxyv1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/proxy/v1alpha1"

	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/providers"
	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/providers/stub"
	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/server"
	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/state"
)

// newTestServer wires a Server with miniredis-backed state + a
// stub provider. Returns the server + cleanup. The discarding
// logger keeps test output quiet; switch to slog.New(slog.
// NewJSONHandler(os.Stdout, nil)) to debug locally.
func newTestServer(t *testing.T) *server.Server {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	st := state.New(rdb)

	stubProv, err := stub.New(stub.Config{
		URLs: []string{
			"http://stub-1.test:8080",
			"http://stub-2.test:8080",
		},
	})
	if err != nil {
		t.Fatalf("stub.New: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	srv, err := server.New(
		map[string]providers.Provider{stub.ProviderName: stubProv},
		[]string{stub.ProviderName},
		st,
		logger,
	)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	return srv
}

func TestAcquire_ReturnsPopulatedLease(t *testing.T) {
	srv := newTestServer(t)
	resp, err := srv.Acquire(context.Background(), &proxyv1alpha1.AcquireRequest{
		Region:       "US",
		Type:         proxyv1alpha1.ProxyType_PROXY_TYPE_RESIDENTIAL,
		TargetDomain: "example.com",
		TenantId:     "tenant-A",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	lease := resp.GetLease()
	if lease == nil {
		t.Fatal("nil lease")
	}
	if lease.GetLeaseId() == "" {
		t.Error("empty lease_id")
	}
	if lease.GetProxyUrl() == "" {
		t.Error("empty proxy_url")
	}
	if lease.GetProvider() != "stub" {
		t.Errorf("provider=%q, want stub", lease.GetProvider())
	}
	if !lease.GetExpiresAt().IsValid() {
		t.Error("invalid expires_at")
	}
}

func TestAcquire_InvalidArgument_NilRequest(t *testing.T) {
	srv := newTestServer(t)
	_, err := srv.Acquire(context.Background(), nil)
	if err == nil {
		t.Error("expected error on nil request")
	}
}

func TestAcquireBatch_PartialFillReportsErrors(t *testing.T) {
	srv := newTestServer(t)
	resp, err := srv.AcquireBatch(context.Background(), &proxyv1alpha1.AcquireBatchRequest{
		Constraints: &proxyv1alpha1.AcquireRequest{
			TenantId: "tenant-A",
		},
		Count: 3,
	})
	if err != nil {
		t.Fatalf("AcquireBatch: %v", err)
	}
	if got := len(resp.GetLeases()); got != 3 {
		t.Errorf("len(leases)=%d, want 3", got)
	}
	if got := len(resp.GetErrors()); got != 0 {
		t.Errorf("len(errors)=%d, want 0", got)
	}
}

func TestAcquireBatch_RejectsZeroCount(t *testing.T) {
	srv := newTestServer(t)
	_, err := srv.AcquireBatch(context.Background(), &proxyv1alpha1.AcquireBatchRequest{
		Constraints: &proxyv1alpha1.AcquireRequest{},
		Count:       0,
	})
	if err == nil {
		t.Error("expected error on count=0")
	}
}

func TestRelease_Idempotent(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	acquired, err := srv.Acquire(ctx, &proxyv1alpha1.AcquireRequest{TenantId: "tenant-A"})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	leaseID := acquired.GetLease().GetLeaseId()

	// First release succeeds.
	if _, err := srv.Release(ctx, &proxyv1alpha1.ReleaseRequest{
		LeaseId: leaseID, TenantId: "tenant-A",
	}); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	// Second release on the now-deleted lease is also OK
	// (idempotent — treated as already released).
	if _, err := srv.Release(ctx, &proxyv1alpha1.ReleaseRequest{
		LeaseId: leaseID, TenantId: "tenant-A",
	}); err != nil {
		t.Errorf("re-Release should be idempotent, got: %v", err)
	}
}

func TestRelease_CrossTenantRejected(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	acquired, err := srv.Acquire(ctx, &proxyv1alpha1.AcquireRequest{TenantId: "tenant-A"})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	_, err = srv.Release(ctx, &proxyv1alpha1.ReleaseRequest{
		LeaseId:  acquired.GetLease().GetLeaseId(),
		TenantId: "tenant-B",
	})
	if err == nil {
		t.Error("expected PermissionDenied on cross-tenant release")
	}
}

func TestReportFailure_SetsCooldownAndDecaysHealth(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	acquired, err := srv.Acquire(ctx, &proxyv1alpha1.AcquireRequest{
		TenantId:     "tenant-A",
		TargetDomain: "example.com",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	leaseID := acquired.GetLease().GetLeaseId()
	if _, err := srv.ReportFailure(ctx, &proxyv1alpha1.ReportFailureRequest{
		LeaseId:      leaseID,
		Kind:         proxyv1alpha1.FailureKind_FAILURE_KIND_BANNED,
		TenantId:     "tenant-A",
		TargetDomain: "example.com",
	}); err != nil {
		t.Fatalf("ReportFailure: %v", err)
	}
	// Cooldown should now be set for the proxy.
	d, err := srv.State.IsInCooldown(ctx, leaseID)
	if err != nil {
		t.Fatalf("IsInCooldown: %v", err)
	}
	if d <= 0 {
		t.Error("expected positive cooldown after BANNED report")
	}
	// Ban set for the (provider, domain) should include this lease.
	bans, err := srv.State.BannedProxiesFor(ctx, "stub", "example.com", 0)
	if err != nil {
		t.Fatalf("BannedProxiesFor: %v", err)
	}
	if len(bans) == 0 {
		t.Error("expected ban recorded for stub/example.com")
	}
}

func TestBudgetStatus_RequiresExactlyOneScope(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()

	// Zero scopes: error.
	if _, err := srv.BudgetStatus(ctx, &proxyv1alpha1.BudgetStatusRequest{}); err == nil {
		t.Error("expected error with zero scopes set")
	}
	// Two scopes: error.
	if _, err := srv.BudgetStatus(ctx, &proxyv1alpha1.BudgetStatusRequest{
		Provider: "stub",
		Region:   "US",
	}); err == nil {
		t.Error("expected error with multiple scopes set")
	}
	// Exactly one scope: success (synthetic shape in W5.1).
	resp, err := srv.BudgetStatus(ctx, &proxyv1alpha1.BudgetStatusRequest{Provider: "stub"})
	if err != nil {
		t.Fatalf("single-scope BudgetStatus: %v", err)
	}
	if resp.GetAvailable() == 0 {
		t.Error("expected synthetic Available > 0 in W5.1")
	}
}

func TestNew_RejectsEmptyProviders(t *testing.T) {
	if _, err := server.New(nil, []string{"stub"}, &state.Client{}, nil); err == nil {
		t.Error("expected error on empty provider map")
	}
}

func TestNew_RejectsUnknownProviderInOrder(t *testing.T) {
	stubProv, _ := stub.New(stub.Config{URLs: []string{"http://x.test:8080"}})
	if _, err := server.New(
		map[string]providers.Provider{stub.ProviderName: stubProv},
		[]string{"oxylabs"}, // unknown
		&state.Client{},
		nil,
	); err == nil {
		t.Error("expected error on provider order referencing unknown name")
	}
}
