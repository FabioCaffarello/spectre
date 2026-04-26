// SPDX-License-Identifier: Apache-2.0

package capabilities

import (
	"reflect"
	"strings"
	"testing"
)

func TestNamesReturnsPR12List(t *testing.T) {
	got := Names()
	want := []string{
		"extract_attribute",
		"extract_html",
		"extract_text",
		"navigation",
		"query_css",
		"query_xpath",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PR12 capability list must be exactly %v; got %v", want, got)
	}
}

func TestNamesAreAlphabetical(t *testing.T) {
	got := Names()
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Fatalf("capability list must be strictly alphabetical: %v", got)
		}
	}
}

func TestForbiddenCapabilitiesAbsent(t *testing.T) {
	// ADR-0016 §5 / ADR-0017 §1: this driver will never declare
	// any of these in v1alpha1.
	got := Names()
	forbidden := map[string]struct{}{
		"js_execution":         {},
		"extract_eval":         {},
		"screenshot_viewport":  {},
		"screenshot_element":   {},
		"screenshot_full_page": {},
		"query_text":           {},
		"query_attribute":      {},
	}
	for _, name := range got {
		if _, bad := forbidden[name]; bad {
			t.Fatalf("forbidden capability %q must not be declared", name)
		}
	}
}

func TestNamesReturnsACopy(t *testing.T) {
	a := Names()
	a[0] = "mutated"
	b := Names()
	if b[0] == "mutated" {
		t.Fatal("Names must return a fresh slice — caller mutation must not leak")
	}
}

func TestAssertCoherenceAcceptsDeclaredList(t *testing.T) {
	if err := AssertCoherence(Names()); err != nil {
		t.Fatalf("declared list must satisfy coherence: %v", err)
	}
}

func TestAssertCoherenceAcceptsEmpty(t *testing.T) {
	if err := AssertCoherence(nil); err != nil {
		t.Fatalf("empty list must satisfy coherence: %v", err)
	}
}

func TestAssertCoherenceRejectsExtractEvalWithoutJSExecution(t *testing.T) {
	err := AssertCoherence([]string{ExtractEval})
	if err == nil {
		t.Fatal("expected coherence violation for extract_eval without js_execution")
	}
	if !strings.Contains(err.Error(), "extract_eval") || !strings.Contains(err.Error(), "js_execution") {
		t.Fatalf("error message must name both capabilities: %v", err)
	}
}

func TestAssertCoherenceAcceptsExtractEvalWithJSExecution(t *testing.T) {
	// Defensive: the curl-impersonate adapter will never declare
	// either, but the rule must accept the legitimate combination
	// so the function stays a portable utility.
	if err := AssertCoherence([]string{ExtractEval, JSExecution}); err != nil {
		t.Fatalf("legitimate combination must satisfy coherence: %v", err)
	}
}

func TestDriverVersionPopulated(t *testing.T) {
	if DriverVersion == "" {
		t.Fatal("DriverVersion must be non-empty for the Capabilities envelope")
	}
}
