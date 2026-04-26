// SPDX-License-Identifier: Apache-2.0

// Package curlx is the subprocess wrapper around the
// curl-impersonate binary (default `curl_chrome116`).
//
// Each Fetch call invokes the binary once via os/exec and parses
// the response. The architecture is one process per request — see
// ADR-0016 §1 for the rationale (architectural symmetry, CI
// simplicity, cross-platform robustness over micro-optimisation).
//
// The package is deliberately small: a Fetch entry point, an
// Options struct describing the request, a Response struct
// describing the response, and a few private helpers that build
// the command line and parse the binary's output. No retries, no
// connection pooling, no concurrency primitives — the gRPC handler
// is the orchestration layer above.
package curlx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// DefaultVariant is the curl-impersonate binary name the adapter
// invokes when no override is supplied via SPECTRE_CURL_VARIANT.
// See ADR-0016 §3.
const DefaultVariant = "curl_chrome116"

// metaSentinel marks the boundary between the response body and
// the trailing line written by curl's `-w` flag. Chosen to be
// extremely unlikely to occur inside an HTML or JSON body. The
// sentinel is paired with a newline so the parser can split on a
// single token.
const metaSentinel = "\n__SPECTRE_CURL_META__\n"

// Options describes one HTTP request the adapter wants to make.
//
// CookieJarPath, Timeout, and Variant are typically session-level
// settings that the gRPC handler threads through; URL and Headers
// are per-request.
type Options struct {
	// URL to fetch. Must be an absolute http(s) URL; the gRPC
	// handler validates this before calling Fetch.
	URL string

	// Headers to send. Empty map = none. Headers are passed via
	// `-H "Key: Value"` arguments — curl handles encoding.
	Headers map[string]string

	// CookieJarPath is the absolute path to a cookie-jar file that
	// curl reads from (`--cookie`) and writes to (`--cookie-jar`).
	// Empty string = no cookie persistence (one-shot request).
	CookieJarPath string

	// Timeout caps the entire request including connection,
	// transfer, and any redirects. Zero = no timeout (let curl
	// use its built-in default, typically unlimited). The gRPC
	// handler defaults this to a sensible value.
	Timeout time.Duration

	// Variant is the curl-impersonate binary name. Empty = use
	// DefaultVariant. ADR-0016 §3 records the default rationale
	// and the SPECTRE_CURL_VARIANT override mechanism (the
	// override is resolved at adapter startup; this struct
	// receives the resolved string).
	Variant string

	// MaxRedirects caps the number of redirects curl will follow.
	// Zero = use curl's built-in cap (50).
	MaxRedirects int
}

// Response is the parsed result of one curl invocation.
type Response struct {
	// StatusCode is the HTTP response status of the final
	// response (after redirects). Zero if curl could not parse
	// one — the gRPC handler treats zero as "unknown" rather
	// than as an error.
	StatusCode uint32

	// FinalURL is the URL after any redirects. Equals
	// Options.URL for non-redirected requests.
	FinalURL string

	// Body is the response body bytes. May be empty for HEAD
	// or for status codes that lack a body.
	Body []byte

	// Elapsed is the wall-clock time the curl subprocess took.
	Elapsed time.Duration
}

// CurlError surfaces a curl subprocess failure with the original
// exit code and stderr text preserved so the error mapping table
// (internal/errors) can render structured DriverError values.
type CurlError struct {
	ExitCode int
	Stderr   string
	Variant  string
	Err      error
}

func (e *CurlError) Error() string {
	if e.Err != nil && e.ExitCode == 0 {
		return fmt.Sprintf("curl-impersonate (%s): %v", e.Variant, e.Err)
	}
	return fmt.Sprintf("curl-impersonate (%s) exited %d: %s", e.Variant, e.ExitCode, strings.TrimSpace(e.Stderr))
}

func (e *CurlError) Unwrap() error { return e.Err }

