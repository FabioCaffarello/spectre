// SPDX-License-Identifier: Apache-2.0

import { describe, expect, test } from "vitest";

import {
  CA_PATH_ENV,
  CERT_PATH_ENV,
  KEY_PATH_ENV,
  TlsConfigError,
  detectMode,
} from "./tls.js";

function fakeEnv(
  env: Record<string, string>,
): (k: string) => string | undefined {
  return (k) => env[k];
}

describe("detectMode", () => {
  test("all unset → plaintext", () => {
    expect(detectMode(fakeEnv({})).mode).toBe("plaintext");
  });

  test("all set → mutual + paths captured", () => {
    const cfg = detectMode(
      fakeEnv({
        [CERT_PATH_ENV]: "/etc/spectre/tls/tls.crt",
        [KEY_PATH_ENV]: "/etc/spectre/tls/tls.key",
        [CA_PATH_ENV]: "/etc/spectre/tls/ca.crt",
      }),
    );
    expect(cfg.mode).toBe("mutual");
    expect(cfg.certPath).toBe("/etc/spectre/tls/tls.crt");
    expect(cfg.keyPath).toBe("/etc/spectre/tls/tls.key");
    expect(cfg.caPath).toBe("/etc/spectre/tls/ca.crt");
  });

  test("partial → throws with unset var name", () => {
    expect(() =>
      detectMode(
        fakeEnv({
          [CERT_PATH_ENV]: "/etc/spectre/tls/tls.crt",
          [KEY_PATH_ENV]: "/etc/spectre/tls/tls.key",
          // CA unset
        }),
      ),
    ).toThrowError(TlsConfigError);
  });

  test("empty strings treated as unset", () => {
    const cfg = detectMode(
      fakeEnv({
        [CERT_PATH_ENV]: "",
        [KEY_PATH_ENV]: "",
        [CA_PATH_ENV]: "",
      }),
    );
    expect(cfg.mode).toBe("plaintext");
  });
});
