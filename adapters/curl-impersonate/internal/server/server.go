// SPDX-License-Identifier: Apache-2.0

// Package server implements the v1alpha1 gRPC Driver service for
// the curl-impersonate adapter.
//
// PR11 ships Initialize and Navigate. The other four RPCs
// (Query, Extract, Screenshot, Close) are inherited from
// UnimplementedDriverServer and return codes.Unimplemented; PR12
// implements Close, Query, and Extract; the screenshot RPC will
// never be implemented for this adapter (ADR-0016 §5).
//
// Architectural decisions are recorded in ADR-0008 (handshake),
// ADR-0009 (Navigate / session lifecycle), ADR-0014 (cross-
// language conformance), and ADR-0016 (this driver's specifics).
package server

import (
	"context"
	"net/url"
	"runtime"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	caps "github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/capabilities"
	curlerrors "github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/errors"
	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/curlx"
	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/sessions"
	driverv1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/driver/v1alpha1"
)

// DefaultNavigateTimeout caps a Navigate RPC when the request
// supplies no explicit `timeout`. Mirrors the SeleniumBase
// adapter's 30s default (server.py DEFAULT_NAVIGATE_TIMEOUT_MS)
// so the engine sees consistent behaviour across drivers.
const DefaultNavigateTimeout = 30 * time.Second

// Fetcher is the curlx.Fetch shape exposed for tests so the
// gRPC server can be exercised without spawning a real curl
// subprocess.
type Fetcher func(ctx context.Context, opts curlx.Options) (*curlx.Response, error)

// Server implements driverv1alpha1.DriverServer. PR11 implements
// Initialize and Navigate; the other RPCs are forwarded to
// UnimplementedDriverServer's codes.Unimplemented responses.
type Server struct {
	driverv1alpha1.UnimplementedDriverServer

	sessions *sessions.Manager
	fetch    Fetcher
	variant  string
}

// New constructs a Server whose Navigate RPC will dispatch via
// the supplied Fetcher (curlx.Fetch in production; a fake in
// tests). variant is the curl-impersonate binary name as
// resolved at startup; the Server forwards it to every
// curlx.Options so SPECTRE_CURL_VARIANT is honoured.
func New(mgr *sessions.Manager, fetch Fetcher, variant string) *Server {
	if fetch == nil {
		fetch = curlx.Fetch
	}
	if variant == "" {
		variant = curlx.DefaultVariant
	}
	return &Server{
		sessions: mgr,
		fetch:    fetch,
		variant:  variant,
	}
}

// Initialize allocates a fresh session and returns the declared
// capabilities. The protocol_version field on the request is
// ignored in PR11 — the engine and the adapter are pinned to the
// same v1alpha1 path via codegen (ADR-0007). Strict version
// checking is a v1alpha2 candidate.
func (s *Server) Initialize(_ context.Context, _ *driverv1alpha1.InitializeRequest) (*driverv1alpha1.InitializeResponse, error) {
	session := s.sessions.Create()
	return &driverv1alpha1.InitializeResponse{
		SessionId: session.ID,
		Capabilities: &driverv1alpha1.Capabilities{
			Names:          caps.Names(),
			DriverVersion:  caps.DriverVersion,
			RuntimeVersion: runtimeVersion(s.variant),
		},
	}, nil
}

// Navigate validates the request, looks up the session, and
// invokes the curl subprocess. The WaitCondition field is
// accepted but has no observable effect for this adapter — see
// ADR-0016 §2 (honest no-op contract).
func (s *Server) Navigate(ctx context.Context, req *driverv1alpha1.NavigateRequest) (*driverv1alpha1.NavigateResponse, error) {
	if req.GetSessionId() == "" {
		return navigateError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			"session_id is required"), nil
	}
	session, err := s.sessions.Get(req.GetSessionId())
	if err != nil {
		return navigateError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			"unknown session_id "+quote(req.GetSessionId())+"; call Initialize first"), nil
	}
	if req.GetUrl() == "" {
		return navigateError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			"url is required"), nil
	}
	if !isValidNavigationURL(req.GetUrl()) {
		return navigateError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			"url must be an absolute http(s) URL, got "+quote(req.GetUrl())), nil
	}

	timeout := DefaultNavigateTimeout
	if d := req.GetTimeout(); d != nil {
		if proposed := d.AsDuration(); proposed > 0 {
			timeout = proposed
		}
	}

	start := time.Now()
	resp, fetchErr := s.fetch(ctx, curlx.Options{
		URL:           req.GetUrl(),
		CookieJarPath: session.CookieJarPath,
		Timeout:       timeout,
		Variant:       s.variant,
	})
	elapsed := time.Since(start)
	if fetchErr != nil {
		// Surface the structured DriverError mapped from the
		// curl exit code / stderr. The wall-clock elapsed is
		// reported on the failure so operator logs see the
		// time-to-failure even when the response is empty.
		var ce *curlx.CurlError
		if asErr := asCurlError(fetchErr, &ce); asErr {
			mapped := curlerrors.Map(ce.ExitCode, ce.Stderr)
			// Special case: the binary itself was not on PATH.
			// curlx surfaces this as a non-zero ExitCode with a
			// "file not found" wrapped error.
			if ce.ExitCode == -1 && (ce.Stderr == "" || strings.Contains(ce.Stderr, "executable file not found")) {
				mapped = curlerrors.MapBinaryMissing(s.variant)
			}
			return &driverv1alpha1.NavigateResponse{
				Elapsed: durationpb.New(elapsed),
				Error: &driverv1alpha1.DriverError{
					Code:    mapped.Code,
					Message: mapped.Message,
				},
			}, nil
		}
		return &driverv1alpha1.NavigateResponse{
			Elapsed: durationpb.New(elapsed),
			Error: &driverv1alpha1.DriverError{
				Code:    driverv1alpha1.DriverError_CODE_INTERNAL,
				Message: fetchErr.Error(),
			},
		}, nil
	}

	finalURL := resp.FinalURL
	if finalURL == "" {
		finalURL = req.GetUrl()
	}
	return &driverv1alpha1.NavigateResponse{
		FinalUrl:   finalURL,
		StatusCode: resp.StatusCode,
		Elapsed:    durationpb.New(elapsed),
	}, nil
}

// Inherit codes.Unimplemented for Query/Extract/Screenshot/Close
// from the embedded UnimplementedDriverServer; PR11 does not
// override them. Future PRs add the implementations here. The
// unit tests assert the codes.Unimplemented response shape so a
// future change that accidentally implements one of these RPCs
// without overriding the embedding fails loudly.

func navigateError(code driverv1alpha1.DriverError_Code, message string) *driverv1alpha1.NavigateResponse {
	return &driverv1alpha1.NavigateResponse{
		Error: &driverv1alpha1.DriverError{
			Code:    code,
			Message: message,
		},
	}
}

func isValidNavigationURL(s string) bool {
	parsed, err := url.Parse(s)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.Host != ""
}

func quote(s string) string { return "'" + s + "'" }

func runtimeVersion(variant string) string {
	return "curl-impersonate(" + variant + ") go@" + runtime.Version()
}

// asCurlError narrows fetchErr to *curlx.CurlError without
// importing the errors package (avoids a name collision with the
// internal/errors package the file already imports).
func asCurlError(err error, target **curlx.CurlError) bool {
	for cur := err; cur != nil; {
		if ce, ok := cur.(*curlx.CurlError); ok {
			*target = ce
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := cur.(unwrapper)
		if !ok {
			return false
		}
		cur = u.Unwrap()
	}
	return false
}
