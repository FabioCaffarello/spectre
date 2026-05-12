// SPDX-License-Identifier: Apache-2.0

package state

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultBanWindow is the rolling window over which the broker
// considers historical bans when deciding whether to deprioritise
// a proxy on the next `Acquire`. Older bans are pruned out of the
// sorted set on each read.
const DefaultBanWindow = 24 * time.Hour

// RecordBan records that `proxyID` was banned by `domain` while
// served by `provider`. Stores the current timestamp as the
// sorted-set score. Idempotent re-records overwrite the prior
// timestamp (most recent wins).
func (c *Client) RecordBan(ctx context.Context, provider, domain, proxyID string, at time.Time) error {
	key := keyBans(provider, domain)
	score := float64(at.Unix())
	if err := c.rdb.ZAdd(ctx, key, redis.Z{Score: score, Member: proxyID}).Err(); err != nil {
		return fmt.Errorf("state: ZADD ban %s/%s/%s: %w", provider, domain, proxyID, err)
	}
	// Keep the sorted set bounded by pruning anything older than
	// the window. Cheap; runs on every record.
	cutoff := strconv.FormatInt(at.Add(-DefaultBanWindow).Unix(), 10)
	if err := c.rdb.ZRemRangeByScore(ctx, key, "-inf", "("+cutoff).Err(); err != nil {
		return fmt.Errorf("state: ZREMRANGEBYSCORE ban %s/%s: %w", provider, domain, err)
	}
	return nil
}

// BannedProxiesFor returns the set of proxy IDs the broker
// should avoid for `(provider, domain)`. Reads the sorted set
// after pruning entries older than `window`. Returns an empty
// slice when no bans are recorded.
func (c *Client) BannedProxiesFor(ctx context.Context, provider, domain string, window time.Duration) ([]string, error) {
	if window <= 0 {
		window = DefaultBanWindow
	}
	key := keyBans(provider, domain)
	cutoff := strconv.FormatInt(time.Now().Add(-window).Unix(), 10)
	// Prune first, then range.
	if err := c.rdb.ZRemRangeByScore(ctx, key, "-inf", "("+cutoff).Err(); err != nil {
		return nil, fmt.Errorf("state: ZREMRANGEBYSCORE banned %s/%s: %w", provider, domain, err)
	}
	members, err := c.rdb.ZRange(ctx, key, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("state: ZRANGE banned %s/%s: %w", provider, domain, err)
	}
	return members, nil
}

// BanCount returns the number of distinct proxy IDs currently
// in the (provider, domain) ban set after pruning. Useful for
// per-domain rate-limit dashboards.
func (c *Client) BanCount(ctx context.Context, provider, domain string, window time.Duration) (int64, error) {
	if window <= 0 {
		window = DefaultBanWindow
	}
	key := keyBans(provider, domain)
	cutoff := strconv.FormatInt(time.Now().Add(-window).Unix(), 10)
	if err := c.rdb.ZRemRangeByScore(ctx, key, "-inf", "("+cutoff).Err(); err != nil {
		return 0, fmt.Errorf("state: ZREMRANGEBYSCORE bancount %s/%s: %w", provider, domain, err)
	}
	n, err := c.rdb.ZCard(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("state: ZCARD bancount %s/%s: %w", provider, domain, err)
	}
	return n, nil
}
