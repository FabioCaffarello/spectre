// SPDX-License-Identifier: Apache-2.0
//
// Capabilities declared by the Playwright adapter at handshake time.
//
// Each capability lands once its RPC and the conformance tests for
// it ship together. PR3 declared none; PR4 added `navigation` and
// `js_execution`; PR5 expands the list to eleven names covering the
// query-* and extract-* surface that `Query` and `Extract` now
// implement. The exported value MUST stay in lockstep with
// `driver.yaml`'s `capabilities:` block — the conformance suite
// asserts byte-for-byte equality at runtime, including order.
//
// The capability mechanism splits into two roles (see ADR-0010,
// decision 3):
//
//   - Descriptive declarations describe what the adapter can do so
//     a future engine can plan whether a job will succeed against
//     this driver. They do not gate behaviour at runtime.
//   - Gating capabilities bind a specific runtime path to a
//     declared capability. v1alpha1 has one gating capability:
//     `js_execution`, which gates `MODE_EVAL` extracts.
//
// `assertCapabilityCoherence` enforces invariants between
// capabilities that imply each other (e.g. `extract_eval` requires
// `js_execution` because the runtime gate would otherwise reject
// every `MODE_EVAL` call). Violations throw at module load so a
// misconfigured constant fails the build / unit tests at startup,
// not as a confusing first-RPC error.
//
// Order: alphabetical by capability name. The conformance suite
// asserts list-equality (not set-equality) between this constant
// and the manifest, so a deterministic order is required.
// Alphabetical was chosen because it has a single canonical form
// and survives editor reordering without churn.

/** Read text content (Playwright `Locator.textContent()`). */
export const EXTRACT_ATTRIBUTE = "extract_attribute";
/** Evaluate a JS expression in the page context (`MODE_EVAL`).
 * Gated at runtime on the presence of `js_execution`. */
export const EXTRACT_EVAL = "extract_eval";
/** Read inner / outer HTML. */
export const EXTRACT_HTML = "extract_html";
/** Read text content / innerText. */
export const EXTRACT_TEXT = "extract_text";
/** Driver can run JS in the page context (gating capability). */
export const JS_EXECUTION = "js_execution";
/** Driver can fetch a URL via the underlying runtime. */
export const NAVIGATION = "navigation";
/** Resolve elements by attribute presence or `name=value`. */
export const QUERY_ATTRIBUTE = "query_attribute";
/** Resolve elements by CSS selector. */
export const QUERY_CSS = "query_css";
/** Resolve elements by visible text (substring). */
export const QUERY_TEXT = "query_text";
/** Resolve elements by XPath. */
export const QUERY_XPATH = "query_xpath";

export const CAPABILITY_NAMES: readonly string[] = Object.freeze([
  EXTRACT_ATTRIBUTE,
  EXTRACT_EVAL,
  EXTRACT_HTML,
  EXTRACT_TEXT,
  JS_EXECUTION,
  NAVIGATION,
  QUERY_ATTRIBUTE,
  QUERY_CSS,
  QUERY_TEXT,
  QUERY_XPATH,
]);

export const DRIVER_VERSION = "0.1.0-alpha.0";

/** Returns true if the declared list contains the named capability. */
export const hasCapability = (
  declared: readonly string[],
  name: string,
): boolean => declared.includes(name);

/** Throws if the declared list violates a coherence invariant.
 * Currently:
 *   - `extract_eval` declared without `js_execution` is a
 *     contradiction (the runtime gate would reject every
 *     `MODE_EVAL` call).
 * Extensible: add rows as new capabilities imply each other. */
export const assertCapabilityCoherence = (
  declared: readonly string[],
): void => {
  if (declared.includes(EXTRACT_EVAL) && !declared.includes(JS_EXECUTION)) {
    throw new Error(
      "capability coherence violation: extract_eval requires js_execution",
    );
  }
};

assertCapabilityCoherence(CAPABILITY_NAMES);
