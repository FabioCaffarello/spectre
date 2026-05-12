// SPDX-License-Identifier: Apache-2.0

//! Per-scope in-memory cache for stable values consumed by the
//! engine's service clients.
//!
//! Per [ADR-0037 §4.2](../../../docs/adr/0037-engine-as-orchestrator.md),
//! certain values are stable enough to cache at a defined
//! scope and reuse across steps without re-asking the upstream
//! service:
//!
//! | Scope    | Example value           | Lifetime                     |
//! |----------|-------------------------|------------------------------|
//! | session  | sticky proxy lease      | session end                  |
//! | session  | browser fingerprint     | session end                  |
//! | job      | job schema              | job end                      |
//! | target   | driver capabilities     | TTL (default 1h)             |
//! | tenant   | credential snapshot     | invalidated on rotate event  |
//!
//! W5.1 consumes this for **sticky proxy leases per session**
//! (`services::proxy::EngineProxyClient`). The cache is
//! `Cache<String, ProxyLease>` keyed by session id; sticky
//! `acquire_for_session` returns the cached lease when present,
//! falls through to the SDK + records the result otherwise.
//!
//! The API is **service-agnostic by design** — the W5.2
//! captcha-solver consumer (`services::captcha`), the W6
//! schema-registry consumer (`services::schema_registry`), and
//! every Wave 5-10 service consumer reuse this exact type with
//! a different `<K, V>`. No per-service variants; the cache
//! doesn't know what it's caching.
//!
//! ### Eviction
//!
//! When `with_ttl(...)` is set, expired entries are evicted
//! lazily on the next `get` for that key. No background sweeper
//! — the engine has bounded scope usage (sessions / jobs are
//! short-lived) so cold entries that never see a follow-up
//! access stay in the map until the process restarts. Future
//! improvement: opt-in background sweep via a feature flag.

use std::collections::HashMap;
use std::hash::Hash;
use std::sync::Arc;
use std::time::{Duration, Instant};

use tokio::sync::RwLock;

/// Generic in-memory cache. `K` is the scope key (session id,
/// job id, target domain, tenant id, or any tuple of those);
/// `V` is the cached value type.
///
/// Cheap to `Arc::clone` — the inner state is wrapped in an
/// `Arc<RwLock<HashMap<...>>>` so a shared `Cache` works across
/// tokio tasks. Per-instance state means each service consumer
/// gets its own cache (the proxy consumer's session cache is
/// distinct from the schema cache; cross-scope invalidation is
/// the caller's concern, not the cache's).
pub struct Cache<K, V>
where
    K: Eq + Hash + Clone + Send + Sync + 'static,
    V: Clone + Send + Sync + 'static,
{
    inner: Arc<RwLock<HashMap<K, Entry<V>>>>,
    ttl: Option<Duration>,
}

impl<K, V> Clone for Cache<K, V>
where
    K: Eq + Hash + Clone + Send + Sync + 'static,
    V: Clone + Send + Sync + 'static,
{
    fn clone(&self) -> Self {
        Self {
            inner: Arc::clone(&self.inner),
            ttl: self.ttl,
        }
    }
}

struct Entry<V> {
    value: V,
    inserted_at: Instant,
}

impl<K, V> Cache<K, V>
where
    K: Eq + Hash + Clone + Send + Sync + 'static,
    V: Clone + Send + Sync + 'static,
{
    /// Construct a cache with no TTL — entries persist for the
    /// process lifetime or until `invalidate` / `invalidate_all`
    /// is called.
    #[must_use]
    pub fn new() -> Self {
        Self {
            inner: Arc::new(RwLock::new(HashMap::new())),
            ttl: None,
        }
    }

    /// Construct a cache with a per-entry TTL. Entries are
    /// considered expired (and lazily evicted on the next
    /// `get`) after the TTL elapses from insertion.
    #[must_use]
    pub fn with_ttl(ttl: Duration) -> Self {
        Self {
            inner: Arc::new(RwLock::new(HashMap::new())),
            ttl: Some(ttl),
        }
    }

    /// Return the cached value for `key`, or `None` when the
    /// entry is absent or expired. Expired entries are evicted
    /// before returning (lazy sweep).
    pub async fn get(&self, key: &K) -> Option<V> {
        // Fast path under the read lock: present + not expired.
        {
            let guard = self.inner.read().await;
            if let Some(entry) = guard.get(key) {
                if !self.is_expired(entry) {
                    return Some(entry.value.clone());
                }
            } else {
                return None;
            }
        }
        // Slow path: take the write lock, re-check (another
        // task may have already evicted), and evict if still
        // expired.
        let mut guard = self.inner.write().await;
        match guard.get(key) {
            Some(entry) if self.is_expired(entry) => {
                guard.remove(key);
                None
            }
            Some(entry) => Some(entry.value.clone()),
            None => None,
        }
    }

    /// Insert / overwrite `key` with `value`. Resets the
    /// per-entry TTL window.
    pub async fn insert(&self, key: K, value: V) {
        let mut guard = self.inner.write().await;
        guard.insert(
            key,
            Entry {
                value,
                inserted_at: Instant::now(),
            },
        );
    }

    /// Remove the entry for `key`, regardless of expiry. Used
    /// by callers that observe upstream invalidation events
    /// (e.g. secret-broker rotation → invalidate the
    /// credential cache for the affected `(job, secret-name)`).
    pub async fn invalidate(&self, key: &K) {
        let mut guard = self.inner.write().await;
        guard.remove(key);
    }

    /// Remove every entry. Used by callers reacting to
    /// scope-wide invalidation (e.g. job-end → clear the
    /// per-job schema cache).
    pub async fn invalidate_all(&self) {
        let mut guard = self.inner.write().await;
        guard.clear();
    }

    /// Number of entries currently in the cache (including
    /// expired-but-not-yet-evicted). Useful for tests + future
    /// metrics emission.
    pub async fn len(&self) -> usize {
        let guard = self.inner.read().await;
        guard.len()
    }

    /// Whether the cache holds zero entries.
    pub async fn is_empty(&self) -> bool {
        self.len().await == 0
    }

    fn is_expired(&self, entry: &Entry<V>) -> bool {
        match self.ttl {
            Some(ttl) => entry.inserted_at.elapsed() >= ttl,
            None => false,
        }
    }
}

