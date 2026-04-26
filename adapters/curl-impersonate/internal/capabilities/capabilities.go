// SPDX-License-Identifier: Apache-2.0

// Package capabilities exports the capability list the
// curl-impersonate adapter declares at handshake time and the
// coherence check from ADR-0010 §3 / ADR-0014 §1.
//
// The exported list MUST stay in lockstep with `driver.yaml`'s
// `capabilities:` block — the conformance suite asserts
// byte-for-byte equality at runtime, including order.
//
// Order: alphabetical by capability name. The conformance suite
// asserts list-equality (not set-equality) between this constant
// and the manifest, so a deterministic order is required.
//
// PR12 grows the list to six entries — alphabetical:
// extract_attribute, extract_html, extract_text, navigation,
// query_css, query_xpath. query_text and query_attribute are
// deliberately omitted because semantic equivalence with the
// browser drivers cannot be honoured by a static-HTML adapter
// (ADR-0017 §1). The screenshot family and the JS-execution
// family remain permanently absent (no DOM, no rendering, no JS
// engine) — see ADR-0016 §5.
package capabilities

import "fmt"

// Capability name constants. Defined here rather than inlined so
// the conformance check and the gRPC handler reference the same
// strings.
const (
	ExtractAttribute  = "extract_attribute"
	ExtractEval       = "extract_eval"
	ExtractHTML       = "extract_html"
	ExtractText       = "extract_text"
	JSExecution       = "js_execution"
	Navigation        = "navigation"
	QueryAttribute    = "query_attribute"
	QueryCSS          = "query_css"
	QueryText         = "query_text"
	QueryXPath        = "query_xpath"
	ScreenshotElement = "screenshot_element"
	ScreenshotFullPg  = "screenshot_full_page"
	ScreenshotVwprt   = "screenshot_viewport"
)

// DriverVersion is the curl-impersonate adapter's own version,
// surfaced as `Capabilities.driver_version` at handshake.
const DriverVersion = "0.1.0a0"

// names is the ordered, alphabetical list of capabilities
// declared at handshake. Kept private so callers go through
// Names() and cannot mutate the slice in place.
//
// Six entries (alphabetical) as of PR12. The list will not grow
// further in v1alpha1 — `js_execution` and the screenshot family
// require runtime primitives this driver does not have, and
// `query_text` / `query_attribute` would violate the cross-driver
// semantic-equivalence contract from ADR-0017 §1.
var names = []string{
	ExtractAttribute,
	ExtractHTML,
	ExtractText,
	Navigation,
	QueryCSS,
	QueryXPath,
}

// Names returns a fresh copy of the declared capability list,
// alphabetically ordered. The byte-for-byte conformance assertion
// from ADR-0008 compares this against driver.yaml; the order is
// load-bearing.
func Names() []string {
	out := make([]string, len(names))
	copy(out, names)
	return out
}

// AssertCoherence raises an error if the declared list violates
// a coherence invariant. Currently:
//
//   - extract_eval declared without js_execution is a
//     contradiction (the runtime gate would reject every
//     MODE_EVAL call). ADR-0010 §3 introduced the rule for the
//     Playwright adapter; ADR-0014 §1 carried it to SeleniumBase;
//     ADR-0016 §5 carries it forward to curl-impersonate even
//     though the curl-impersonate adapter will never declare
//     either capability — symmetric implementation across the
//     three drivers is what the rule preserves.
//
// Extensible: add cases as new capabilities imply each other.
func AssertCoherence(declared []string) error {
	set := make(map[string]struct{}, len(declared))
	for _, name := range declared {
		set[name] = struct{}{}
	}
	if _, hasEval := set[ExtractEval]; hasEval {
		if _, hasJS := set[JSExecution]; !hasJS {
			return fmt.Errorf("capability coherence violation: %s requires %s", ExtractEval, JSExecution)
		}
	}
	return nil
}

// Init runs the coherence check at package load so a hand-edited
// `names` slice that violates an invariant fails the import
// rather than the first RPC.
func init() {
	if err := AssertCoherence(names); err != nil {
		panic(err)
	}
}
