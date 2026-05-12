// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	proxyv1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/proxy/v1alpha1"
)

// BudgetStatus implements `proxy.ProxyServer`. W5.1 returns
// synthetic placeholder values — real per-provider billing
// API integration (BrightData's `zone-bw`, etc.) is W5.1b.
// The scope-validation logic is real and stays as-is when
// W5.1b wires the upstream billing calls.
func (s *Server) BudgetStatus(_ context.Context, req *proxyv1alpha1.BudgetStatusRequest) (*proxyv1alpha1.BudgetStatusResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "budget_status: request is nil")
	}
	// Exactly one scope field must be set.
	setCount := 0
	if req.GetProvider() != "" {
		setCount++
	}
	if req.GetRegion() != "" {
		setCount++
	}
	if req.GetTenantId() != "" {
		setCount++
	}
	if setCount != 1 {
		return nil, status.Errorf(codes.InvalidArgument,
			"budget_status: exactly one of provider / region / tenant_id must be set (got %d)",
			setCount)
	}
	// W5.1 synthetic shape — limit unknown (provider doesn't
	// expose), available reported as max uint64 so callers'
	// "do I have headroom?" checks pass during local-dev.
	// W5.1b replaces with real billing-API integration; the
	// scope validation above remains.
	return &proxyv1alpha1.BudgetStatusResponse{
		Spent:     0,
		Reserved:  0,
		Available: ^uint64(0),
		Limit:     0,
	}, nil
}
