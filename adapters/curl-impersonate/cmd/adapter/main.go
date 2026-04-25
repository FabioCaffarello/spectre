// SPDX-License-Identifier: Apache-2.0

// Command spectre-curl-impersonate is the Spectre curl-impersonate
// driver adapter.
//
// The adapter wraps the curl-impersonate C library via cgo and
// exposes a gRPC Driver server. It is targeted at HTTP-only flows
// where a full browser is unnecessary but the request fingerprint
// must match a real browser's TLS and HTTP/2 profile.
//
// As of v0.1.0-alpha.0 the binary is a placeholder that prints its
// build identity and exits. The cgo wrapper and gRPC server land in
// Phase 2 of the project roadmap (see docs/roadmap.md).
package main

import (
	"fmt"
	"io"
	"os"
)

const (
	binaryName      = "spectre-curl-impersonate"
	version         = "0.1.0-alpha.0"
	protocolVersion = "spectre.driver.v1alpha1"
)

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out io.Writer) error {
	_, err := fmt.Fprintf(out, "%s %s (driver protocol %s)\n", binaryName, version, protocolVersion)
	return err
}
