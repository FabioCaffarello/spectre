// SPDX-License-Identifier: Apache-2.0
//
// W3.2 Cluster A tests for `./telemetry`.

import { describe, expect, it } from "vitest";
import {
  DEFAULT_METRICS_PORT,
  KIND,
  METER_NAME,
  SERVICE_NAME,
  resolveMetricsPort,
} from "./telemetry.js";

describe("constants match canonical values", () => {
  it("snake-cases the metric `{kind}` label per ADR-0031 §3.4", () => {
    expect(KIND).toBe("playwright");
  });

  it("service name matches chart service discovery", () => {
    expect(SERVICE_NAME).toBe("spectre-playwright");
    expect(METER_NAME).toBe("spectre-playwright");
  });

  it("default metrics port matches ADR-0031 §3.3", () => {
    expect(DEFAULT_METRICS_PORT).toBe(9090);
  });
});

describe("resolveMetricsPort", () => {
  it("falls back to 9090 when SPECTRE_METRICS_PORT is unset", () => {
    expect(resolveMetricsPort({})).toBe(9090);
    expect(resolveMetricsPort({ SPECTRE_METRICS_PORT: "" })).toBe(9090);
  });

  it("honours a valid integer override", () => {
    expect(resolveMetricsPort({ SPECTRE_METRICS_PORT: "9091" })).toBe(9091);
    expect(resolveMetricsPort({ SPECTRE_METRICS_PORT: "0" })).toBe(0);
    expect(resolveMetricsPort({ SPECTRE_METRICS_PORT: "65535" })).toBe(65535);
  });

  it("rejects non-integer values", () => {
    expect(() => resolveMetricsPort({ SPECTRE_METRICS_PORT: "abc" })).toThrow(
      /SPECTRE_METRICS_PORT must be a port number/,
    );
    expect(() =>
      resolveMetricsPort({ SPECTRE_METRICS_PORT: "9090.5" }),
    ).toThrow(/SPECTRE_METRICS_PORT must be a port number/);
  });

  it("rejects out-of-range values", () => {
    expect(() => resolveMetricsPort({ SPECTRE_METRICS_PORT: "-1" })).toThrow(
      /between 0 and 65535/,
    );
    expect(() => resolveMetricsPort({ SPECTRE_METRICS_PORT: "65536" })).toThrow(
      /between 0 and 65535/,
    );
  });
});
