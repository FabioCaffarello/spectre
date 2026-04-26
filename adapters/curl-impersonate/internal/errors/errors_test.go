// SPDX-License-Identifier: Apache-2.0

package errors

import (
	"strings"
	"testing"

	driverv1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/driver/v1alpha1"
)

func TestMapKnownExitCodes(t *testing.T) {
	cases := []struct {
		name     string
		exitCode int
		stderr   string
		wantCode driverv1alpha1.DriverError_Code
		wantSub  string
	}{
		{"dns_failure", 6, "curl: (6) Could not resolve host: nope.invalid",
			driverv1alpha1.DriverError_CODE_TARGET_UNREACHABLE, "Could not resolve host"},
		{"connection_refused", 7, "curl: (7) Failed to connect to localhost port 9 after 0 ms: Connection refused",
			driverv1alpha1.DriverError_CODE_TARGET_UNREACHABLE, "Connection refused"},
		{"timeout", 28, "curl: (28) Operation timed out after 5000 milliseconds",
			driverv1alpha1.DriverError_CODE_TIMEOUT, "Operation timed out"},
		{"tls_handshake", 35, "curl: (35) error:1408F10B:SSL routines:ssl3_get_record:wrong version number",
			driverv1alpha1.DriverError_CODE_TARGET_UNREACHABLE, "SSL"},
		{"peer_cert_invalid", 60, "curl: (60) SSL certificate problem",
			driverv1alpha1.DriverError_CODE_TARGET_UNREACHABLE, "SSL certificate"},
		{"too_many_redirects", 47, "curl: (47) Maximum (50) redirects followed",
			driverv1alpha1.DriverError_CODE_INTERNAL, "redirects"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Map(tc.exitCode, tc.stderr)
			if got.Code != tc.wantCode {
				t.Fatalf("code: got %v want %v (msg=%q)", got.Code, tc.wantCode, got.Message)
			}
			if !strings.Contains(got.Message, tc.wantSub) {
				t.Fatalf("message %q missing substring %q", got.Message, tc.wantSub)
			}
		})
	}
}

func TestMapStderrFallbackForUnknownExit(t *testing.T) {
	got := Map(99, "curl: (99) Could not resolve host: example.invalid")
	if got.Code != driverv1alpha1.DriverError_CODE_TARGET_UNREACHABLE {
		t.Fatalf("expected stderr fallback to TARGET_UNREACHABLE, got %v", got.Code)
	}
}

func TestMapCatchAllToInternal(t *testing.T) {
	got := Map(99, "totally unexpected stderr text")
	if got.Code != driverv1alpha1.DriverError_CODE_INTERNAL {
		t.Fatalf("expected INTERNAL fallback, got %v", got.Code)
	}
	if !strings.Contains(got.Message, "totally unexpected") {
		t.Fatalf("expected stderr preserved in message, got %q", got.Message)
	}
}

func TestMapEmptyStderrUsesFallbackMessage(t *testing.T) {
	got := Map(28, "")
	if !strings.Contains(got.Message, "timed out") {
		t.Fatalf("expected fallback message, got %q", got.Message)
	}
}

func TestMapBinaryMissingHint(t *testing.T) {
	got := MapBinaryMissing("curl_chrome116")
	if got.Code != driverv1alpha1.DriverError_CODE_INTERNAL {
		t.Fatalf("expected INTERNAL, got %v", got.Code)
	}
	if !strings.Contains(got.Message, "curl_chrome116") {
		t.Fatalf("expected variant name in message, got %q", got.Message)
	}
	if !strings.Contains(got.Message, "SPECTRE_CURL_VARIANT") {
		t.Fatalf("expected env var hint in message, got %q", got.Message)
	}
}
