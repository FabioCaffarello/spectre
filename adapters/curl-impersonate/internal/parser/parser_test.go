// SPDX-License-Identifier: Apache-2.0

package parser

import (
	"strings"
	"testing"
)

const sampleHTML = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>elements</title></head>
<body>
<h1 id="title">Elements Page</h1>
<ul id="items">
<li class="item">first</li>
<li class="item">second</li>
<li class="item">third</li>
</ul>
<a id="link" href="https://example.com/target">visit</a>
<div id="badge" data-test="primary">Primary</div>
</body>
</html>`

func TestParseReturnsDocument(t *testing.T) {
	doc, err := Parse([]byte(sampleHTML))
	if err != nil {
		t.Fatalf("Parse err: %v", err)
	}
	if doc == nil {
		t.Fatal("Parse must return a non-nil document")
	}
	if title := doc.Find("title").Text(); title != "elements" {
		t.Fatalf("expected <title> 'elements', got %q", title)
	}
	if items := doc.Find("li.item"); items.Length() != 3 {
		t.Fatalf("expected 3 li.item, got %d", items.Length())
	}
}

func TestParseHandlesMalformedHTML(t *testing.T) {
	// Browsers (and golang.org/x/net/html) absorb missing close
	// tags. The adapter relies on this for cross-driver
	// equivalence — see ADR-0017 §2.
	body := []byte(`<html><body><p>open <span>no close</body></html>`)
	doc, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse err: %v", err)
	}
	if got := doc.Find("p").Text(); !strings.Contains(got, "open") {
		t.Fatalf("expected <p> text to contain 'open', got %q", got)
	}
}

func TestXPathQueryFindsElements(t *testing.T) {
	doc, err := Parse([]byte(sampleHTML))
	if err != nil {
		t.Fatalf("Parse err: %v", err)
	}
	sel, err := XPathQuery(doc, "//li[@class='item']")
	if err != nil {
		t.Fatalf("XPathQuery err: %v", err)
	}
	if sel.Length() != 3 {
		t.Fatalf("expected 3 li.item via XPath, got %d", sel.Length())
	}
	if got := sel.First().Text(); got != "first" {
		t.Fatalf("expected first item to read 'first', got %q", got)
	}
}

func TestXPathQueryEmptyOnNoMatch(t *testing.T) {
	doc, err := Parse([]byte(sampleHTML))
	if err != nil {
		t.Fatalf("Parse err: %v", err)
	}
	sel, err := XPathQuery(doc, "//section[@class='nope']")
	if err != nil {
		t.Fatalf("XPathQuery err: %v", err)
	}
	if sel.Length() != 0 {
		t.Fatalf("expected zero matches, got %d", sel.Length())
	}
}

func TestXPathQuerySurfacesMalformedExpression(t *testing.T) {
	doc, err := Parse([]byte(sampleHTML))
	if err != nil {
		t.Fatalf("Parse err: %v", err)
	}
	if _, err := XPathQuery(doc, "[[not valid xpath"); err == nil {
		t.Fatal("expected an error for malformed XPath")
	}
}

func TestOuterHtmlRendersFirstNode(t *testing.T) {
	doc, err := Parse([]byte(sampleHTML))
	if err != nil {
		t.Fatalf("Parse err: %v", err)
	}
	sel := doc.Find("#title")
	got, err := OuterHtml(sel)
	if err != nil {
		t.Fatalf("OuterHtml err: %v", err)
	}
	want := `<h1 id="title">Elements Page</h1>`
	if got != want {
		t.Fatalf("OuterHtml: got %q, want %q", got, want)
	}
}

func TestOuterHtmlEmptySelectionIsEmptyString(t *testing.T) {
	doc, err := Parse([]byte(sampleHTML))
	if err != nil {
		t.Fatalf("Parse err: %v", err)
	}
	got, err := OuterHtml(doc.Find(".no-such-class"))
	if err != nil {
		t.Fatalf("OuterHtml err: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty string for empty selection, got %q", got)
	}
}
