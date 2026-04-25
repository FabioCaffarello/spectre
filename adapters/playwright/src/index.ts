// SPDX-License-Identifier: Apache-2.0
//
// Spectre Playwright driver adapter.
//
// As of v0.1.0-alpha.0 this adapter is a placeholder that prints its
// build identity and exits. The Driver Protocol implementation lands
// in Phase 1 of the project roadmap.

import { file_spectre_driver_v1alpha1_driver } from "./proto/spectre/driver/v1alpha1/driver_pb.js";

// PROTOCOL_VERSION is sourced from the generated file descriptor's
// package directive. The value is `"spectre.driver.v1alpha1"` —
// generated, not duplicated. See ADR-0007.
export const PROTOCOL_VERSION: string =
  file_spectre_driver_v1alpha1_driver.proto.package;
export const ADAPTER_VERSION = "0.1.0-alpha.0";

export function identity(): string {
  return `spectre-playwright ${ADAPTER_VERSION} (driver protocol ${PROTOCOL_VERSION})`;
}
