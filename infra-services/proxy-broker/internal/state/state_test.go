// SPDX-License-Identifier: Apache-2.0

package state

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestClient spins up an in-process Redis via miniredis and
// returns a `Client` wired to it. The miniredis instance is
// closed via `t.Cleanup`.
func newTestClient(t *testing.T) *Client {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return New(rdb)
}

func TestCooldown_NotSet_ReturnsZero(t *testing.T) {
	c := newTestClient(t)
	d, err := c.IsInCooldown(context.Background(), "p1")
	if err != nil {
		t.Fatalf("IsInCooldown: %v", err)
	}
	if d != 0 {
		t.Errorf("expected 0 cooldown for unknown proxy, got %v", d)
	}
}

func TestCooldown_Set_ReturnsRemaining(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	if err := c.SetCooldown(ctx, "p1", "banned", 30*time.Minute); err != nil {
		t.Fatalf("SetCooldown: %v", err)
	}
	d, err := c.IsInCooldown(ctx, "p1")
	if err != nil {
		t.Fatalf("IsInCooldown: %v", err)
	}
	if d <= 0 || d > 30*time.Minute {
		t.Errorf("expected positive cooldown <= 30m, got %v", d)
	}
}

func TestSetCooldown_RejectsNonPositive(t *testing.T) {
	c := newTestClient(t)
	if err := c.SetCooldown(context.Background(), "p1", "banned", 0); err == nil {
		t.Error("expected error on zero duration")
	}
}

func TestBanTracking_RecordAndQuery(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	now := time.Now()

	if err := c.RecordBan(ctx, "stub", "example.com", "p1", now); err != nil {
		t.Fatalf("RecordBan p1: %v", err)
	}
	if err := c.RecordBan(ctx, "stub", "example.com", "p2", now); err != nil {
		t.Fatalf("RecordBan p2: %v", err)
	}

	bans, err := c.BannedProxiesFor(ctx, "stub", "example.com", 24*time.Hour)
	if err != nil {
		t.Fatalf("BannedProxiesFor: %v", err)
	}
	if len(bans) != 2 {
		t.Errorf("expected 2 banned proxies, got %d (%v)", len(bans), bans)
	}
}

func TestBanTracking_PrunesOlderThanWindow(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	old := time.Now().Add(-2 * time.Hour)
	recent := time.Now()

	if err := c.RecordBan(ctx, "stub", "example.com", "p_old", old); err != nil {
		t.Fatalf("RecordBan old: %v", err)
	}
	if err := c.RecordBan(ctx, "stub", "example.com", "p_recent", recent); err != nil {
		t.Fatalf("RecordBan recent: %v", err)
	}

	bans, err := c.BannedProxiesFor(ctx, "stub", "example.com", 1*time.Hour)
	if err != nil {
		t.Fatalf("BannedProxiesFor: %v", err)
	}
	if len(bans) != 1 || bans[0] != "p_recent" {
		t.Errorf("expected only p_recent in 1h window, got %v", bans)
	}
}

func TestHealthScore_UnknownIsOptimistic(t *testing.T) {
	c := newTestClient(t)
	score, err := c.HealthScore(context.Background(), "fresh-proxy")
	if err != nil {
		t.Fatalf("HealthScore: %v", err)
	}
	if score != 1.0 {
		t.Errorf("expected optimistic 1.0 for unknown proxy, got %v", score)
	}
}

func TestHealthScore_TracksRatio(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	for i := 0; i < 8; i++ {
		if err := c.RecordSuccess(ctx, "p1"); err != nil {
			t.Fatalf("RecordSuccess %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := c.RecordFailure(ctx, "p1"); err != nil {
			t.Fatalf("RecordFailure %d: %v", i, err)
		}
	}

	score, err := c.HealthScore(ctx, "p1")
	if err != nil {
		t.Fatalf("HealthScore: %v", err)
	}
	// 8/10 = 0.8 exactly.
	if score < 0.79 || score > 0.81 {
		t.Errorf("expected score ~0.8 for 8s/2f, got %v", score)
	}
}

func TestCooldownDuration_MapsKindsToDurations(t *testing.T) {
	cases := map[string]time.Duration{
		"banned":         time.Hour,
		"captcha":        30 * time.Minute,
		"timeout":        5 * time.Minute,
		"bad_response":   5 * time.Minute,
		"unspecified":    time.Minute,
		"unknown-future": time.Minute,
	}
	for kind, want := range cases {
		got := CooldownDuration(kind)
		if got != want {
			t.Errorf("CooldownDuration(%q) = %v, want %v", kind, got, want)
		}
	}
}

func TestPing_OK(t *testing.T) {
	c := newTestClient(t)
	if err := c.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}
