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
	"encoding/json"
	"fmt"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	caps "github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/capabilities"
	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/curlx"
	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/elements"
	curlerrors "github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/errors"
	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/parser"
	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/sessions"
	driverv1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/driver/v1alpha1"
)

// R4.3 / ADR-0023 §5: every non-Initialize RPC validates the
// session against Redis before doing any work.
// ``ValidationDifferentInstance`` and Redis-unreachable surface
// as transport-level gRPC ``codes.Unavailable``; the conformance
// test in ``tools/conformance/tests/test_session_restart_invalidation.py``
// asserts on ``grpc.StatusCode.UNAVAILABLE`` precisely against
// this code path. ``ValidationUnknown`` continues to return the
// in-band ``CODE_INVALID_ARGUMENT`` envelope so the behaviour on
// a never-Initialized id is unchanged from PR12.
const (
	differentInstanceMessage = "session belongs to a different adapter instance; client must re-Initialize"
	redisUnavailablePrefix   = "redis unreachable"
)

// gateSession validates ``id`` against Redis. Returns the
// :class:`sessions.Validation` for the caller to inspect (it
// will be ``OK`` or ``Unknown``) or a non-nil gRPC ``status``
// error to propagate (``codes.Unavailable`` for
// ``DifferentInstance`` or Redis errors).
func (s *Server) gateSession(ctx context.Context, id string) (sessions.Validation, error) {
	v, err := s.sessions.Validate(ctx, id)
	if err != nil {
		return sessions.Validation{}, status.Errorf(codes.Unavailable, "%s: %v", redisUnavailablePrefix, err)
	}
	if v.Kind == sessions.ValidationDifferentInstance {
		return sessions.Validation{}, status.Error(codes.Unavailable, differentInstanceMessage)
	}
	return v, nil
}

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
// capabilities. ADR-0023 §6 makes Redis required: if the
// metadata write fails the RPC fails at the transport layer with
// ``codes.Unavailable`` so the caller sees the same gRPC status
// it would see if the adapter could not start. The local
// registry is only updated after a successful Redis write — see
// ``sessions.Manager.Create``.
func (s *Server) Initialize(ctx context.Context, _ *driverv1alpha1.InitializeRequest) (*driverv1alpha1.InitializeResponse, error) {
	session, err := s.sessions.Create(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "%s; cannot persist session metadata: %v", redisUnavailablePrefix, err)
	}
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
	gate, gateErr := s.gateSession(ctx, req.GetSessionId())
	if gateErr != nil {
		return nil, gateErr
	}
	if gate.Kind == sessions.ValidationUnknown {
		return navigateError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			"unknown session_id "+quote(req.GetSessionId())+"; call Initialize first"), nil
	}
	session, err := s.sessions.Get(req.GetSessionId())
	if err != nil {
		// Redis says the session is for this instance but the
		// local registry has lost it — treat as a foreign-
		// instance case (the manager's local state has been torn
		// down out from under Redis).
		return nil, status.Error(codes.Unavailable, differentInstanceMessage)
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

	// Parse the body and cache it on the session. SetDocument bumps
	// the generation counter atomically so any prior ElementRefs
	// allocated against the previous Navigate are invalidated. The
	// underlying x/net/html parser is permissive and absorbs
	// malformed HTML the same way browsers do — important for
	// cross-driver equivalence (ADR-0017 §2). A parse failure is
	// surfaced as CODE_INTERNAL because the body is effectively
	// unconsumable for downstream Query/Extract; in practice the
	// permissive parser does not fail on real HTTP responses.
	doc, parseErr := parser.Parse(resp.Body)
	if parseErr != nil {
		return &driverv1alpha1.NavigateResponse{
			FinalUrl:   finalURL,
			StatusCode: resp.StatusCode,
			Elapsed:    durationpb.New(elapsed),
			Error: &driverv1alpha1.DriverError{
				Code:    driverv1alpha1.DriverError_CODE_INTERNAL,
				Message: "parse response body: " + parseErr.Error(),
			},
		}, nil
	}
	if err := s.sessions.SetDocument(req.GetSessionId(), doc); err != nil {
		// SetDocument can only fail with ErrUnknownSession, which
		// the strict-id check above already excluded. A failure
		// here means the session was concurrently closed; report
		// it honestly rather than mask.
		return &driverv1alpha1.NavigateResponse{
			Elapsed: durationpb.New(elapsed),
			Error: &driverv1alpha1.DriverError{
				Code:    driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
				Message: "session was closed during Navigate: " + err.Error(),
			},
		}, nil
	}

	return &driverv1alpha1.NavigateResponse{
		FinalUrl:   finalURL,
		StatusCode: resp.StatusCode,
		Elapsed:    durationpb.New(elapsed),
	}, nil
}