impl<K, V> Default for Cache<K, V>
where
    K: Eq + Hash + Clone + Send + Sync + 'static,
    V: Clone + Send + Sync + 'static,
{
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;

    // Each test exercises the cache with a different K,V combo
    // to keep the service-agnostic property visible.

    #[tokio::test]
    async fn get_returns_none_when_absent() {
        let c: Cache<String, String> = Cache::new();
        assert_eq!(c.get(&"missing".to_string()).await, None);
    }

    #[tokio::test]
    async fn insert_then_get_returns_value() {
        let c: Cache<String, u64> = Cache::new();
        c.insert("k".to_string(), 42).await;
        assert_eq!(c.get(&"k".to_string()).await, Some(42));
    }

    #[tokio::test]
    async fn insert_overwrites_prior_value() {
        let c: Cache<&'static str, &'static str> = Cache::new();
        c.insert("k", "v1").await;
        c.insert("k", "v2").await;
        assert_eq!(c.get(&"k").await, Some("v2"));
    }

    #[tokio::test]
    async fn invalidate_removes_single_entry() {
        let c: Cache<String, String> = Cache::new();
        c.insert("a".to_string(), "1".to_string()).await;
        c.insert("b".to_string(), "2".to_string()).await;
        c.invalidate(&"a".to_string()).await;
        assert_eq!(c.get(&"a".to_string()).await, None);
        assert_eq!(c.get(&"b".to_string()).await, Some("2".to_string()));
    }

    #[tokio::test]
    async fn invalidate_all_clears_every_entry() {
        let c: Cache<String, String> = Cache::new();
        c.insert("a".to_string(), "1".to_string()).await;
        c.insert("b".to_string(), "2".to_string()).await;
        c.invalidate_all().await;
        assert!(c.is_empty().await);
    }

    #[tokio::test]
    async fn with_ttl_expires_entry_after_window() {
        let c: Cache<String, u64> = Cache::with_ttl(Duration::from_millis(50));
        c.insert("k".to_string(), 1).await;
        assert_eq!(c.get(&"k".to_string()).await, Some(1));
        tokio::time::sleep(Duration::from_millis(75)).await;
        assert_eq!(c.get(&"k".to_string()).await, None);
    }

    #[tokio::test]
    async fn cache_is_clone_cheap_and_shared() {
        // Cloning the cache yields a handle to the same
        // backing store — service consumers can hand clones
        // to per-task closures without losing entries.
        let a: Cache<String, u64> = Cache::new();
        let b = a.clone();
        a.insert("k".to_string(), 1).await;
        assert_eq!(b.get(&"k".to_string()).await, Some(1));
    }

    // Service-agnostic property: the same Cache type serves
    // proxy-leases-per-session (Cache<String, FakeLease>),
    // schemas-per-job (Cache<String, FakeSchema>), and
    // capabilities-per-target with TTL
    // (Cache<String, FakeCapabilities> + with_ttl).
    // No proxy-specific knowledge in this module.

    #[derive(Clone, Debug, PartialEq)]
    struct FakeLease(&'static str);
    #[derive(Clone, Debug, PartialEq)]
    struct FakeSchema(u32);

    #[tokio::test]
    async fn service_agnostic_proxy_lease_shape() {
        let c: Cache<String, FakeLease> = Cache::new();
        c.insert("session-1".to_string(), FakeLease("http://proxy"))
            .await;
        assert_eq!(
            c.get(&"session-1".to_string()).await,
            Some(FakeLease("http://proxy"))
        );
    }

    #[tokio::test]
    async fn service_agnostic_schema_shape() {
        let c: Cache<String, FakeSchema> = Cache::new();
        c.insert("job-1".to_string(), FakeSchema(7)).await;
        assert_eq!(c.get(&"job-1".to_string()).await, Some(FakeSchema(7)));
    }
}
