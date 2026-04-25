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

	driverv1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/driver/v1alpha1"
)

const (
	binaryName = "spectre-controller"
	version    = "0.1.0-alpha.0"
)

// protocolVersion is sourced from the generated protobuf package path
// rather than from a literal so the binary, the engine, and the
// drivers cannot drift out of sync. See ADR-0007.
var protocolVersion = string(driverv1alpha1.File_spectre_driver_v1alpha1_driver_proto.Package())

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