// Close is the full session-teardown RPC. PR11 shipped a thin
// implementation so the engine's executor could finish navigate-
// only plans; PR12 promotes it to the full contract:
//
//   - Strict session_id validation (empty → CODE_INVALID_ARGUMENT).
//   - Idempotent rejection of unknown / already-closed ids
//     (second Close on the same id → CODE_INVALID_ARGUMENT).
//   - Cookie-jar file deletion (sessions.Manager.Close).
//   - ElementRegistry teardown for the session (sessions.Manager
//     forgets the registry entry before deleting the jar).
//
// ADR-0010 §1 / ADR-0017 §3 record the lifecycle contract.
func (s *Server) Close(ctx context.Context, req *driverv1alpha1.CloseRequest) (*driverv1alpha1.CloseResponse, error) {
	if req.GetSessionId() == "" {
		return &driverv1alpha1.CloseResponse{
			Error: &driverv1alpha1.DriverError{
				Code:    driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
				Message: "session_id is required",
			},
		}, nil
	}
	gate, gateErr := s.gateSession(ctx, req.GetSessionId())
	if gateErr != nil {
		return nil, gateErr
	}
	if gate.Kind == sessions.ValidationUnknown {
		return &driverv1alpha1.CloseResponse{
			Error: &driverv1alpha1.DriverError{
				Code:    driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
				Message: "unknown session_id " + quote(req.GetSessionId()),
			},
		}, nil
	}
	// Best-effort delete inside Manager.Close — Redis blips during
	// teardown are logged but do not fail the RPC (§4.6).
	if err := s.sessions.Close(ctx, req.GetSessionId()); err != nil {
		return &driverv1alpha1.CloseResponse{
			Error: &driverv1alpha1.DriverError{
				Code:    driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
				Message: "unknown session_id " + quote(req.GetSessionId()),
			},
		}, nil
	}
	return &driverv1alpha1.CloseResponse{}, nil
}

