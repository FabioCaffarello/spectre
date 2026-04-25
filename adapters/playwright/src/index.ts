// SPDX-License-Identifier: Apache-2.0
//
// Spectre Playwright driver adapter.
//
// As of v0.1.0-alpha.0 this adapter is a placeholder that prints its
// build identity and exits. The Driver Protocol implementation lands
// in Phase 1 of the project roadmap.

export const PROTOCOL_VERSION = "spectre.driver.v1alpha1";
export const ADAPTER_VERSION = "0.1.0-alpha.0";

export function identity(): string {
  return `spectre-playwright ${ADAPTER_VERSION} (driver protocol ${PROTOCOL_VERSION})`;
}
