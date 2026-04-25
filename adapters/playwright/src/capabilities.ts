// SPDX-License-Identifier: Apache-2.0
//
// Capabilities declared by the Playwright adapter at handshake time.
//
// PR3 implements only `Initialize`, so no capability is yet honoured.
// The list grows as RPCs come online: `navigation` lands with the
// `Navigate` implementation, and so on. The exported value MUST stay
// in lockstep with `driver.yaml`'s `capabilities:` block — the
// conformance suite asserts equality between the two at runtime.

export const CAPABILITY_NAMES: readonly string[] = Object.freeze([]);

export const DRIVER_VERSION = "0.1.0-alpha.0";
