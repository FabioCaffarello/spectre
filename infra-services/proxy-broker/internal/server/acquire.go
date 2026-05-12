// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	proxyv1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/proxy/v1alpha1"

	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/providers"
	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/state"
)

// Acquire implements `proxy.ProxyServer`. Picks a provider,
// delegates the URL construction, records the lease in
// state, and returns the wire-level Lease.
func (s *Server) Acquire(ctx context.Context, req *proxyv1alpha1.AcquireRequest) (*proxyv1alpha1.AcquireResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "acquire: request is nil")
	}
	provReq := providers.AcquireRequest{
		Region:       req.GetRegion(),
		Type:         proxyTypeFromProto(req.GetType()),
		Sticky:       req.GetSticky(),
		TargetDomain: req.GetTargetDomain(),
		TenantID:     req.GetTenantId(),
	}
	provider := s.pickProvider(ctx, provReq)
	lease, err := provider.Acquire(ctx, provReq)
	if err != nil {
		s.Logger.WarnContext(ctx, "acquire: provider returned error",
			"provider", provider.Name(),
			"region", provReq.Region,
			"target_domain", provReq.TargetDomain,
			"tenant_id", provReq.TenantID,
			"error", err.Error())
		return nil, status.Errorf(codes.Unavailable, "acquire: %s: %v", provider.Name(), err)
	}
	// Record lease so Release / ReportFailure can locate the
	// provider + tenant context. TTL slightly longer than the
	// lease expiry so the record is around for the last-mile
	// reports; expired records auto-cleanup.
	ttl := time.Until(lease.ExpiresAt) + 1*time.Minute
	if ttl <= 0 {
		// Defensive: a provider returning a past-dated lease is
		// a vendor bug. Refuse the lease rather than recording
		// it with non-positive TTL.
		return nil, status.Error(codes.Internal, "acquire: provider returned expired lease")
	}
	info := state.LeaseInfo{
		ProxyID:      lease.LeaseID,
		Provider:     lease.Provider,
		TenantID:     provReq.TenantID,
		TargetDomain: provReq.TargetDomain,
		IssuedAt:     time.Now(),
	}
	if err := s.State.RecordLease(ctx, lease.LeaseID, info, ttl); err != nil {
		s.Logger.ErrorContext(ctx, "acquire: state.RecordLease",
			"lease_id", lease.LeaseID,
			"tenant_id", provReq.TenantID,
			"error", err.Error())
		return nil, status.Errorf(codes.Internal, "acquire: record lease: %v", err)
	}
	s.Logger.InfoContext(ctx, "acquire: lease issued",
		"lease_id", lease.LeaseID,
		"provider", lease.Provider,
		"region", lease.Region,
		"target_domain", provReq.TargetDomain,
		"tenant_id", provReq.TenantID,
		"sticky", provReq.Sticky)

	return &proxyv1alpha1.AcquireResponse{
		Lease: &proxyv1alpha1.Lease{
			LeaseId:   lease.LeaseID,
			ProxyUrl:  lease.ProxyURL,
			Provider:  lease.Provider,
			Region:    lease.Region,
			ExpiresAt: timestamppb.New(lease.ExpiresAt),
		},
	}, nil
}

// AcquireBatch implements `proxy.ProxyServer`. Issues N
// `Acquire`s serially and packs results into a single batch
// response with per-slot errors. Future: parallel issuance
// when providers support concurrent calls (BrightData does;
// stub trivially does).
func (s *Server) AcquireBatch(ctx context.Context, req *proxyv1alpha1.AcquireBatchRequest) (*proxyv1alpha1.AcquireBatchResponse, error) {
	if req == nil || req.GetConstraints() == nil {
		return nil, status.Error(codes.InvalidArgument, "acquire_batch: request / constraints nil")
	}
	count := req.GetCount()
	if count == 0 {
		return nil, status.Error(codes.InvalidArgument, "acquire_batch: count must be > 0")
	}
	resp := &proxyv1alpha1.AcquireBatchResponse{
		Leases: make([]*proxyv1alpha1.Lease, 0, count),
	}
	for i := uint32(0); i < count; i++ {
		single, err := s.Acquire(ctx, req.GetConstraints())
		if err != nil {
			resp.Errors = append(resp.Errors, &proxyv1alpha1.BatchError{
				SlotIndex: i,
				Message:   err.Error(),
			})
			continue
		}
		resp.Leases = append(resp.Leases, single.GetLease())
	}
	return resp, nil
}
