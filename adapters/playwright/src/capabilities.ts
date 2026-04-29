// SPDX-License-Identifier: Apache-2.0
//
// Capabilities declared by the Playwright adapter at handshake time.
//
// Each capability lands once its RPC and the conformance tests for
// it ship together. The declared set is `navigation`, `js_execution`,
// the four `query_*` names, the four `extract_*` names, and the
// three `screenshot_*` names — thirteen entries total covering the
// full v1alpha1 unary surface. The exported value MUST stay in
// lockstep with `driver.yaml`'s `capabilities:` block — the
// conformance suite asserts byte-for-byte equality at runtime,
// including order.
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
/** Capture a screenshot clipped to a referenced ElementRef
 * (`SCREENSHOT_SCOPE_ELEMENT`). */
export const SCREENSHOT_ELEMENT = "screenshot_element";
/** Capture the entire scrollable page
 * (`SCREENSHOT_SCOPE_FULL_PAGE`). */
export const SCREENSHOT_FULL_PAGE = "screenshot_full_page";
/** Capture the visible viewport (`SCREENSHOT_SCOPE_VIEWPORT`). */
export const SCREENSHOT_VIEWPORT = "screenshot_viewport";

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
  SCREENSHOT_ELEMENT,
  SCREENSHOT_FULL_PAGE,
  SCREENSHOT_VIEWPORT,
]);

export const DRIVER_VERSION = "0.1.0-alpha.0";

/** Returns true if the declared list contains the named capability. */
export const hasCapability = (
  declared: readonly string[],
  name: string,
): boolean => declared.includes(name);

/** The single runtime gate v1alpha1 enforces: a `Field` whose
 * `mode` is `MODE_EVAL` (the integer constant is `6` per
 * extraction.proto's `Field.Mode`) requires `js_execution` in the
 * declared capability list. Returns the missing capability name if
 * the gate would reject the request, or null if it would pass.
 *
 * Pure function, so the gate is unit-testable without standing up
 * a real adapter or fake SessionManager. The Extract handler
 * imports it directly. See ADR-0010, decision 3. */
export const MODE_EVAL = 6;

export const missingCapabilityForMode = (
  mode: number,
  declared: readonly string[],
): string | null => {
  if (mode === MODE_EVAL && !hasCapability(declared, JS_EXECUTION)) {
    return JS_EXECUTION;
  }
  return null;
};

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
