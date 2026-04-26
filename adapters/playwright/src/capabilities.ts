// SPDX-License-Identifier: Apache-2.0
//
// Capabilities declared by the Playwright adapter at handshake time.
//
// Each capability lands once its RPC and the conformance tests for
// it ship together. PR3 declared none; PR4 adds `navigation` (with
// the `Navigate` RPC) and `js_execution` (the underlying runtime
// supports it; future RPCs will exercise it). The exported value
// MUST stay in lockstep with `driver.yaml`'s `capabilities:` block —
// the conformance suite asserts byte-for-byte equality at runtime.

export const CAPABILITY_NAMES: readonly string[] = Object.freeze([
  "navigation",
  "js_execution",
]);

export const DRIVER_VERSION = "0.1.0-alpha.0";