// Fetch invokes the curl-impersonate binary once and returns the
// parsed response. The context is propagated to the subprocess so
// the caller can cancel the in-flight request — see ADR-0016 §1
// (CommandContext for SIGTERM cancellation).
func Fetch(ctx context.Context, opts Options) (*Response, error) {
	variant := opts.Variant
	if variant == "" {
		variant = DefaultVariant
	}

	args := buildArgs(opts)

	cmd := exec.CommandContext(ctx, variant, args...) //nolint:gosec // variant is a curated allowlist via SPECTRE_CURL_VARIANT
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)
	if err != nil {
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		return nil, &CurlError{
			ExitCode: exitCode,
			Stderr:   stderr.String(),
			Variant:  variant,
			Err:      err,
		}
	}

	resp, parseErr := parseOutput(stdout.Bytes())
	if parseErr != nil {
		return nil, &CurlError{
			ExitCode: 0,
			Stderr:   stderr.String(),
			Variant:  variant,
			Err:      parseErr,
		}
	}
	resp.Elapsed = elapsed
	return resp, nil
}

// buildArgs renders the curl command-line argument list. Exposed
// for unit tests — the gRPC handler should not call this
// directly.
func buildArgs(opts Options) []string {
	args := []string{
		// Follow redirects by default. The protocol's NavigateResponse
		// reports `final_url` after redirects (driver.proto).
		"-L",
		// Silent: suppress curl's progress meter on stderr; the
		// adapter writes its own diagnostics.
		"-sS",
	}

	// Status code + final URL emitted after the body, separated
	// by metaSentinel. The newline before the sentinel separates
	// it from the last byte of the body so binary bodies with no
	// trailing newline are still parseable.
	args = append(args, "-w", metaSentinel+"%{http_code}\t%{url_effective}\n")

	if opts.Timeout > 0 {
		// curl's --max-time is in seconds; partial seconds are
		// allowed so we render millisecond precision.
		seconds := opts.Timeout.Seconds()
		args = append(args, "--max-time", strconv.FormatFloat(seconds, 'f', 3, 64))
	}

	if opts.MaxRedirects > 0 {
		args = append(args, "--max-redirs", strconv.Itoa(opts.MaxRedirects))
	}

	if opts.CookieJarPath != "" {
		// `--cookie` reads cookies in for the request; `--cookie-jar`
		// persists any Set-Cookie responses back to the same file.
		// Pointing both at the same path gives session-scoped
		// persistence across multiple Navigates in the same session.
		args = append(args, "--cookie", opts.CookieJarPath, "--cookie-jar", opts.CookieJarPath)
	}

	for key, value := range opts.Headers {
		args = append(args, "-H", fmt.Sprintf("%s: %s", key, value))
	}

	args = append(args, "--", opts.URL)
	return args
}

// parseOutput splits curl's combined stdout (body + sentinel +
// meta line) into a populated Response. Exposed for unit tests.
func parseOutput(out []byte) (*Response, error) {
	idx := bytes.LastIndex(out, []byte(metaSentinel))
	if idx < 0 {
		return nil, fmt.Errorf("curl output missing meta sentinel; received %d bytes", len(out))
	}
	body := out[:idx]
	meta := strings.TrimSpace(string(out[idx+len(metaSentinel):]))

	// Meta line is `<status>\t<final_url>` (tab-separated).
	parts := strings.SplitN(meta, "\t", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("curl meta line malformed: %q", meta)
	}
	statusStr := strings.TrimSpace(parts[0])
	finalURL := strings.TrimSpace(parts[1])

	status, err := strconv.ParseUint(statusStr, 10, 32)
	if err != nil {
		// curl emits "000" when no response was received (e.g. DNS
		// failure that nonetheless returned exit zero, which should
		// not happen but defending here keeps parsing total).
		status = 0
	}

	return &Response{
		StatusCode: uint32(status),
		FinalURL:   finalURL,
		Body:       append([]byte(nil), body...),
	}, nil
}

// MetaSentinel is exported so tests can construct synthetic outputs.
func MetaSentinel() string { return metaSentinel }
