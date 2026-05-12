// SPDX-License-Identifier: Apache-2.0

package state

import "time"

// CooldownDuration maps a failure kind to a cooldown duration.
// Mirrors the FailureKind contract in
// `proto/spectre/proxy/v1alpha1/proxy.proto`:
//
//	BANNED        long cooldown (per-domain)
//	CAPTCHA       moderate cooldown (per-domain)
//	TIMEOUT       short cooldown (proxy-wide)
//	BAD_RESPONSE  short cooldown + investigate
//
// The durations are conservative defaults — tunable per
// deployment via env or future per-tenant policy.
func CooldownDuration(kind string) time.Duration {
	switch kind {
	case "banned":
		return 1 * time.Hour
	case "captcha":
		return 30 * time.Minute
	case "timeout":
		return 5 * time.Minute
	case "bad_response":
		return 5 * time.Minute
	default:
		// Unknown / unspecified — short cooldown so the proxy
		// re-enters rotation quickly without ignoring the signal
		// entirely.
		return 1 * time.Minute
	}
}
