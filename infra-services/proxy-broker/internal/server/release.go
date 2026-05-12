// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	proxyv1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/proxy/v1alpha1"

	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/providers"
)

// Release implements `proxy.ProxyServer`. Idempotent: an
// unknown / expired lease is treated as already-released
// (no error). Cross-tenant releases are rejected.
func (s *Server) Release(ctx context.Context, req *proxyv1alpha1.ReleaseRequest) (*proxyv1alpha1.ReleaseResponse, error) {
	if req == nil || req.GetLeaseId() == "" {
		return nil, status.Error(codes.InvalidArgument, "release: lease_id is required")
	}
	info, found, err := s.State.LookupLease(ctx, req.GetLeaseId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "release: lookup: %v", err)
	}
	if !found {
		// Already released or expired — idempotent no-op.
		s.Logger.DebugContext(ctx, "release: lease not found (already released or expired)",
			"lease_id", req.GetLeaseId(),
			"tenant_id", req.GetTenantId())
		return &proxyv1alpha1.ReleaseResponse{}, nil
	}
	if info.TenantID != req.GetTenantId() {
		s.Logger.WarnContext(ctx, "release: cross-tenant attempt rejected",
			"lease_id", req.GetLeaseId(),
			"lease_tenant", info.TenantID,
			"request_tenant", req.GetTenantId())
		return nil, status.Error(codes.PermissionDenied, "release: cross-tenant release rejected")
	}
	// Call provider-side Release (idempotent per the Provider
	// contract; stubs typically no-op).
	if provider, ok := s.Providers[info.Provider]; ok {
		if err := provider.Release(ctx, &providers.Lease{
			LeaseID:  req.GetLeaseId(),
			Provider: info.Provider,
		}); err != nil {
			// Don't fail the RPC on provider-side errors —
			// the lease record gets deleted regardless so
			// the broker doesn't accumulate stale state.
			s.Logger.WarnContext(ctx, "release: provider returned error (state cleanup proceeds)",
				"lease_id", req.GetLeaseId(),
				"provider", info.Provider,
				"error", err.Error())
		}
	}
	if err := s.State.DeleteLease(ctx, req.GetLeaseId()); err != nil {
		return nil, status.Errorf(codes.Internal, "release: delete lease: %v", err)
	}
	s.Logger.InfoContext(ctx, "release: lease released",
		"lease_id", req.GetLeaseId(),
		"provider", info.Provider,
		"tenant_id", info.TenantID)
	return &proxyv1alpha1.ReleaseResponse{}, nil
}
