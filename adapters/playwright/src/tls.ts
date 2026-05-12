// SPDX-License-Identifier: Apache-2.0

/**
 * Server-side mTLS for the Playwright adapter (ADR-0032 §4.2, W3.4).
 *
 * Symmetric to the SeleniumBase Python module + the curl-impersonate
 * Go package + the engine Rust module from W3.3/W3.4: same three
 * env vars (`SPECTRE_TLS_{CERT,KEY,CA}_PATH`), same Mode
 * classification (Plaintext / Mutual / partial → fail-fast).
 *
 * ADR-0032 §5.1 (TypeScript row) accepts restart-on-rotation:
 * Node's `http2.createSecureServer` consumes static PEM material;
 * cert-manager's rotation triggers a Pod restart via the chart's
 * annotation pattern. The 30-day rotation lead window accommodates
 * Pod restarts at the rotation cadence.
 */

import * as fs from "node:fs";

export const CERT_PATH_ENV = "SPECTRE_TLS_CERT_PATH";
export const KEY_PATH_ENV = "SPECTRE_TLS_KEY_PATH";
export const CA_PATH_ENV = "SPECTRE_TLS_CA_PATH";

export type TlsMode = "plaintext" | "mutual";

export interface TlsConfig {
  mode: TlsMode;
  certPath?: string;
  keyPath?: string;
  caPath?: string;
}

export interface TlsMaterial {
  cert: Buffer;
  key: Buffer;
  ca: Buffer;
}

export class TlsConfigError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "TlsConfigError";
  }
}

/**
 * Resolve the TLS posture from process env.
 *
 * All three vars set → `mutual`; all three unset (or empty) →
 * `plaintext`; partial → throws {@link TlsConfigError}.
 */
export function detectMode(
  env: (key: string) => string | undefined = (k) => process.env[k],
): TlsConfig {
  const cert = env(CERT_PATH_ENV) || undefined;
  const key = env(KEY_PATH_ENV) || undefined;
  const ca = env(CA_PATH_ENV) || undefined;

  const setCount = [cert, key, ca].filter((v) => v && v.length > 0).length;
  if (setCount === 0) {
    return { mode: "plaintext" };
  }
  if (setCount === 3) {
    return {
      mode: "mutual",
      certPath: cert,
      keyPath: key,
      caPath: ca,
    };
  }

  const setVars: string[] = [];
  const unsetVars: string[] = [];
  for (const [name, value] of [
    [CERT_PATH_ENV, cert],
    [KEY_PATH_ENV, key],
    [CA_PATH_ENV, ca],
  ] as const) {
    if (value) {
      setVars.push(name);
    } else {
      unsetVars.push(name);
    }
  }
  throw new TlsConfigError(
    `tls: partial env config — [${setVars.join(", ")}] set, ` +
      `[${unsetVars.join(", ")}] unset; all three of ${CERT_PATH_ENV}, ` +
      `${KEY_PATH_ENV}, ${CA_PATH_ENV} must be set together (mTLS) or ` +
      "all unset (plaintext)",
  );
}

/**
 * Read the three PEM files for a Mutual-mode config. Throws if
 * any file cannot be read (the error message identifies which).
 *
 * Plaintext mode never calls this — callers gate on `config.mode`
 * first.
 */
export function loadTlsMaterial(config: TlsConfig): TlsMaterial {
  if (config.mode !== "mutual") {
    throw new TlsConfigError(
      "loadTlsMaterial called for non-mutual mode; caller should gate on mode first",
    );
  }
  // The mode === "mutual" branch guarantees these are set.
  const certPath = config.certPath!;
  const keyPath = config.keyPath!;
  const caPath = config.caPath!;
  try {
    return {
      cert: fs.readFileSync(certPath),
      key: fs.readFileSync(keyPath),
      ca: fs.readFileSync(caPath),
    };
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    throw new TlsConfigError(
      `tls: failed to read PEM material (cert=${certPath}, key=${keyPath}, ca=${caPath}): ${message}`,
    );
  }
}
