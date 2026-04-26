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
// PR11 declares only `navigation`. PR12 will add the Query and
// Extract capability names alongside their conformance tests
// (declared = tested, ADR-0014 §1). The screenshot family and
// the JS-execution family will *never* be declared by this
// adapter — see ADR-0016 §5 for the rationale.
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
var names = []string{
	Navigation,
}

// Names returns a fresh copy of the declared capability list,
// alphabetically ordered. PR11 declares one entry; PR12 grows
// the list to five or six entries.
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
