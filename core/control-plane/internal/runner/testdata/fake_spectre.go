// SPDX-License-Identifier: Apache-2.0
//
// fake_spectre is a stand-in for the spectre engine binary used by
// the SubprocessRunner unit tests. It accepts the same argv shape
// the runner produces (`run <jobfile> --output=- [--adapters-path=…]`)
// and shapes its output via environment variables so each test can
// drive a different scenario from the same binary:
//
//   - SPECTRE_TEST_ROWS=N        emit N JSONL rows on stdout (default 1)
//   - SPECTRE_TEST_FAIL=1        write "engine failure" to stderr, exit 1
//   - SPECTRE_TEST_HANG=1        block forever (until killed by ctx)
//   - SPECTRE_TEST_BLANK_LINES=1 emit row, blank line, row (count 2)
//   - SPECTRE_TEST_LONG_ROW=1    emit one row with a 100 KiB string
//
// Lives under testdata/ so the Go toolchain skips it during normal
// package compilation; subprocess_test.go's TestMain compiles it
// explicitly via `go build` before the suite runs.

//go:build ignore

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	hasOutputDash := false
	for _, a := range os.Args[1:] {
		if a == "--output=-" {
			hasOutputDash = true
		}
	}
	if !hasOutputDash {
		fmt.Fprintln(os.Stderr, "expected --output=-")
		os.Exit(2)
	}

	if os.Getenv("SPECTRE_TEST_HANG") == "1" {
		// Block until the parent kills us via ctx cancellation.
		// time.Sleep with a long duration is interruptible by SIGKILL
		// so the runner's exec.CommandContext path can reap us.
		time.Sleep(1 * time.Hour)
		return
	}

	if os.Getenv("SPECTRE_TEST_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "engine failure")
		os.Exit(1)
	}

	if os.Getenv("SPECTRE_TEST_LONG_ROW") == "1" {
		big := strings.Repeat("x", 100*1024) // 100 KiB string field
		fmt.Printf(`{"text":"%s"}`+"\n", big)
		return
	}

	if os.Getenv("SPECTRE_TEST_BLANK_LINES") == "1" {
		fmt.Println(`{"i":1}`)
		fmt.Println("")
		fmt.Println(`{"i":2}`)
		return
	}

	rows := 1
	if v := os.Getenv("SPECTRE_TEST_ROWS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			rows = n
		}
	}
	for i := 0; i < rows; i++ {
		fmt.Printf(`{"i":%d}`+"\n", i)
	}
}
