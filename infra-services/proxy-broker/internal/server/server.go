// SPDX-License-Identifier: Apache-2.0

// Package server implements the gRPC Proxy server. The
// per-RPC handlers (acquire / release / report / budget) live
// in sibling files; this file holds the Server struct + helpers
// shared across handlers.
//
// Per ADR-0036 §5 normative shape: every RPC logs with the
// mandated ADR-0031 §3.4 fields (`request_id`, `job_id`,
// `tenant_id`, `service`, `service_version`, etc.). The Server
// struct holds the structured logger so handlers don't have to
// thread it.
package server

import (
	"context"
	"errors"
	"log/slog"

	proxyv1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/proxy/v1alpha1"

	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/providers"
	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/state"
)

// Server implements `proxy.ProxyServer` (the generated gRPC
// service interface). Holds the provider registry, state
// client, and structured logger that handlers share.
type Server struct {
	proxyv1alpha1.UnimplementedProxyServer

	// Providers is the registry mapping provider name → client.
	// The server's strategy is currently: pick the first
	// provider in `providerOrder`; future W5.x ordering
	// strategies (capacity-aware, cost-aware) plug in here.
	Providers     map[string]providers.Provider
	ProviderOrder []string

	State  *state.Client
	Logger *slog.Logger
}

// New constructs a Server. `providerOrder` is the priority
// list — the first provider in the list is preferred for
// `Acquire`; subsequent providers are fall-backs when the
// first cannot serve the request.
func New(providersMap map[string]providers.Provider, providerOrder []string, st *state.Client, logger *slog.Logger) (*Server, error) {
	if len(providersMap) == 0 {
		return nil, errors.New("server: at least one provider must be registered")
	}
	if len(providerOrder) == 0 {
		return nil, errors.New("server: providerOrder must be non-empty")
	}
	for _, name := range providerOrder {
		if _, ok := providersMap[name]; !ok {
			return nil, errors.New("server: providerOrder references unknown provider: " + name)
		}
	}
	if st == nil {
		return nil, errors.New("server: state client is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		Providers:     providersMap,
		ProviderOrder: providerOrder,
		State:         st,
		Logger:        logger,
	}, nil
}

// pickProvider returns the first provider in `providerOrder`
// for now. Future strategies layer on top — e.g., skip
// providers whose ban count for the target domain exceeds a
// threshold; round-robin across equal-priority tier; cost
// optimisation. Kept simple in W5.1; the registry shape
// supports the future evolutions.
func (s *Server) pickProvider(_ context.Context, _ providers.AcquireRequest) providers.Provider {
	name := s.ProviderOrder[0]
	return s.Providers[name]
}

// proxyTypeFromProto maps the wire-level enum to the
// provider-internal type. UNSPECIFIED defaults to
// RESIDENTIAL per the proto contract.
func proxyTypeFromProto(t proxyv1alpha1.ProxyType) providers.ProxyType {
	switch t {
	case proxyv1alpha1.ProxyType_PROXY_TYPE_RESIDENTIAL,
		proxyv1alpha1.ProxyType_PROXY_TYPE_UNSPECIFIED:
		return providers.ProxyTypeResidential
	case proxyv1alpha1.ProxyType_PROXY_TYPE_DATACENTER:
		return providers.ProxyTypeDatacenter
	case proxyv1alpha1.ProxyType_PROXY_TYPE_MOBILE:
		return providers.ProxyTypeMobile
	case proxyv1alpha1.ProxyType_PROXY_TYPE_ISP:
		return providers.ProxyTypeISP
	default:
		return providers.ProxyTypeResidential
	}
}
