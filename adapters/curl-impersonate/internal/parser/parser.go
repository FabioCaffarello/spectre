// SPDX-License-Identifier: Apache-2.0

// Package parser wraps goquery and htmlquery so the rest of the
// curl-impersonate adapter sees one uniform shape for HTML
// parsing, CSS selection, and XPath selection.
//
// PR12 introduces this package so Query and Extract have a
// consistent surface against the response body cached by
// Navigate. The contract:
//
//   - Parse(body) -> *goquery.Document. The document is
//     immutable for the lifetime of the session generation
//     (ADR-0017 §3 — no mid-generation staleness).
//   - XPathQuery(doc, expr) -> *goquery.Selection. Built atop
//     htmlquery.QueryAll over the same *html.Node graph
//     goquery wraps; downstream code does not need to know
//     two libraries are involved.
//   - OuterHtml(selection) -> (string, error). goquery exposes
//     inner HTML via Selection.Html(); outer HTML requires
//     html.Render against the underlying node.
//
// See ADR-0017 §2 for the goquery + htmlquery rationale.
package parser

import (
	"bytes"
	"fmt"

	"github.com/PuerkitoBio/goquery"
	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"
)

// Parse turns a response body into a *goquery.Document. The
// underlying parser (golang.org/x/net/html) is permissive and
// matches browser behaviour on malformed input — important for
// cross-driver equivalence (ADR-0017 §2).
func Parse(body []byte) (*goquery.Document, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}
	return doc, nil
}

// XPathQuery evaluates an XPath expression against a parsed
// document and wraps the resulting nodes in a *goquery.Selection
// so the Extract handler sees the same shape regardless of
// SelectorKind. Returns an empty Selection (not an error) when
// the expression matches nothing.
//
// Errors from htmlquery (malformed XPath, mostly) are surfaced
// to the caller; the gRPC handler maps them to
// CODE_INVALID_ARGUMENT with the original message.
func XPathQuery(doc *goquery.Document, expr string) (*goquery.Selection, error) {
	if doc == nil {
		return nil, fmt.Errorf("xpath query: document is nil")
	}
	root := doc.Get(0)
	if root == nil {
		// An empty document still wraps a synthetic root, so this
		// branch is defensive rather than expected.
		return doc.Slice(0, 0), nil
	}
	nodes, err := htmlquery.QueryAll(root, expr)
	if err != nil {
		return nil, fmt.Errorf("xpath %q: %w", expr, err)
	}
	// Build a fresh selection scoped to the document by starting
	// from an empty slice of the document's selection (which keeps
	// the same underlying *html.Document association) and adding
	// the htmlquery-returned nodes. AddNodes returns a new
	// Selection without mutating the DOM.
	return doc.Slice(0, 0).AddNodes(nodes...), nil
}

// OuterHtml returns the outer HTML of the first node in the
// selection. goquery's Selection.Html() returns inner HTML; outer
// HTML requires golang.org/x/net/html.Render against the node
// itself.
//
// Returns the empty string (no error) for an empty selection so
// callers do not have to special-case it; consumers that want to
// distinguish "empty selection" from "empty markup" check the
// selection's length before calling.
func OuterHtml(s *goquery.Selection) (string, error) {
	if s == nil || s.Length() == 0 {
		return "", nil
	}
	node := s.Get(0)
	if node == nil {
		return "", nil
	}
	var buf bytes.Buffer
	if err := html.Render(&buf, node); err != nil {
		return "", fmt.Errorf("render outer html: %w", err)
	}
	return buf.String(), nil
}
