// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunPrintsIdentity(t *testing.T) {
	var buf bytes.Buffer
	if err := run(&buf); err != nil {
		t.Fatalf("run() returned error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{binaryName, version, protocolVersion} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %q", out, want)
		}
	}
}
