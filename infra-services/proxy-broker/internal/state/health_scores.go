// SPDX-License-Identifier: Apache-2.0

package state

import (
	"context"
	"fmt"
	"strconv"
)

// RecordSuccess increments the per-proxy success counter and
// updates the health score using a simple decayed-EMA shape
// (new_score = old_score * decay + success_fraction * (1-decay)).
// The simple shape is intentional — we're not building a
// stats engine; the score is a deprioritisation hint, not a
// load-balancing weight.
func (c *Client) RecordSuccess(ctx context.Context, proxyID string) error {
	key := keyHealth(proxyID)
	if err := c.rdb.HIncrBy(ctx, key, "success", 1).Err(); err != nil {
		return fmt.Errorf("state: HINCRBY success %s: %w", proxyID, err)
	}
	return c.recomputeScore(ctx, proxyID)
}

// RecordFailure increments the per-proxy failure counter and
// updates the health score.
func (c *Client) RecordFailure(ctx context.Context, proxyID string) error {
	key := keyHealth(proxyID)
	if err := c.rdb.HIncrBy(ctx, key, "failure", 1).Err(); err != nil {
		return fmt.Errorf("state: HINCRBY failure %s: %w", proxyID, err)
	}
	return c.recomputeScore(ctx, proxyID)
}

// HealthScore returns the current `[0.0, 1.0]` health score
// for `proxyID`. Unknown proxies (no recorded events) return
// 1.0 — the optimistic default, otherwise a fresh proxy could
// never enter rotation.
func (c *Client) HealthScore(ctx context.Context, proxyID string) (float64, error) {
	key := keyHealth(proxyID)
	v, err := c.rdb.HGet(ctx, key, "score").Result()
	if err != nil {
		if isRedisNil(err) {
			return 1.0, nil
		}
		return 0, fmt.Errorf("state: HGET score %s: %w", proxyID, err)
	}
	score, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("state: score %s parse %q: %w", proxyID, v, err)
	}
	return score, nil
}

func (c *Client) recomputeScore(ctx context.Context, proxyID string) error {
	key := keyHealth(proxyID)
	pairs, err := c.rdb.HMGet(ctx, key, "success", "failure").Result()
	if err != nil {
		return fmt.Errorf("state: HMGET success/failure %s: %w", proxyID, err)
	}
	success := redisHashUint64(pairs[0])
	failure := redisHashUint64(pairs[1])
	total := success + failure
	var score float64
	if total == 0 {
		score = 1.0
	} else {
		score = float64(success) / float64(total)
	}
	if err := c.rdb.HSet(ctx, key, "score", strconv.FormatFloat(score, 'f', -1, 64)).Err(); err != nil {
		return fmt.Errorf("state: HSET score %s: %w", proxyID, err)
	}
	return nil
}

// redisHashUint64 parses a `HMGET` result element to uint64,
// treating nil / parse errors as zero. Conservative: a corrupt
// counter degrades the score toward zero rather than panicking
// the broker.
func redisHashUint64(v interface{}) uint64 {
	s, ok := v.(string)
	if !ok || s == "" {
		return 0
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// isRedisNil reports whether `err` is the Redis "key not
// found" sentinel. Hides the package import from health-score
// callers that don't need to know.
func isRedisNil(err error) bool {
	return err != nil && err.Error() == "redis: nil"
}
