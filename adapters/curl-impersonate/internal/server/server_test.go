// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/curlx"
	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/sessions"
	driverv1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/driver/v1alpha1"
)

const queryFixtureHTML = `<!doctype html><html><body>
<h1 id="title">Elements Page</h1>
<ul id="items">
<li class="item">first</li>
<li class="item">second</li>
<li class="item">third</li>
</ul>
<a id="link" href="https://example.com/target">visit</a>
<div id="badge" data-test="primary">Primary</div>
</body></html>`

func newServerWithFetcher(t *testing.T, fetcher Fetcher) (*Server, *sessions.Manager) {
	t.Helper()
	mgr := newTestManager(t)
	return New(mgr, fetcher, "curl_chrome116"), mgr
}

func newTestManager(t *testing.T) *sessions.Manager {
	t.Helper()
	// Reuse the manager via its public constructor; the directory
	// the manager uses is /tmp by default but the gRPC tests do
	// not write any cookie-jar files (the fake Fetcher never
	// touches disk). Cleanup after the test still calls CloseAll.
	mgr := sessions.NewManager()
	t.Cleanup(mgr.CloseAll)
	return mgr
}

func TestInitializeReturnsSessionAndCapabilities(t *testing.T) {
	srv, _ := newServerWithFetcher(t, fakeOK("https://example.com"))
	resp, err := srv.Initialize(context.Background(), &driverv1alpha1.InitializeRequest{})
	if err != nil {
		t.Fatalf("Initialize err: %v", err)
	}
	if resp.GetSessionId() == "" {
		t.Fatal("Initialize must return a session_id")
	}
	if resp.GetError() != nil {
		t.Fatalf("Initialize must not populate error: %v", resp.GetError())
	}
	caps := resp.GetCapabilities()
	if caps == nil {
		t.Fatal("Initialize must populate capabilities envelope")
	}
	if got := caps.GetNames(); len(got) != 1 || got[0] != "navigation" {
		t.Fatalf("PR11 capability list must be ['navigation']; got %v", got)
	}
	if caps.GetDriverVersion() == "" {
		t.Fatal("driver_version must be populated")
	}
	if !strings.Contains(caps.GetRuntimeVersion(), "curl-impersonate") {
		t.Fatalf("runtime_version should identify curl-impersonate; got %q", caps.GetRuntimeVersion())
	}
}

func TestNavigateRejectsMissingSessionID(t *testing.T) {
	srv, _ := newServerWithFetcher(t, mustNotCall(t))
	resp, err := srv.Navigate(context.Background(), &driverv1alpha1.NavigateRequest{
		Url: "https://example.com",
	})
	if err != nil {
		t.Fatalf("Navigate err: %v", err)
	}
	mustErrCode(t, resp.GetError(), driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT, "session_id is required")
}

func TestNavigateRejectsUnknownSessionID(t *testing.T) {
	srv, _ := newServerWithFetcher(t, mustNotCall(t))
	resp, err := srv.Navigate(context.Background(), &driverv1alpha1.NavigateRequest{
		SessionId: "never-initialized",
		Url:       "https://example.com",
	})
	if err != nil {
		t.Fatalf("Navigate err: %v", err)
	}
	mustErrCode(t, resp.GetError(), driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT, "unknown session_id")
}

func TestNavigateRejectsInvalidURL(t *testing.T) {
	srv, mgr := newServerWithFetcher(t, mustNotCall(t))
	session := mgr.Create()
	cases := []struct {
		name string
		url  string
	}{
		{"empty", ""},
		{"relative", "/foo"},
		{"non_http", "ftp://example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := srv.Navigate(context.Background(), &driverv1alpha1.NavigateRequest{
				SessionId: session.ID,
				Url:       tc.url,
			})
			if err != nil {
				t.Fatalf("Navigate err: %v", err)
			}
			if resp.GetError() == nil ||
				resp.GetError().GetCode() != driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT {
				t.Fatalf("expected INVALID_ARGUMENT, got %v", resp.GetError())
			}
		})
	}
}