// Query resolves a selector against the session's cached
// document and returns one ElementRef per match. PR12 supports
// CSS and XPATH only; TEXT and ATTRIBUTE are rejected because
// the curl-impersonate adapter does not declare query_text /
// query_attribute (ADR-0017 §1: capability declaration is a
// cross-driver semantic-equivalence contract, not a feasibility
// decision). Zero matches is success with an empty list, not
// CODE_NOT_FOUND (ADR-0010 §4).
func (s *Server) Query(ctx context.Context, req *driverv1alpha1.QueryRequest) (*driverv1alpha1.QueryResponse, error) {
	if req.GetSessionId() == "" {
		return queryError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			"session_id is required"), nil
	}
	gate, gateErr := s.gateSession(ctx, req.GetSessionId())
	if gateErr != nil {
		return nil, gateErr
	}
	if gate.Kind == sessions.ValidationUnknown {
		return queryError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			"unknown session_id "+quote(req.GetSessionId())+"; call Initialize first"), nil
	}
	if req.GetSelector() == "" {
		return queryError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			"selector is required"), nil
	}

	switch req.GetKind() {
	case driverv1alpha1.SelectorKind_SELECTOR_KIND_UNSPECIFIED:
		return queryError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			"selector kind is required; SELECTOR_KIND_UNSPECIFIED is not accepted"), nil
	case driverv1alpha1.SelectorKind_SELECTOR_KIND_TEXT:
		return queryError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			"this adapter does not declare query_text; see ADR-0017 §1"), nil
	case driverv1alpha1.SelectorKind_SELECTOR_KIND_ATTRIBUTE:
		return queryError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			"this adapter does not declare query_attribute; see ADR-0017 §1"), nil
	case driverv1alpha1.SelectorKind_SELECTOR_KIND_CSS,
		driverv1alpha1.SelectorKind_SELECTOR_KIND_XPATH:
		// fall through
	default:
		return queryError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			"unsupported selector kind"), nil
	}

	doc := s.sessions.Document(req.GetSessionId())
	if doc == nil {
		return queryError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			"no page is open for this session; call Navigate first"), nil
	}

	var matches *goquery.Selection
	switch req.GetKind() {
	case driverv1alpha1.SelectorKind_SELECTOR_KIND_CSS:
		matches = doc.Find(req.GetSelector())
	case driverv1alpha1.SelectorKind_SELECTOR_KIND_XPATH:
		sel, err := parser.XPathQuery(doc, req.GetSelector())
		if err != nil {
			return queryError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
				err.Error()), nil
		}
		matches = sel
	}

	if limit := req.GetLimit(); limit > 0 && uint32(matches.Length()) > limit {
		matches = matches.Slice(0, int(limit))
	}

	ids := s.sessions.Allocate(req.GetSessionId(), matches)
	out := make([]*driverv1alpha1.ElementRef, 0, len(ids))
	for _, id := range ids {
		out = append(out, &driverv1alpha1.ElementRef{OpaqueId: id})
	}
	return &driverv1alpha1.QueryResponse{Elements: out}, nil
}

// Stale / unknown element-ref messages are reproduced verbatim
// from ADR-0010 §1. The Playwright (TypeScript) and SeleniumBase
// (Python) adapters use the same strings; tools that key on the
// message are cross-driver-stable.
const (
	staleNavigateMessage = "element reference is stale; query was performed before a navigation"
	unknownRefMessage    = "element reference is unknown"
	modeEvalGateMessage  = "MODE_EVAL requires the js_execution capability; this adapter does not declare it"
)

