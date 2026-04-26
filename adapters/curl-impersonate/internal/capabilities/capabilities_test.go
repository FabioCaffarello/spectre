// SPDX-License-Identifier: Apache-2.0

package capabilities

import (
	"reflect"
	"strings"
	"testing"
)

func TestNamesReturnsPR11List(t *testing.T) {
	got := Names()
	want := []string{"navigation"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PR11 capability list must be exactly %v; got %v", want, got)
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

func TestAssertCoherenceAcceptsNavigationOnly(t *testing.T) {
	if err := AssertCoherence([]string{Navigation}); err != nil {
		t.Fatalf("PR11 declared list must satisfy coherence: %v", err)
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
