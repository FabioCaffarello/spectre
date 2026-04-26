// SPDX-License-Identifier: Apache-2.0

package curlx

import (
	"strings"
	"testing"
	"time"
)

func TestBuildArgsBasics(t *testing.T) {
	args := buildArgs(Options{URL: "https://example.com"})

	mustContain(t, args, "-L")
	mustContain(t, args, "-sS")
	mustContain(t, args, "-w")
	mustContain(t, args, "--")
	mustContain(t, args, "https://example.com")

	// No cookie flags when CookieJarPath is empty.
	if hasArg(args, "--cookie") || hasArg(args, "--cookie-jar") {
		t.Fatalf("did not expect cookie flags when CookieJarPath is empty: %v", args)
	}
	// No timeout flag when Timeout is zero.
	if hasArg(args, "--max-time") {
		t.Fatalf("did not expect --max-time when Timeout is zero: %v", args)
	}
}

func TestBuildArgsCookieJar(t *testing.T) {
	args := buildArgs(Options{
		URL:           "https://example.com",
		CookieJarPath: "/tmp/spectre-curl-abc.cookies",
	})
	mustContain(t, args, "--cookie")
	mustContain(t, args, "--cookie-jar")
	cookieFlagCount := 0
	for i, a := range args {
		if a == "--cookie" || a == "--cookie-jar" {
			cookieFlagCount++
			if i+1 >= len(args) || args[i+1] != "/tmp/spectre-curl-abc.cookies" {
				t.Fatalf("cookie flag %s not followed by jar path; got %v", a, args)
			}
		}
	}
	if cookieFlagCount != 2 {
		t.Fatalf("expected --cookie and --cookie-jar both present, got %d cookie-related flags", cookieFlagCount)
	}
}

func TestBuildArgsTimeout(t *testing.T) {
	args := buildArgs(Options{
		URL:     "https://example.com",
		Timeout: 1500 * time.Millisecond,
	})
	mustContain(t, args, "--max-time")
	idx := -1
	for i, a := range args {
		if a == "--max-time" {
			idx = i
			break
		}
	}
	if idx < 0 || idx+1 >= len(args) {
		t.Fatalf("--max-time missing value: %v", args)
	}
	if args[idx+1] != "1.500" {
		t.Fatalf("expected --max-time 1.500, got %q", args[idx+1])
	}
}

func TestBuildArgsHeaders(t *testing.T) {
	args := buildArgs(Options{
		URL:     "https://example.com",
		Headers: map[string]string{"X-Test": "value"},
	})
	want := "X-Test: value"
	if !hasArg(args, want) {
		t.Fatalf("expected header arg %q in args; got %v", want, args)
	}
}

func TestBuildArgsMaxRedirects(t *testing.T) {
	args := buildArgs(Options{
		URL:          "https://example.com",
		MaxRedirects: 3,
	})
	idx := -1
	for i, a := range args {
		if a == "--max-redirs" {
			idx = i
			break
		}
	}
	if idx < 0 || args[idx+1] != "3" {
		t.Fatalf("expected --max-redirs 3, got %v", args)
	}
}

func TestParseOutputHappy(t *testing.T) {
	body := "<html><body>hello</body></html>"
	combined := body + metaSentinel + "200\thttps://example.com/final\n"

	resp, err := parseOutput([]byte(combined))
	if err != nil {
		t.Fatalf("parseOutput err: %v", err)
	}
	if string(resp.Body) != body {
		t.Fatalf("body mismatch: got %q", string(resp.Body))
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status: got %d", resp.StatusCode)
	}
	if resp.FinalURL != "https://example.com/final" {
		t.Fatalf("final url: got %q", resp.FinalURL)
	}
}

func TestParseOutputMissingSentinel(t *testing.T) {
	_, err := parseOutput([]byte("just a body, no sentinel"))
	if err == nil {
		t.Fatal("expected error when sentinel is missing")
	}
	if !strings.Contains(err.Error(), "meta sentinel") {
		t.Fatalf("expected meta-sentinel error, got %v", err)
	}
}

func TestParseOutputMalformedMeta(t *testing.T) {
	combined := "body" + metaSentinel + "this is not valid meta\n"
	_, err := parseOutput([]byte(combined))
	if err == nil {
		t.Fatal("expected error when meta line lacks tab separator")
	}
}

func TestParseOutputZeroStatus(t *testing.T) {
	// curl emits "000" when no HTTP response was received.
	combined := metaSentinel + "000\thttps://example.com\n"
	resp, err := parseOutput([]byte(combined))
	if err != nil {
		t.Fatalf("parseOutput err: %v", err)
	}
	if resp.StatusCode != 0 {
		t.Fatalf("expected status 0 for curl 000, got %d", resp.StatusCode)
	}
}

func mustContain(t *testing.T, args []string, want string) {
	t.Helper()
	if !hasArg(args, want) {
		t.Fatalf("expected arg %q in %v", want, args)
	}
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
