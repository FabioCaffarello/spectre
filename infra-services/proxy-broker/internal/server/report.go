// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	proxyv1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/proxy/v1alpha1"

	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/state"
)

// ReportFailure implements `proxy.ProxyServer`. Updates
// cooldown + health-score state; for BANNED / CAPTCHA the
// failure is also recorded against the per-domain ban set so
// future `Acquire`s for the same domain deprioritise the
// proxy.
func (s *Server) ReportFailure(ctx context.Context, req *proxyv1alpha1.ReportFailureRequest) (*proxyv1alpha1.ReportFailureResponse, error) {
	if req == nil || req.GetLeaseId() == "" {
		return nil, status.Error(codes.InvalidArgument, "report_failure: lease_id is required")
	}
	info, found, err := s.State.LookupLease(ctx, req.GetLeaseId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "report_failure: lookup: %v", err)
	}
	if !found {
		// Expired lease — accept the report without failing
		// (the report still carries health signal even if the
		// lease record has aged out; record against the
		// proxy_id from the lease_id field as best-effort).
		s.Logger.DebugContext(ctx, "report_failure: lease not found; recording against lease_id directly",
			"lease_id", req.GetLeaseId())
		info = state.LeaseInfo{
			ProxyID:      req.GetLeaseId(),
			Provider:     "",
			TenantID:     req.GetTenantId(),
			TargetDomain: req.GetTargetDomain(),
		}
	}
	// Recorded `target_domain` from the request overrides the
	// lease record (the consumer may report a failure against
	// a different domain than the lease was originally tagged
	// with — e.g. lease used for batch crawl across multiple
	// domains).
	if req.GetTargetDomain() != "" {
		info.TargetDomain = req.GetTargetDomain()
	}

	kindStr := failureKindString(req.GetKind())

	// Always decay health score on any failure.
	if err := s.State.RecordFailure(ctx, info.ProxyID); err != nil {
		s.Logger.ErrorContext(ctx, "report_failure: RecordFailure",
			"lease_id", req.GetLeaseId(),
			"error", err.Error())
		// Continue — cooldown is the more important signal.
	}

	// Set per-proxy cooldown.
	cooldown := state.CooldownDuration(kindStr)
	if err := s.State.SetCooldown(ctx, info.ProxyID, kindStr, cooldown); err != nil {
		s.Logger.ErrorContext(ctx, "report_failure: SetCooldown",
			"lease_id", req.GetLeaseId(),
			"error", err.Error())
		return nil, status.Errorf(codes.Internal, "report_failure: cooldown: %v", err)
	}

	// BANNED / CAPTCHA also update the per-domain ban set
	// (per-domain coordination — same proxy may be usable
	// for other targets).
	if req.GetKind() == proxyv1alpha1.FailureKind_FAILURE_KIND_BANNED ||
		req.GetKind() == proxyv1alpha1.FailureKind_FAILURE_KIND_CAPTCHA {
		if info.Provider != "" && info.TargetDomain != "" {
			if err := s.State.RecordBan(ctx, info.Provider, info.TargetDomain, info.ProxyID, time.Now()); err != nil {
				s.Logger.ErrorContext(ctx, "report_failure: RecordBan",
					"lease_id", req.GetLeaseId(),
					"provider", info.Provider,
					"domain", info.TargetDomain,
					"error", err.Error())
				// Don't fail the RPC — cooldown already set.
			}
		}
	}

	s.Logger.InfoContext(ctx, "report_failure: recorded",
		"lease_id", req.GetLeaseId(),
		"provider", info.Provider,
		"target_domain", info.TargetDomain,
		"tenant_id", req.GetTenantId(),
		"kind", kindStr,
		"cooldown", cooldown.String())

	return &proxyv1alpha1.ReportFailureResponse{}, nil
}

// failureKindString maps the wire-level enum to the lowercase
// names `state.CooldownDuration` expects + that surface in
// logs / `redis-cli` for observability.
func failureKindString(k proxyv1alpha1.FailureKind) string {
	switch k {
	case proxyv1alpha1.FailureKind_FAILURE_KIND_BANNED:
		return "banned"
	case proxyv1alpha1.FailureKind_FAILURE_KIND_TIMEOUT:
		return "timeout"
	case proxyv1alpha1.FailureKind_FAILURE_KIND_BAD_RESPONSE:
		return "bad_response"
	case proxyv1alpha1.FailureKind_FAILURE_KIND_CAPTCHA:
		return "captcha"
	default:
		return strings.ToLower(k.String())
	}
}
