// SPDX-License-Identifier: Apache-2.0

// Command spectre-controller is the Spectre control-plane binary.
//
// The controller is the project's Kubernetes-native scheduler. It
// receives job specifications, materialises engine workers, tracks
// state, and applies retry and quota policies.
//
// As of v0.1.0-alpha.0 the binary is a placeholder that prints its
// build identity and exits. Substantive functionality lands in Phase
// 3 of the project roadmap (see docs/roadmap.md).
package main

import (
	"fmt"
	"io"
	"os"
)

const (
	binaryName      = "spectre-controller"
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