func TestNavigateHappyPathPopulatesResponse(t *testing.T) {
	called := false
	fetcher := func(_ context.Context, opts curlx.Options) (*curlx.Response, error) {
		called = true
		if opts.URL != "https://example.com" {
			t.Fatalf("expected URL forwarded; got %q", opts.URL)
		}
		if opts.Variant != "curl_chrome116" {
			t.Fatalf("expected variant forwarded; got %q", opts.Variant)
		}
		if !strings.Contains(opts.CookieJarPath, "spectre-curl-") {
			t.Fatalf("expected session cookie jar; got %q", opts.CookieJarPath)
		}
		return &curlx.Response{
			StatusCode: 200,
			FinalURL:   "https://example.com/",
			Body:       []byte("ok"),
			Elapsed:    20 * time.Millisecond,
		}, nil
	}
	srv, mgr := newServerWithFetcher(t, fetcher)
	session := mgr.Create()
	resp, err := srv.Navigate(context.Background(), &driverv1alpha1.NavigateRequest{
		SessionId: session.ID,
		Url:       "https://example.com",
	})
	if err != nil {
		t.Fatalf("Navigate err: %v", err)
	}
	if !called {
		t.Fatal("fetcher must be invoked")
	}
	if resp.GetError() != nil {
		t.Fatalf("expected no error; got %v", resp.GetError())
	}
	if resp.GetStatusCode() != 200 {
		t.Fatalf("status: got %d", resp.GetStatusCode())
	}
	if resp.GetFinalUrl() != "https://example.com/" {
		t.Fatalf("final url: got %q", resp.GetFinalUrl())
	}
	if resp.GetElapsed() == nil {
		t.Fatal("elapsed must be populated")
	}
}

func TestNavigateAcceptsAllWaitConditions(t *testing.T) {
	// ADR-0016 §2: WaitCondition is honest no-op for this adapter.
	// The handler must not reject any of the four enum values.
	cases := []driverv1alpha1.WaitCondition{
		driverv1alpha1.WaitCondition_WAIT_CONDITION_UNSPECIFIED,
		driverv1alpha1.WaitCondition_WAIT_CONDITION_LOAD,
		driverv1alpha1.WaitCondition_WAIT_CONDITION_DOM_CONTENT_LOADED,
		driverv1alpha1.WaitCondition_WAIT_CONDITION_NETWORK_IDLE,
	}
	srv, mgr := newServerWithFetcher(t, fakeOK("https://example.com/"))
	session := mgr.Create()
	for _, wait := range cases {
		t.Run(wait.String(), func(t *testing.T) {
			resp, err := srv.Navigate(context.Background(), &driverv1alpha1.NavigateRequest{
				SessionId: session.ID,
				Url:       "https://example.com",
				Wait:      wait,
			})
			if err != nil {
				t.Fatalf("Navigate err: %v", err)
			}
			if resp.GetError() != nil {
				t.Fatalf("WaitCondition %v must be no-op; got error %v", wait, resp.GetError())
			}
		})
	}
}

func TestNavigateMapsCurlError(t *testing.T) {
	fetcher := func(_ context.Context, _ curlx.Options) (*curlx.Response, error) {
		return nil, &curlx.CurlError{
			ExitCode: 6,
			Stderr:   "curl: (6) Could not resolve host: nope.invalid",
			Variant:  "curl_chrome116",
			Err:      errors.New("exit status 6"),
		}
	}
	srv, mgr := newServerWithFetcher(t, fetcher)
	session := mgr.Create()
	resp, err := srv.Navigate(context.Background(), &driverv1alpha1.NavigateRequest{
		SessionId: session.ID,
		Url:       "https://nope.invalid",
	})
	if err != nil {
		t.Fatalf("Navigate err: %v", err)
	}
	if resp.GetError() == nil {
		t.Fatal("expected DriverError for curl exit 6")
	}
	if resp.GetError().GetCode() != driverv1alpha1.DriverError_CODE_TARGET_UNREACHABLE {
		t.Fatalf("expected TARGET_UNREACHABLE; got %v", resp.GetError().GetCode())
	}
	if resp.GetElapsed() == nil {
		t.Fatal("elapsed must be reported even on failure")
	}
}