// Extract reads the requested Field values off the element
// referenced by element.opaque_id in the session's registry.
//
// Behaviour summary (ADR-0017 §5):
//
//   - MODE_EVAL fields short-circuit the entire request with
//     CODE_CAPABILITY_MISSING (atomic fail-the-whole-request
//     semantics from ADR-0010 §3). curl-impersonate has no
//     JavaScript engine; this is the conformance suite's first
//     test of the runtime capability gate's negative path.
//   - MODE_UNSPECIFIED rejects with CODE_INVALID_ARGUMENT.
//   - Stale / unknown ElementRefs reject with
//     CODE_INVALID_ARGUMENT and the documented messages.
//   - MODE_INNER_TEXT falls back to selection.Text() — the same
//     output as MODE_TEXT_CONTENT — because computing rendered
//     visibility requires a layout engine. Documented
//     approximation; see ADR-0017 §5.
func (s *Server) Extract(ctx context.Context, req *driverv1alpha1.ExtractRequest) (*driverv1alpha1.ExtractResponse, error) {
	if req.GetSessionId() == "" {
		return extractError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			"session_id is required"), nil
	}
	gate, gateErr := s.gateSession(ctx, req.GetSessionId())
	if gateErr != nil {
		return nil, gateErr
	}
	if gate.Kind == sessions.ValidationUnknown {
		return extractError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			"unknown session_id "+quote(req.GetSessionId())+"; call Initialize first"), nil
	}
	opaqueID := ""
	if el := req.GetElement(); el != nil {
		opaqueID = el.GetOpaqueId()
	}
	if opaqueID == "" {
		return extractError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			"element.opaque_id is required"), nil
	}
	fields := req.GetFields()
	if len(fields) == 0 {
		return extractError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			"at least one field is required"), nil
	}

	// MODE_EVAL gate — runs before any field is evaluated. If any
	// field requests MODE_EVAL, fail the whole request with
	// CODE_CAPABILITY_MISSING. ADR-0010 §3, ADR-0017 §1 / §5.
	for _, f := range fields {
		if f.GetMode() == driverv1alpha1.Field_MODE_EVAL {
			return extractError(driverv1alpha1.DriverError_CODE_CAPABILITY_MISSING,
				modeEvalGateMessage), nil
		}
	}
	// Reject any unspecified mode up-front so the field-reading
	// loop only ever sees real modes.
	for _, f := range fields {
		if f.GetMode() == driverv1alpha1.Field_MODE_UNSPECIFIED {
			return extractError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
				"field "+quote(f.GetName())+" has an unspecified mode"), nil
		}
	}

	lookup := s.sessions.LookupElement(req.GetSessionId(), opaqueID)
	switch lookup.Status {
	case elements.StatusStale:
		return extractError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			staleNavigateMessage), nil
	case elements.StatusUnknown:
		return extractError(driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT,
			unknownRefMessage), nil
	case elements.StatusOK:
		// fall through
	}

	entries := make([]*driverv1alpha1.ExtractedValues_Entry, 0, len(fields))
	for _, f := range fields {
		value, err := readFieldValue(lookup.Selection, f.GetMode(), f.GetArg())
		if err != nil {
			return extractError(driverv1alpha1.DriverError_CODE_INTERNAL,
				"read field "+quote(f.GetName())+": "+err.Error()), nil
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return extractError(driverv1alpha1.DriverError_CODE_INTERNAL,
				"json encode field "+quote(f.GetName())+": "+err.Error()), nil
		}
		entries = append(entries, &driverv1alpha1.ExtractedValues_Entry{
			Name:      f.GetName(),
			JsonValue: string(encoded),
		})
	}
	return &driverv1alpha1.ExtractResponse{
		Values: &driverv1alpha1.ExtractedValues{Fields: entries},
	}, nil
}

// readFieldValue reads one Field's value off a *goquery.Selection.
// Returns an `any` so the JSON encoder produces null for absent
// attributes (preserving the "absent vs present-but-empty"
// distinction from ADR-0017 §5).
func readFieldValue(sel *goquery.Selection, mode driverv1alpha1.Field_Mode, arg string) (any, error) {
	switch mode {
	case driverv1alpha1.Field_MODE_TEXT_CONTENT:
		return sel.Text(), nil
	case driverv1alpha1.Field_MODE_INNER_TEXT:
		// Static-HTML approximation: no layout engine, no
		// visibility filtering. Same output as TEXT_CONTENT.
		// ADR-0017 §5 records the trade-off and the v1alpha2
		// growth path (a separate capability that adapters with
		// renderers can declare).
		return sel.Text(), nil
	case driverv1alpha1.Field_MODE_INNER_HTML:
		return sel.Html()
	case driverv1alpha1.Field_MODE_OUTER_HTML:
		return parser.OuterHtml(sel)
	case driverv1alpha1.Field_MODE_ATTR:
		value, exists := sel.Attr(arg)
		if !exists {
			return nil, nil
		}
		return value, nil
	}
	return nil, fmt.Errorf("unsupported field mode %v", mode)
}

func extractError(code driverv1alpha1.DriverError_Code, message string) *driverv1alpha1.ExtractResponse {
	return &driverv1alpha1.ExtractResponse{
		Error: &driverv1alpha1.DriverError{
			Code:    code,
			Message: message,
		},
	}
}

func queryError(code driverv1alpha1.DriverError_Code, message string) *driverv1alpha1.QueryResponse {
	return &driverv1alpha1.QueryResponse{
		Error: &driverv1alpha1.DriverError{
			Code:    code,
			Message: message,
		},
	}
}

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
