/*
Copyright 2026 Fabio Caffarello.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeSpectrePath is the path to the compiled fake_spectre helper
// binary. TestMain builds it once before the suite runs and removes
// it on exit.
var fakeSpectrePath string

func TestMain(m *testing.M) {
	src, err := filepath.Abs(filepath.Join("testdata", "fake_spectre.go"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve fake_spectre source: %v\n", err)
		os.Exit(1)
	}

	dir, err := os.MkdirTemp("", "spectre-runner-test-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "tempdir: %v\n", err)
		os.Exit(1)
	}
	bin := filepath.Join(dir, "fake_spectre")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	cmd := exec.Command("go", "build", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build fake_spectre: %v\n%s\n", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	fakeSpectrePath = bin

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func newFakeRunner(t *testing.T) *SubprocessRunner {
	t.Helper()
	if fakeSpectrePath == "" {
		t.Fatal("fakeSpectrePath unset; TestMain failed to build fake_spectre")
	}
	return &SubprocessRunner{EnginePath: fakeSpectrePath}
}

func TestSubprocessRunner_HappyPath_ReturnsRowCount(t *testing.T) {
	t.Setenv("SPECTRE_TEST_ROWS", "5")
	r := newFakeRunner(t)

	var buf bytes.Buffer
	rows, err := r.Run(context.Background(), "spectre: v1alpha1\n", &buf)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if rows != 5 {
		t.Fatalf("rows = %d, want 5", rows)
	}
	got := strings.Count(buf.String(), "\n")
	if got != 5 {
		t.Fatalf("writer received %d lines, want 5; output=%q", got, buf.String())
	}
	if !strings.Contains(buf.String(), `{"i":0}`) {
		t.Fatalf("writer missing first row; output=%q", buf.String())
	}
}

func TestSubprocessRunner_NonZeroExit_ReturnsError(t *testing.T) {
	t.Setenv("SPECTRE_TEST_FAIL", "1")
	r := newFakeRunner(t)

	rows, err := r.Run(context.Background(), "spectre: v1alpha1\n", &bytes.Buffer{})
	if err == nil {
		t.Fatal("Run() returned nil error, want non-nil")
	}
	if rows != 0 {
		t.Fatalf("rows = %d, want 0", rows)
	}
	if !strings.Contains(err.Error(), "exit") {
		t.Fatalf("error = %q, want substring \"exit\"", err.Error())
	}
}

func TestSubprocessRunner_ContextCancellation_KillsEngine(t *testing.T) {
	t.Setenv("SPECTRE_TEST_HANG", "1")
	r := newFakeRunner(t)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	rows, err := r.Run(ctx, "spectre: v1alpha1\n", &bytes.Buffer{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Run() returned nil error, want context error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want DeadlineExceeded or Canceled", err)
	}
	if rows != 0 {
		t.Fatalf("rows = %d, want 0 on cancellation", rows)
	}
	if elapsed > time.Second {
		t.Fatalf("Run() took %v; want sub-second cancellation", elapsed)
	}
}

func TestSubprocessRunner_EmptyLines_DoNotCount(t *testing.T) {
	t.Setenv("SPECTRE_TEST_BLANK_LINES", "1")
	r := newFakeRunner(t)

	var buf bytes.Buffer
	rows, err := r.Run(context.Background(), "spectre: v1alpha1\n", &buf)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if rows != 2 {
		t.Fatalf("rows = %d, want 2 (blank line must not count)", rows)
	}
}

func TestSubprocessRunner_LongRow_IsHandled(t *testing.T) {
	t.Setenv("SPECTRE_TEST_LONG_ROW", "1")
	r := newFakeRunner(t)

	var buf bytes.Buffer
	rows, err := r.Run(context.Background(), "spectre: v1alpha1\n", &buf)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows = %d, want 1", rows)
	}
	if buf.Len() < 100*1024 {
		t.Fatalf("writer received %d bytes, want >= 100 KiB", buf.Len())
	}
}

func TestSubprocessRunner_BinaryNotFound_ReturnsError(t *testing.T) {
	r := &SubprocessRunner{EnginePath: "/nonexistent/spectre-binary"}

	_, err := r.Run(context.Background(), "spectre: v1alpha1\n", &bytes.Buffer{})
	if err == nil {
		t.Fatal("Run() returned nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "/nonexistent/spectre-binary") {
		t.Fatalf("error = %q; want substring with the missing binary path", err.Error())
	}
}

func TestSubprocessRunner_EmptyEnginePath_ReturnsError(t *testing.T) {
	r := &SubprocessRunner{}
	_, err := r.Run(context.Background(), "spectre: v1alpha1\n", &bytes.Buffer{})
	if err == nil {
		t.Fatal("Run() with empty EnginePath returned nil error, want non-nil")
	}
}

// failingWriter returns the configured error on every Write,
// surfacing the writer-error branch of Run.
type failingWriter struct{ err error }

func (w *failingWriter) Write(_ []byte) (int, error) { return 0, w.err }

func TestSubprocessRunner_WriterError_SurfacesAsRunError(t *testing.T) {
	t.Setenv("SPECTRE_TEST_ROWS", "3")
	r := newFakeRunner(t)

	rows, err := r.Run(context.Background(), "spectre: v1alpha1\n",
		&failingWriter{err: errors.New("downstream broken")})
	if err == nil {
		t.Fatal("Run() returned nil, want write-row error")
	}
	if !strings.Contains(err.Error(), "write row") {
		t.Fatalf("err = %q, want substring \"write row\"", err.Error())
	}
	// We bail on the first failed write but still increment the
	// counter for the row we attempted; rows is the number of rows
	// observed, not the number successfully forwarded.
	if rows != 1 {
		t.Fatalf("rows = %d, want 1 (broken on first write)", rows)
	}
}

func TestSubprocessRunner_AdaptersPath_ForwardedAsFlag(t *testing.T) {
	// The fake binary rejects an invocation that lacks --output=-.
	// We piggy-back: set EnginePath to a tiny shell script that
	// dumps argv to stderr and exits 0 with one row on stdout.
	dir := t.TempDir()
	script := filepath.Join(dir, "spectre-argv")
	body := "#!/bin/sh\nfor a in \"$@\"; do echo \"$a\" >&2; done\nprintf '%s\\n' '{\"i\":0}'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write argv script: %v", err)
	}

	r := &SubprocessRunner{EnginePath: script, AdaptersPath: "/some/adapters"}

	var stderr bytes.Buffer
	// Re-route the runner's process stderr capture into our buffer
	// by swapping os.Stderr for the duration of this test.
	origStderr := os.Stderr
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = pw
	t.Cleanup(func() { os.Stderr = origStderr })

	done := make(chan struct{})
	go func() {
		_, _ = stderr.ReadFrom(pr)
		close(done)
	}()

	if _, err := r.Run(context.Background(), "spectre: v1alpha1\n", &bytes.Buffer{}); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	_ = pw.Close()
	<-done
	os.Stderr = origStderr

	if !strings.Contains(stderr.String(), "--adapters-path=/some/adapters") {
		t.Fatalf("argv missing --adapters-path; got: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "--output=-") {
		t.Fatalf("argv missing --output=-; got: %q", stderr.String())
	}
}
