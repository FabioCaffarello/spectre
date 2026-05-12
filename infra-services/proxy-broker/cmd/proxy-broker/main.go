// SPDX-License-Identifier: Apache-2.0

// Command proxy-broker is the Spectre proxy-broker infra-service
// — slot 1 of the ADR-0036 §3.1 catalog; first inhabitant of
// `infra-services/` per ADR-0028 §4.1.
//
// The service centralises proxy acquisition, cooldown tracking,
// budget accounting, and provider-agnostic vendor routing across
// every Spectre engine instance. Adapter processes do NOT consume
// the broker directly — the engine acquires a lease per session
// and forwards the resulting URL via the existing
// `spectre.driver.v1alpha1.Driver.Initialize` SessionConfig.
//
// W5.1 cluster B.5 (binary wire-up) replaces this stub with the
// full main loop (env config + TLS detection + OTel + slog +
// gRPC server registration + signal handling). This stub keeps
// `go build ./...` green while B.2 – B.4 author the supporting
// packages.
package main

func main() {
	// Intentionally empty; cluster B.5 wires the full main loop.
	// See `internal/server/`, `internal/providers/`, `internal/state/`
	// for the components this binary will compose.
}
