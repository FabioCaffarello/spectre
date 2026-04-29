// SPDX-License-Identifier: Apache-2.0

package sessions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-redis/redismock/v9"

	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/elements"
	redisx "github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/redis"
)

const testInstanceID = "instance-aaaa"

func mustParse(t *testing.T, body string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

// newTestManager constructs a Manager whose cookie-jar files
// live under a per-test temp dir, with a deterministic uuidFn
// and a redismock-backed Redis client. ``setExpect`` is invoked
// with the redismock so individual tests can wire the
// expectations they want.
func newTestManager(t *testing.T, setExpect func(redismock.ClientMock)) (*Manager, redismock.ClientMock) {
	t.Helper()
	dir := t.TempDir()
	rdb, mock := redismock.NewClientMock()
	t.Cleanup(func() { _ = rdb.Close() })
	if setExpect != nil {
		setExpect(mock)
	}
	m := newManagerIn(dir, redisx.NewClient(rdb), testInstanceID)
	counter := 0
	m.uuidFn = func() string {
		counter++
		return "test-id-" + string(rune('a'+counter-1))
	}
	return m, mock
}

func expectAnySetSession(mock redismock.ClientMock) {
	// redismock's regex-mode matcher; we only care that a SET
	// against the namespaced key landed with the right TTL.
	mock.MatchExpectationsInOrder(false)
	mock.Regexp().ExpectSet(
		`session:`+redisx.AdapterName+`:.+`,
		`.+`,
		redisx.SessionTTL,
	).SetVal("OK")
}

// expectAnySetSessionN allows N matching SETs (Create + each
// Validate-OK refresh).
func expectAnySetSessionN(mock redismock.ClientMock, n int) {
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < n; i++ {
		mock.Regexp().ExpectSet(
			`session:`+redisx.AdapterName+`:.+`,
			`.+`,
			redisx.SessionTTL,
		).SetVal("OK")
	}
}

func TestCreateAllocatesIDAndJarPathAndWritesRedis(t *testing.T) {
	m, mock := newTestManager(t, expectAnySetSession)
	s, err := m.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if s.ID != "test-id-a" {
		t.Fatalf("expected deterministic id 'test-id-a', got %q", s.ID)
	}
	if !strings.HasSuffix(s.CookieJarPath, "spectre-curl-test-id-a.cookies") {
		t.Fatalf("jar path: %q", s.CookieJarPath)
	}
	if s.Created.IsZero() {
		t.Fatal("Created timestamp must be set")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestCreateRedisFailureLeavesNoLocalState(t *testing.T) {
	m, mock := newTestManager(t, func(mock redismock.ClientMock) {
		mock.Regexp().ExpectSet(
			`session:`+redisx.AdapterName+`:.+`,
			`.+`,
			redisx.SessionTTL,
		).SetErr(errors.New("redis offline"))
	})
	if _, err := m.Create(context.Background()); err == nil {
		t.Fatal("expected Create to surface the redis error")
	}
	if m.Has("test-id-a") {
		t.Fatal("local registry must not retain the id when redis SET fails")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestHasGetUnknown(t *testing.T) {
	m, _ := newTestManager(t, expectAnySetSession)
	if m.Has("nope") {
		t.Fatal("Has should be false for unregistered id")
	}
	if _, err := m.Get("nope"); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("expected ErrUnknownSession, got %v", err)
	}
	s, err := m.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !m.Has(s.ID) {
		t.Fatal("Has should be true after Create")
	}
	got, err := m.Get(s.ID)
	if err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if got.ID != s.ID {
		t.Fatalf("Get returned wrong session: %v", got)
	}
}

// -- Validate (R4.3) ---------------------------------------------------

func TestValidateOKRefreshesTTL(t *testing.T) {
	m, mock := newTestManager(t, func(mock redismock.ClientMock) {
		// 1) Create writes the session.
		// 2) Validate's GET returns the metadata.
		// 3) Validate's refresh writes a new last_active_at.
		mock.MatchExpectationsInOrder(false)
		mock.Regexp().ExpectSet(
			`session:`+redisx.AdapterName+`:.+`,
			`.+`,
			redisx.SessionTTL,
		).SetVal("OK")
		mock.Regexp().ExpectGet(
			`session:` + redisx.AdapterName + `:.+`,
		).SetVal(`{"session_id":"test-id-a","adapter":"curl-impersonate","adapter_instance_id":"instance-aaaa","created_at":"x","last_active_at":"x","metadata":{}}`)
		mock.Regexp().ExpectSet(
			`session:`+redisx.AdapterName+`:.+`,
			`.+`,
			redisx.SessionTTL,
		).SetVal("OK")
	})
	s, err := m.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	v, err := m.Validate(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if v.Kind != ValidationOK {
		t.Fatalf("expected ValidationOK, got %v", v.Kind)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestValidateUnknown(t *testing.T) {
	m, mock := newTestManager(t, func(mock redismock.ClientMock) {
		mock.Regexp().ExpectGet(
			`session:` + redisx.AdapterName + `:.+`,
		).RedisNil()
	})
	v, err := m.Validate(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if v.Kind != ValidationUnknown {
		t.Fatalf("expected ValidationUnknown, got %v", v.Kind)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestValidateDifferentInstance(t *testing.T) {
	m, mock := newTestManager(t, func(mock redismock.ClientMock) {
		mock.Regexp().ExpectGet(
			`session:` + redisx.AdapterName + `:.+`,
		).SetVal(`{"session_id":"x","adapter":"curl-impersonate","adapter_instance_id":"instance-bbbb","created_at":"x","last_active_at":"x","metadata":{}}`)
	})
	v, err := m.Validate(context.Background(), "x")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if v.Kind != ValidationDifferentInstance {
		t.Fatalf("expected ValidationDifferentInstance, got %v", v.Kind)
	}
	if v.StoredInstanceID != "instance-bbbb" {
		t.Fatalf("StoredInstanceID: %q", v.StoredInstanceID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestValidateRedisErrorPropagates(t *testing.T) {
	m, mock := newTestManager(t, func(mock redismock.ClientMock) {
		mock.Regexp().ExpectGet(
			`session:` + redisx.AdapterName + `:.+`,
		).SetErr(errors.New("network down"))
	})
	if _, err := m.Validate(context.Background(), "x"); err == nil {
		t.Fatal("expected error from redis GET failure")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// -- Close -------------------------------------------------------------

func TestCloseRemovesSessionAndJarFile(t *testing.T) {
	m, mock := newTestManager(t, func(mock redismock.ClientMock) {
		expectAnySetSession(mock)
		mock.Regexp().ExpectDel(
			`session:` + redisx.AdapterName + `:.+`,
		).SetVal(1)
	})
	s, err := m.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Simulate curl having written a cookie-jar file.
	if err := os.WriteFile(s.CookieJarPath, []byte("# cookies\n"), 0o600); err != nil {
		t.Fatalf("write jar: %v", err)
	}

	if err := m.Close(context.Background(), s.ID); err != nil {
		t.Fatalf("Close err: %v", err)
	}
	if m.Has(s.ID) {
		t.Fatal("Has should be false after Close")
	}
	if _, err := os.Stat(s.CookieJarPath); !os.IsNotExist(err) {
		t.Fatalf("jar file should be gone, stat err: %v", err)
	}

	// Idempotent: a second Close returns ErrUnknownSession.
	if err := m.Close(context.Background(), s.ID); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("second Close should return ErrUnknownSession, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestCloseSwallowsRedisDeleteFailure(t *testing.T) {
	m, mock := newTestManager(t, func(mock redismock.ClientMock) {
		expectAnySetSession(mock)
		mock.Regexp().ExpectDel(
			`session:` + redisx.AdapterName + `:.+`,
		).SetErr(errors.New("redis blip"))
	})
	s, err := m.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Close(context.Background(), s.ID); err != nil {
		t.Fatalf("Close should swallow redis delete failures, got %v", err)
	}
	if m.Has(s.ID) {
		t.Fatal("local entry must be gone even on redis delete failure")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestCloseAllRemovesEveryJar(t *testing.T) {
	m, mock := newTestManager(t, func(mock redismock.ClientMock) {
		expectAnySetSessionN(mock, 2)
	})
	a, err := m.Create(context.Background())
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	b, err := m.Create(context.Background())
	if err != nil {
		t.Fatalf("Create b: %v", err)
	}
	for _, p := range []string{a.CookieJarPath, b.CookieJarPath} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	m.CloseAll()

	if m.Has(a.ID) || m.Has(b.ID) {
		t.Fatal("CloseAll must evict every session")
	}
	for _, p := range []string{a.CookieJarPath, b.CookieJarPath} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("jar %q should be gone, stat err: %v", p, err)
		}
	}

	// Idempotent: calling again is a no-op.
	m.CloseAll()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// -- ElementRegistry --------------------------------------------------

func TestSetDocumentCachesAndBumpsGeneration(t *testing.T) {
	m, _ := newTestManager(t, expectAnySetSession)
	s, err := m.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := m.CurrentGeneration(s.ID); got != 0 {
		t.Fatalf("expected gen 0 before any Navigate, got %d", got)
	}
	doc := mustParse(t, `<p>hi</p>`)
	if err := m.SetDocument(s.ID, doc); err != nil {
		t.Fatalf("SetDocument: %v", err)
	}
	if got := m.CurrentGeneration(s.ID); got != 1 {
		t.Fatalf("expected gen 1 after SetDocument, got %d", got)
	}
	if got := m.Document(s.ID); got == nil {
		t.Fatal("Document must be populated after SetDocument")
	}
}

func TestSetDocumentUnknownSession(t *testing.T) {
	m, _ := newTestManager(t, nil)
	if err := m.SetDocument("nope", mustParse(t, `<p/>`)); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("expected ErrUnknownSession, got %v", err)
	}
}

func TestAllocateAndLookupElement(t *testing.T) {
	m, _ := newTestManager(t, expectAnySetSession)
	s, err := m.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	doc := mustParse(t, `<ul><li>a</li><li>b</li></ul>`)
	if err := m.SetDocument(s.ID, doc); err != nil {
		t.Fatalf("SetDocument: %v", err)
	}
	ids := m.Allocate(s.ID, doc.Find("li"))
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %d", len(ids))
	}
	got := m.LookupElement(s.ID, ids[0])
	if got.Status != elements.StatusOK {
		t.Fatalf("expected StatusOK, got %v", got.Status)
	}
}

func TestSetDocumentSecondTimeInvalidatesPriorRefs(t *testing.T) {
	m, _ := newTestManager(t, expectAnySetSession)
	s, err := m.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	docA := mustParse(t, `<h1>A</h1>`)
	if err := m.SetDocument(s.ID, docA); err != nil {
		t.Fatalf("SetDocument A: %v", err)
	}
	ids := m.Allocate(s.ID, docA.Find("h1"))
	docB := mustParse(t, `<h1>B</h1>`)
	if err := m.SetDocument(s.ID, docB); err != nil {
		t.Fatalf("SetDocument B: %v", err)
	}
	got := m.LookupElement(s.ID, ids[0])
	if got.Status != elements.StatusStale {
		t.Fatalf("expected StatusStale after re-Navigate, got %v", got.Status)
	}
}

func TestCloseForgetsRegistryEntry(t *testing.T) {
	m, _ := newTestManager(t, func(mock redismock.ClientMock) {
		expectAnySetSession(mock)
		mock.Regexp().ExpectDel(
			`session:` + redisx.AdapterName + `:.+`,
		).SetVal(1)
	})
	s, err := m.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	doc := mustParse(t, `<h1>hi</h1>`)
	if err := m.SetDocument(s.ID, doc); err != nil {
		t.Fatalf("SetDocument: %v", err)
	}
	ids := m.Allocate(s.ID, doc.Find("h1"))
	if err := m.Close(context.Background(), s.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := m.LookupElement(s.ID, ids[0])
	if got.Status != elements.StatusUnknown {
		t.Fatalf("expected StatusUnknown after Close, got %v", got.Status)
	}
}

func TestSweepStaleRemovesPriorRunFiles(t *testing.T) {
	dir := t.TempDir()
	rdb, _ := redismock.NewClientMock()
	defer func() { _ = rdb.Close() }()
	m := newManagerIn(dir, redisx.NewClient(rdb), testInstanceID)

	stale := filepath.Join(dir, "spectre-curl-prior.cookies")
	keep := filepath.Join(dir, "unrelated.txt")
	for _, p := range []string{stale, keep} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := m.SweepStale(); err != nil {
		t.Fatalf("SweepStale: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale jar should be removed, stat err: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("unrelated file must survive sweep: %v", err)
	}
}
