// SPDX-License-Identifier: Apache-2.0

// Package redis wraps github.com/redis/go-redis/v9 with the four
// session-shaped operations the curl-impersonate SessionManager
// needs (set, get, delete, ping). R4.3 / ADR-0023 §4 + §5 places
// session metadata under ``session:<adapter>:<session_id>``; the
// JSON document includes ``adapter_instance_id``, the per-process
// UUID the §5 R4.3 addendum specifies as the restart-invalidation
// mechanism.
//
// The wrapper is intentionally thin: it owns one
// ``*redis.Client`` for the adapter's lifetime and exposes
// methods over typed inputs/outputs. Reconnection on transient
// failures is handled by the underlying client's connection pool;
// failed RPCs surface as connection errors that the gRPC server
// maps to ``codes.Unavailable``.
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// AdapterName is fixed: this adapter writes session metadata
// under the ``session:curl-impersonate:<session_id>`` key.
// Matches ``jobs.driver`` in Postgres and the future Kafka
// header value.
const AdapterName = "curl-impersonate"

// SessionTTL is the per-key idle expiration. ADR-0023 §4
// commits to 1 hour; the value is refreshed on every read and
// write.
const SessionTTL = time.Hour

// SessionMetadata is the JSON document stored under each session
// key. Mirrors the Playwright (TS) and SeleniumBase (Python)
// counterparts byte-for-byte so the conformance harness can
// decode any adapter's session document with the same keys.
type SessionMetadata struct {
	SessionID         string         `json:"session_id"`
	Adapter           string         `json:"adapter"`
	AdapterInstanceID string         `json:"adapter_instance_id"`
	CreatedAt         string         `json:"created_at"`
	LastActiveAt      string         `json:"last_active_at"`
	Metadata          map[string]any `json:"metadata"`
}

// Client wraps go-redis with a session-scoped API. The adapter
// constructs one Client at startup and shares it across the
// lifetime of the process.
type Client struct {
	rdb *goredis.Client
}

// FromURL constructs a Client from a redis:// URL. Returns an
// error if parsing fails. The returned Client is lazy — the
// first Ping triggers the actual dial.
func FromURL(url string) (*Client, error) {
	opts, err := goredis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url %q: %w", url, err)
	}
	return &Client{rdb: goredis.NewClient(opts)}, nil
}

// NewClient wraps an existing *redis.Client. Used by tests that
// want to plug in a redismock-driven client.
func NewClient(rdb *goredis.Client) *Client {
	return &Client{rdb: rdb}
}

// Ping verifies the connection. Returns an error if Redis is
// unreachable. ADR-0023 §6 makes Redis required at adapter
// startup, so callers exit non-zero if this returns an error.
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

func sessionKey(sessionID string) string {
	return "session:" + AdapterName + ":" + sessionID
}

// SetSession marshals value as JSON and writes it under the
// session key with the configured TTL.
func (c *Client) SetSession(ctx context.Context, sessionID string, value SessionMetadata) error {
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal session metadata: %w", err)
	}
	return c.rdb.Set(ctx, sessionKey(sessionID), body, SessionTTL).Err()
}

// GetSession reads the session metadata. Returns (nil, nil) when
// the key does not exist (TTL-expired or never written) so the
// caller can distinguish "redis is reachable but the session is
// gone" from "redis is unreachable".
func (c *Client) GetSession(ctx context.Context, sessionID string) (*SessionMetadata, error) {
	raw, err := c.rdb.Get(ctx, sessionKey(sessionID)).Bytes()
	if err != nil {
		if err == goredis.Nil {
			return nil, nil
		}
		return nil, err
	}
	var value SessionMetadata
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("unmarshal session metadata: %w", err)
	}
	return &value, nil
}

// DeleteSession removes the session key. A missing key is not an
// error — DEL is a no-op in that case.
func (c *Client) DeleteSession(ctx context.Context, sessionID string) error {
	return c.rdb.Del(ctx, sessionKey(sessionID)).Err()
}

// Close releases the underlying go-redis client and its
// connection pool.
func (c *Client) Close() error {
	return c.rdb.Close()
}
