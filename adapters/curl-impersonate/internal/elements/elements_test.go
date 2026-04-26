// SPDX-License-Identifier: Apache-2.0

package elements

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

const sampleHTML = `<!doctype html><html><body>
<h1 id="title">Title</h1>
<ul id="items"><li>a</li><li>b</li><li>c</li></ul>
</body></html>`

func mustDoc(t *testing.T) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(sampleHTML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func newDeterministic(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	counter := 0
	r.uuidFn = func() string {
		counter++
		return "ref-" + string(rune('a'+counter-1))
	}
	return r
}

func TestAllocateAssignsUniqueIDs(t *testing.T) {
	r := newDeterministic(t)
	doc := mustDoc(t)
	r.BumpGeneration("s1") // simulate post-Navigate
	ids := r.Allocate("s1", doc.Find("li"))
	if len(ids) != 3 {
		t.Fatalf("expected 3 ids, got %d", len(ids))
	}
	if ids[0] == ids[1] || ids[1] == ids[2] {
		t.Fatalf("ids must be unique: %v", ids)
	}
}

func TestLookupReturnsOKWhenGenerationMatches(t *testing.T) {
	r := newDeterministic(t)
	doc := mustDoc(t)
	r.BumpGeneration("s1")
	ids := r.Allocate("s1", doc.Find("h1"))
	if got := r.Lookup("s1", ids[0]); got.Status != StatusOK {
		t.Fatalf("expected StatusOK, got %v", got.Status)
	} else if text := got.Selection.Text(); text != "Title" {
		t.Fatalf("expected 'Title', got %q", text)
	}
}

func TestLookupReturnsStaleAfterGenerationBump(t *testing.T) {
	r := newDeterministic(t)
	doc := mustDoc(t)
	r.BumpGeneration("s1")
	ids := r.Allocate("s1", doc.Find("h1"))
	r.BumpGeneration("s1") // new Navigate
	got := r.Lookup("s1", ids[0])
	if got.Status != StatusStale {
		t.Fatalf("expected StatusStale, got %v", got.Status)
	}
	if got.Selection != nil {
		t.Fatal("StatusStale must not surface a Selection")
	}
}

func TestLookupReturnsUnknownForMissingID(t *testing.T) {
	r := newDeterministic(t)
	r.BumpGeneration("s1")
	got := r.Lookup("s1", "no-such-ref")
	if got.Status != StatusUnknown {
		t.Fatalf("expected StatusUnknown, got %v", got.Status)
	}
}

func TestLookupReturnsUnknownForUnknownSession(t *testing.T) {
	r := newDeterministic(t)
	got := r.Lookup("s1", "anything")
	if got.Status != StatusUnknown {
		t.Fatalf("expected StatusUnknown, got %v", got.Status)
	}
}

func TestForgetSessionRemovesAllRefs(t *testing.T) {
	r := newDeterministic(t)
	doc := mustDoc(t)
	r.BumpGeneration("s1")
	ids := r.Allocate("s1", doc.Find("li"))
	r.ForgetSession("s1")
	if got := r.Lookup("s1", ids[0]); got.Status != StatusUnknown {
		t.Fatalf("expected StatusUnknown after forget, got %v", got.Status)
	}
	// Idempotent.
	r.ForgetSession("s1")
}

func TestBumpGenerationOnEmptySessionStartsAtOne(t *testing.T) {
	r := newDeterministic(t)
	r.BumpGeneration("s1")
	if got := r.CurrentGeneration("s1"); got != 1 {
		t.Fatalf("expected gen 1 after first bump, got %d", got)
	}
	r.BumpGeneration("s1")
	if got := r.CurrentGeneration("s1"); got != 2 {
		t.Fatalf("expected gen 2 after second bump, got %d", got)
	}
}

func TestCurrentGenerationZeroForUnknownSession(t *testing.T) {
	r := newDeterministic(t)
	if got := r.CurrentGeneration("unknown"); got != 0 {
		t.Fatalf("expected 0 for unknown session, got %d", got)
	}
}

func TestAllocateOnSessionWithoutBumpStoresAtGenZero(t *testing.T) {
	// Defensive: a Query before any Navigate is a protocol misuse,
	// but the registry should not crash. The gRPC handler is the
	// guard; the registry is permissive.
	r := newDeterministic(t)
	doc := mustDoc(t)
	ids := r.Allocate("s1", doc.Find("h1"))
	if len(ids) != 1 {
		t.Fatalf("expected 1 id, got %d", len(ids))
	}
	if got := r.Lookup("s1", ids[0]); got.Status != StatusOK {
		t.Fatalf("expected StatusOK at gen 0, got %v", got.Status)
	}
}
