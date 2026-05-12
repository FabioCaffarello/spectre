// SPDX-License-Identifier: Apache-2.0

// Package state implements the proxy-broker's Redis-backed
// state per ADR-0039 §3.1. Cooldown tables, ban tracking
// (per-domain sliding windows), per-proxy health scores, and
// lease tracking all live in Redis so the broker is stateless
// across restarts and horizontally scalable.
//
// Key prefixes the broker uses (single Redis logical DB):
//
//	proxy:cooldown:<proxy_id>            string TTL
//	proxy:bans:<provider>:<domain>       sorted set (sliding window)
//	proxy:health:<proxy_id>              hash (success / failure / score)
//	proxy:lease:<lease_id>               hash + TTL
//
// Operations never block — `go-redis/v9` cancellation tokens
// flow through the supplied `context.Context`.
package state

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps a `*redis.Client` and exposes the broker's
// state operations as a focused surface (rather than letting
// callers issue raw Redis commands). Three reasons:
//
//  1. Centralises the key-naming scheme so a future migration
//     to MongoDB (per ADR-0039 §6 evolution path) touches one
//     file, not every call site.
//  2. Lets tests substitute `miniredis` transparently via the
//     standard `redis.Client` interface — no broker-side mock
//     needed.
//  3. Makes the broker's state surface auditable in one read
//     (cooldown, bans, health, leases).
type Client struct {
	rdb *redis.Client
}

// New returns a `Client` wrapping the supplied `*redis.Client`.
// The caller is responsible for pool sizing, TLS, and lifecycle.
func New(rdb *redis.Client) *Client {
	return &Client{rdb: rdb}
}

// Close shuts down the underlying Redis connection pool.
func (c *Client) Close() error {
	return c.rdb.Close()
}

// Ping verifies the Redis connection is live. The broker's
// startup health check calls this before reporting `SERVING`
// via the gRPC health protocol.
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// keyCooldown returns the cooldown key for a proxy. Cooldowns
// are TTL'd strings — the value is the `FailureKind` that
// triggered the cooldown (for observability), the TTL is the
// cooldown duration.
func keyCooldown(proxyID string) string {
	return fmt.Sprintf("proxy:cooldown:%s", proxyID)
}

// keyBans returns the ban-tracking sorted-set key for a
// (provider, target_domain) pair. The set's members are proxy
// IDs; scores are unix-second timestamps of when the ban was
// recorded. The broker prunes entries older than the configured
// ban window on each read (sliding window pattern).
func keyBans(provider, domain string) string {
	return fmt.Sprintf("proxy:bans:%s:%s", provider, domain)
}

// keyHealth returns the per-proxy health-score hash key. The
// hash carries:
//
//	success   uint64  count of successful uses
//	failure   uint64  count of reported failures (any kind)
//	score     float64 current health score in [0.0, 1.0]
func keyHealth(proxyID string) string {
	return fmt.Sprintf("proxy:health:%s", proxyID)
}

// keyLease returns the per-lease hash key. Hash fields:
//
//	proxy_id      string
//	provider      string
//	tenant_id     string
//	target_domain string
//	issued_at     unix-second timestamp
//
// TTL is set to match the lease's expiry; expired keys are
// auto-deleted by Redis.
func keyLease(leaseID string) string {
	return fmt.Sprintf("proxy:lease:%s", leaseID)
}

// IsInCooldown reports whether the proxy is currently in a
// cooldown window. Returns the remaining cooldown duration
// when true; zero when the proxy is not in cooldown.
func (c *Client) IsInCooldown(ctx context.Context, proxyID string) (time.Duration, error) {
	ttl, err := c.rdb.TTL(ctx, keyCooldown(proxyID)).Result()
	if err != nil {
		return 0, fmt.Errorf("state: TTL cooldown %s: %w", proxyID, err)
	}
	// `-2` = key does not exist; `-1` = key exists with no TTL.
	// Both mean "not in cooldown" for our purposes.
	if ttl < 0 {
		return 0, nil
	}
	return ttl, nil
}

// SetCooldown puts a proxy into cooldown for the given
// duration. The stored value is the FailureKind's lowercase
// name (for `redis-cli` observability); the TTL drives the
// cooldown semantics.
func (c *Client) SetCooldown(ctx context.Context, proxyID string, kind string, d time.Duration) error {
	if d <= 0 {
		return fmt.Errorf("state: cooldown duration must be positive, got %v", d)
	}
	if err := c.rdb.Set(ctx, keyCooldown(proxyID), kind, d).Err(); err != nil {
		return fmt.Errorf("state: SET cooldown %s: %w", proxyID, err)
	}
	return nil
}

// LeaseInfo carries the broker-side metadata recorded against
// a lease. The server stores this on Acquire and looks it up
// on Release / ReportFailure so the provider-side Release call
// can locate the right provider client.
type LeaseInfo struct {
	ProxyID      string
	Provider     string
	TenantID     string
	TargetDomain string
	IssuedAt     time.Time
}

// RecordLease persists lease metadata with a TTL matching the
// lease's expiry. Expired keys auto-delete; the server treats
// a missing lease as already-released (idempotent semantics).
func (c *Client) RecordLease(ctx context.Context, leaseID string, info LeaseInfo, ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("state: lease TTL must be positive, got %v", ttl)
	}
	key := keyLease(leaseID)
	if err := c.rdb.HSet(ctx, key, map[string]interface{}{
		"proxy_id":      info.ProxyID,
		"provider":      info.Provider,
		"tenant_id":     info.TenantID,
		"target_domain": info.TargetDomain,
		"issued_at":     info.IssuedAt.Unix(),
	}).Err(); err != nil {
		return fmt.Errorf("state: HSET lease %s: %w", leaseID, err)
	}
	if err := c.rdb.Expire(ctx, key, ttl).Err(); err != nil {
		return fmt.Errorf("state: EXPIRE lease %s: %w", leaseID, err)
	}
	return nil
}

// LookupLease returns the lease metadata for `leaseID`. The
// boolean is false when the lease is unknown / expired (callers
// treat that as already-released).
func (c *Client) LookupLease(ctx context.Context, leaseID string) (LeaseInfo, bool, error) {
	key := keyLease(leaseID)
	m, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return LeaseInfo{}, false, fmt.Errorf("state: HGETALL lease %s: %w", leaseID, err)
	}
	if len(m) == 0 {
		return LeaseInfo{}, false, nil
	}
	issued, _ := parseUnix(m["issued_at"])
	return LeaseInfo{
		ProxyID:      m["proxy_id"],
		Provider:     m["provider"],
		TenantID:     m["tenant_id"],
		TargetDomain: m["target_domain"],
		IssuedAt:     issued,
	}, true, nil
}

// DeleteLease removes the lease record. Idempotent — deleting
// an absent key is a no-op (Redis DEL returns 0).
func (c *Client) DeleteLease(ctx context.Context, leaseID string) error {
	if err := c.rdb.Del(ctx, keyLease(leaseID)).Err(); err != nil {
		return fmt.Errorf("state: DEL lease %s: %w", leaseID, err)
	}
	return nil
}

func parseUnix(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	var n int64
	_, err := fmt.Sscan(s, &n)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(n, 0), nil
}