func TestNavigateUsesRequestTimeoutWhenProvided(t *testing.T) {
	var captured time.Duration
	fetcher := func(_ context.Context, opts curlx.Options) (*curlx.Response, error) {
		captured = opts.Timeout
		return &curlx.Response{StatusCode: 200, FinalURL: "https://example.com/"}, nil
	}
	srv, mgr := newServerWithFetcher(t, fetcher)
	session := mgr.Create()
	_, err := srv.Navigate(context.Background(), &driverv1alpha1.NavigateRequest{
		SessionId: session.ID,
		Url:       "https://example.com",
		Timeout:   durationpb.New(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("Navigate err: %v", err)
	}
	if captured != 2*time.Second {
		t.Fatalf("expected 2s timeout to flow into curlx.Options; got %v", captured)
	}
}

func navigateThenInit(t *testing.T, srv *Server, mgr *sessions.Manager) string {
	t.Helper()
	session := mgr.Create()
	_, err := srv.Navigate(context.Background(), &driverv1alpha1.NavigateRequest{
		SessionId: session.ID,
		Url:       "https://example.com",
	})
	if err != nil {
		t.Fatalf("Navigate err: %v", err)
	}
	return session.ID
}

func newServerWithFixture(t *testing.T) (*Server, *sessions.Manager, string) {
	t.Helper()
	fetcher := func(_ context.Context, _ curlx.Options) (*curlx.Response, error) {
		return &curlx.Response{
			StatusCode: 200,
			FinalURL:   "https://example.com/",
			Body:       []byte(queryFixtureHTML),
		}, nil
	}
	srv, mgr := newServerWithFetcher(t, fetcher)
	id := navigateThenInit(t, srv, mgr)
	return srv, mgr, id
}

func TestQueryRejectsMissingSessionID(t *testing.T) {
	srv, _ := newServerWithFetcher(t, mustNotCall(t))
	resp, err := srv.Query(context.Background(), &driverv1alpha1.QueryRequest{
		Selector: "h1",
		Kind:     driverv1alpha1.SelectorKind_SELECTOR_KIND_CSS,
	})
	if err != nil {
		t.Fatalf("Query err: %v", err)
	}
	mustErrCode(t, resp.GetError(), driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT, "session_id is required")
}

func TestQueryRejectsUnknownSession(t *testing.T) {
	srv, _ := newServerWithFetcher(t, mustNotCall(t))
	resp, err := srv.Query(context.Background(), &driverv1alpha1.QueryRequest{
		SessionId: "never-initialized",
		Selector:  "h1",
		Kind:      driverv1alpha1.SelectorKind_SELECTOR_KIND_CSS,
	})
	if err != nil {
		t.Fatalf("Query err: %v", err)
	}
	mustErrCode(t, resp.GetError(), driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT, "unknown session_id")
}

func TestQueryRejectsTextSelectorKindWithADRReference(t *testing.T) {
	// ADR-0017 §1: query_text is not declared because the cross-
	// driver semantic contract diverges. Operators hitting the
	// rejection should see the ADR reference in the message.
	srv, _, id := newServerWithFixture(t)
	resp, err := srv.Query(context.Background(), &driverv1alpha1.QueryRequest{
		SessionId: id,
		Selector:  "Primary",
		Kind:      driverv1alpha1.SelectorKind_SELECTOR_KIND_TEXT,
	})
	if err != nil {
		t.Fatalf("Query err: %v", err)
	}
	mustErrCode(t, resp.GetError(), driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT, "query_text")
	if !strings.Contains(resp.GetError().GetMessage(), "ADR-0017") {
		t.Fatalf("rejection message should reference ADR-0017; got %q", resp.GetError().GetMessage())
	}
}

func TestQueryRejectsAttributeSelectorKindWithADRReference(t *testing.T) {
	srv, _, id := newServerWithFixture(t)
	resp, err := srv.Query(context.Background(), &driverv1alpha1.QueryRequest{
		SessionId: id,
		Selector:  "data-test=primary",
		Kind:      driverv1alpha1.SelectorKind_SELECTOR_KIND_ATTRIBUTE,
	})
	if err != nil {
		t.Fatalf("Query err: %v", err)
	}
	mustErrCode(t, resp.GetError(), driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT, "query_attribute")
	if !strings.Contains(resp.GetError().GetMessage(), "ADR-0017") {
		t.Fatalf("rejection message should reference ADR-0017; got %q", resp.GetError().GetMessage())
	}
}

func TestQueryRejectsUnspecifiedKind(t *testing.T) {
	srv, _, id := newServerWithFixture(t)
	resp, err := srv.Query(context.Background(), &driverv1alpha1.QueryRequest{
		SessionId: id,
		Selector:  "h1",
		// SELECTOR_KIND_UNSPECIFIED is the proto3 default.
	})
	if err != nil {
		t.Fatalf("Query err: %v", err)
	}
	mustErrCode(t, resp.GetError(), driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT, "UNSPECIFIED")
}

func TestQueryRejectsBeforeNavigate(t *testing.T) {
	srv, mgr := newServerWithFetcher(t, mustNotCall(t))
	session := mgr.Create()
	resp, err := srv.Query(context.Background(), &driverv1alpha1.QueryRequest{
		SessionId: session.ID,
		Selector:  "h1",
		Kind:      driverv1alpha1.SelectorKind_SELECTOR_KIND_CSS,
	})
	if err != nil {
		t.Fatalf("Query err: %v", err)
	}
	mustErrCode(t, resp.GetError(), driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT, "no page is open")
}

func TestQueryCSSReturnsMatches(t *testing.T) {
	srv, _, id := newServerWithFixture(t)
	resp, err := srv.Query(context.Background(), &driverv1alpha1.QueryRequest{
		SessionId: id,
		Selector:  "li.item",
		Kind:      driverv1alpha1.SelectorKind_SELECTOR_KIND_CSS,
	})
	if err != nil {
		t.Fatalf("Query err: %v", err)
	}
	if resp.GetError() != nil {
		t.Fatalf("unexpected error: %v", resp.GetError())
	}
	if got := len(resp.GetElements()); got != 3 {
		t.Fatalf("expected 3 li.item, got %d", got)
	}
	for _, el := range resp.GetElements() {
		if el.GetOpaqueId() == "" {
			t.Fatal("every ElementRef must carry an opaque_id")
		}
	}
}

func TestQueryXPathReturnsMatches(t *testing.T) {
	srv, _, id := newServerWithFixture(t)
	resp, err := srv.Query(context.Background(), &driverv1alpha1.QueryRequest{
		SessionId: id,
		Selector:  "//li[@class='item']",
		Kind:      driverv1alpha1.SelectorKind_SELECTOR_KIND_XPATH,
	})
	if err != nil {
		t.Fatalf("Query err: %v", err)
	}
	if got := len(resp.GetElements()); got != 3 {
		t.Fatalf("expected 3 matches via XPath, got %d", got)
	}
}

func TestQueryZeroMatchesIsSuccess(t *testing.T) {
	srv, _, id := newServerWithFixture(t)
	resp, err := srv.Query(context.Background(), &driverv1alpha1.QueryRequest{
		SessionId: id,
		Selector:  ".no-such-class",
		Kind:      driverv1alpha1.SelectorKind_SELECTOR_KIND_CSS,
	})
	if err != nil {
		t.Fatalf("Query err: %v", err)
	}
	if resp.GetError() != nil {
		t.Fatalf("zero matches must not produce a DriverError; got %v", resp.GetError())
	}
	if got := len(resp.GetElements()); got != 0 {
		t.Fatalf("expected zero elements, got %d", got)
	}
}

func TestQueryHonoursLimit(t *testing.T) {
	srv, _, id := newServerWithFixture(t)
	resp, err := srv.Query(context.Background(), &driverv1alpha1.QueryRequest{
		SessionId: id,
		Selector:  "li.item",
		Kind:      driverv1alpha1.SelectorKind_SELECTOR_KIND_CSS,
		Limit:     2,
	})
	if err != nil {
		t.Fatalf("Query err: %v", err)
	}
	if got := len(resp.GetElements()); got != 2 {
		t.Fatalf("limit=2 must cap matches; got %d", got)
	}
}

func TestUnimplementedRPCsReturnUnimplemented(t *testing.T) {
	// PR12 implements Query and Extract; Screenshot stays
	// permanently Unimplemented for this adapter (ADR-0016 §5,
	// ADR-0017 §1) — the underlying runtime has no rendering
	// pipeline.
	srv, mgr := newServerWithFetcher(t, mustNotCall(t))
	session := mgr.Create()

	_, err := srv.Screenshot(context.Background(), &driverv1alpha1.ScreenshotRequest{SessionId: session.ID})
	mustGRPCCode(t, err, codes.Unimplemented)
}

func TestCloseRejectsMissingAndUnknownIDs(t *testing.T) {
	srv, _ := newServerWithFetcher(t, mustNotCall(t))

	resp, err := srv.Close(context.Background(), &driverv1alpha1.CloseRequest{})
	if err != nil {
		t.Fatalf("Close err: %v", err)
	}
	mustErrCode(t, resp.GetError(),
		driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT, "session_id is required")

	resp, err = srv.Close(context.Background(), &driverv1alpha1.CloseRequest{SessionId: "nope"})
	if err != nil {
		t.Fatalf("Close err: %v", err)
	}
	mustErrCode(t, resp.GetError(),
		driverv1alpha1.DriverError_CODE_INVALID_ARGUMENT, "unknown session_id")
}

func TestCloseEvictsRegisteredSession(t *testing.T) {
	srv, mgr := newServerWithFetcher(t, mustNotCall(t))
	session := mgr.Create()
	if !mgr.Has(session.ID) {
		t.Fatal("precondition: manager must hold the session")
	}

	resp, err := srv.Close(context.Background(), &driverv1alpha1.CloseRequest{SessionId: session.ID})
	if err != nil {
		t.Fatalf("Close err: %v", err)
	}
	if resp.GetError() != nil {
		t.Fatalf("expected no error on successful Close; got %v", resp.GetError())
	}
	if mgr.Has(session.ID) {
		t.Fatal("Close must evict the session")
	}
}

// ----------------------------------------------------------------------------
// helpers

func fakeOK(finalURL string) Fetcher {
	return func(_ context.Context, _ curlx.Options) (*curlx.Response, error) {
		return &curlx.Response{
			StatusCode: 200,
			FinalURL:   finalURL,
			Body:       []byte("ok"),
		}, nil
	}
}

func mustNotCall(t *testing.T) Fetcher {
	t.Helper()
	return func(_ context.Context, _ curlx.Options) (*curlx.Response, error) {
		t.Fatal("fetcher must not be called for this case")
		return nil, errors.New("unreachable")
	}
}

func mustErrCode(t *testing.T, err *driverv1alpha1.DriverError, code driverv1alpha1.DriverError_Code, msgSub string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected DriverError, got nil")
	}
	if err.GetCode() != code {
		t.Fatalf("code: got %v want %v (msg=%q)", err.GetCode(), code, err.GetMessage())
	}
	if msgSub != "" && !strings.Contains(err.GetMessage(), msgSub) {
		t.Fatalf("message %q missing substring %q", err.GetMessage(), msgSub)
	}
}

func mustGRPCCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected gRPC %v, got nil", want)
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status err, got %v", err)
	}
	if st.Code() != want {
		t.Fatalf("expected %v, got %v (%q)", want, st.Code(), st.Message())
	}
}
