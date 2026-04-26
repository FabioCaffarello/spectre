// SPDX-License-Identifier: Apache-2.0

// Package errors maps curl subprocess failures onto v1alpha1
// DriverError values.
//
// Substantively different from the Selenium and Playwright
// tables: curl errors come as integer exit codes (CURLE_*) and
// stderr patterns rather than typed exceptions. The shape
// mirrors the SeleniumBase `errors.py` and Playwright
// `errors.ts`: a sequence of rules tried in order, with a
// catch-all that collapses to CODE_INTERNAL so an unmapped
// failure never escapes as a transport exception.
//
// The v1alpha1 DriverError.Code enum is frozen (ADR-0004); new
// diagnostic content goes in the message, not in new wire
// fields. The same enum gaps documented in ADR-0009 apply: no
// dedicated UNAVAILABLE for missing binaries, no NETWORK split,
// no UNKNOWN — those collapse to CODE_INTERNAL with an
// actionable message.
//
// curl exit-code reference (curl(1) man page, EXIT CODES):
//
//	  6  CURLE_COULDNT_RESOLVE_HOST
//	  7  CURLE_COULDNT_CONNECT
//	 28  CURLE_OPERATION_TIMEDOUT
//	 35  CURLE_SSL_CONNECT_ERROR
//	 47  CURLE_TOO_MANY_REDIRECTS
//	 60  CURLE_PEER_FAILED_VERIFICATION
//
// See ADR-0016 §1 (subprocess model) for why this table exists
// at all.
package errors

import (
	"strings"

	driverv1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/driver/v1alpha1"
)

// MappedError pairs a v1alpha1 DriverError code with a
// user-facing message. The gRPC handler renders this onto the
// proto envelope.
type MappedError struct {
	Code    driverv1alpha1.DriverError_Code
	Message string
}

// Map translates a curl exit code and stderr output into a
// MappedError. Returns CODE_INTERNAL with the original stderr
// when no specific rule matches — an unmapped failure is still
// a structured DriverError, never a transport exception.
//
// stderr is best-effort: curl's `-sS` mode emits a single line
// for most errors, but malformed argv or signal-induced exits
// can yield empty stderr. The trimmed message is used in either
// case.
func Map(exitCode int, stderr string) MappedError {
	trimmed := strings.TrimSpace(stderr)

	switch exitCode {
	case 6:
		return MappedError{
			Code:    driverv1alpha1.DriverError_CODE_TARGET_UNREACHABLE,
			Message: messageOr(trimmed, "could not resolve host"),
		}
	case 7:
		return MappedError{
			Code:    driverv1alpha1.DriverError_CODE_TARGET_UNREACHABLE,
			Message: messageOr(trimmed, "could not connect to host"),
		}
	case 28:
		return MappedError{
			Code:    driverv1alpha1.DriverError_CODE_TIMEOUT,
			Message: messageOr(trimmed, "operation timed out"),
		}
	case 35, 60:
		return MappedError{
			Code:    driverv1alpha1.DriverError_CODE_TARGET_UNREACHABLE,
			Message: messageOr(trimmed, "tls handshake failed"),
		}
	case 47:
		return MappedError{
			Code:    driverv1alpha1.DriverError_CODE_INTERNAL,
			Message: messageOr(trimmed, "too many redirects"),
		}
	}

	// Fallback: scan stderr for known phrases when the exit code
	// does not match. Some curl-impersonate variants surface DNS
	// or connection failures with a non-standard exit code.
	lower := strings.ToLower(trimmed)
	switch {
	case strings.Contains(lower, "could not resolve host"),
		strings.Contains(lower, "name or service not known"):
		return MappedError{
			Code:    driverv1alpha1.DriverError_CODE_TARGET_UNREACHABLE,
			Message: messageOr(trimmed, "could not resolve host"),
		}
	case strings.Contains(lower, "connection refused"),
		strings.Contains(lower, "couldn't connect"),
		strings.Contains(lower, "could not connect"):
		return MappedError{
			Code:    driverv1alpha1.DriverError_CODE_TARGET_UNREACHABLE,
			Message: messageOr(trimmed, "could not connect to host"),
		}
	case strings.Contains(lower, "timed out"):
		return MappedError{
			Code:    driverv1alpha1.DriverError_CODE_TIMEOUT,
			Message: messageOr(trimmed, "operation timed out"),
		}
	}

	return MappedError{
		Code:    driverv1alpha1.DriverError_CODE_INTERNAL,
		Message: messageOr(trimmed, "curl-impersonate failed"),
	}
}

// MapBinaryMissing surfaces a missing curl-impersonate binary
// with an actionable hint. Called when os/exec returns a
// "file not found" error before the subprocess starts; the
// adapter cannot serve any Navigate without the binary.
func MapBinaryMissing(variant string) MappedError {
	return MappedError{
		Code: driverv1alpha1.DriverError_CODE_INTERNAL,
		Message: "curl-impersonate binary " + variant + " not found on PATH; " +
			"install from https://github.com/lwthiker/curl-impersonate/releases " +
			"or override SPECTRE_CURL_VARIANT",
	}
}

func messageOr(message, fallback string) string {
	if message == "" {
		return fallback
	}
	return message
}
