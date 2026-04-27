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
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// SubprocessRunner runs a ScrapeJob by shelling out to the spectre
// engine binary. The DSL is materialised to a temp YAML file (the
// engine's `run` subcommand takes a path argument; ADR-0013 §3),
// `--output=-` forces JSONL onto the engine's stdout regardless of
// the YAML's `output.path`, and each non-empty stdout line is
// counted and forwarded to the supplied writer. Engine stderr
// (tracing logs) is wired to os.Stderr so it surfaces alongside
// the controller's own logs in `kubectl logs <operator-pod>`.
//
// See ADR-0019 §3 (subprocess-in-pod execution model), §5
// (JobRunner seam), §6 (stdout sink) and ADR-0013 §3 (CLI surface).
type SubprocessRunner struct {
	// EnginePath is the absolute path to the spectre engine binary.
	// The operator image bundles it at /usr/local/bin/spectre; local
	// development overrides via cmd/main.go's --engine-binary flag.
	EnginePath string

	// AdaptersPath, when non-empty, is forwarded as --adapters-path
	// to the engine, overriding the engine's built-in default and
	// any SPECTRE_ADAPTERS_PATH resolution. The operator image
	// leaves this empty (the engine resolves its own default);
	// local `make run` sets it to the workspace's adapters/ dir.
	AdaptersPath string
}

// jsonlScannerCap raises bufio.Scanner's max line length to 16 MiB
// so JSONL rows that carry large string fields (HTML fragments,
// Screenshot payloads as base64) do not surface as
// bufio.ErrTooLong. The default 64 KiB cap is too tight for real
// extraction workloads.
const jsonlScannerCap = 16 * 1024 * 1024

// Run implements JobRunner.
func (r *SubprocessRunner) Run(ctx context.Context, jobDSL string, writer io.Writer) (int64, error) {
	if r.EnginePath == "" {
		return 0, fmt.Errorf("subprocess runner: engine binary path is empty")
	}

	jobFile, err := os.CreateTemp("", "spectre-scrapejob-*.yaml")
	if err != nil {
		return 0, fmt.Errorf("subprocess runner: create temp job file: %w", err)
	}
	jobPath := jobFile.Name()
	defer func() { _ = os.Remove(jobPath) }()
	if _, err := jobFile.WriteString(jobDSL); err != nil {
		_ = jobFile.Close()
		return 0, fmt.Errorf("subprocess runner: write job dsl: %w", err)
	}
	if err := jobFile.Close(); err != nil {
		return 0, fmt.Errorf("subprocess runner: close job file: %w", err)
	}

	args := []string{"run", jobPath, "--output=-"}
	if r.AdaptersPath != "" {
		args = append(args, "--adapters-path="+r.AdaptersPath)
	}

	cmd := exec.CommandContext(ctx, r.EnginePath, args...)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("subprocess runner: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("subprocess runner: start %s: %w", r.EnginePath, err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), jsonlScannerCap)

	var rows int64
	var writeErr error
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		rows++
		if _, werr := fmt.Fprintln(writer, line); werr != nil {
			writeErr = werr
			break
		}
	}
	scanErr := scanner.Err()

	// Drain any remaining stdout so the engine never blocks on a
	// full pipe after we stop reading. Cheap on the happy path
	// (Scan already returned false), defensive when the writer
	// errored out mid-stream.
	if writeErr != nil {
		_, _ = io.Copy(io.Discard, stdout)
	}

	waitErr := cmd.Wait()

	if ctxErr := ctx.Err(); ctxErr != nil {
		return rows, ctxErr
	}
	if waitErr != nil {
		return rows, fmt.Errorf("subprocess runner: %s exited: %w", r.EnginePath, waitErr)
	}
	if writeErr != nil {
		return rows, fmt.Errorf("subprocess runner: write row: %w", writeErr)
	}
	if scanErr != nil {
		return rows, fmt.Errorf("subprocess runner: read stdout: %w", scanErr)
	}

	return rows, nil
}
