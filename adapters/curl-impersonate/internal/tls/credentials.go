// SPDX-License-Identifier: Apache-2.0

package tls

import (
	"crypto/tls"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc/credentials"
)

// DefaultReloadInterval — re-read the server cert + key off disk
// at most once every 30 seconds, matching ADR-0032 §5.1's Go
// commitment. cert-manager rotations propagate within this
// window without restarting the Pod.
const DefaultReloadInterval = 30 * time.Second

// ReloadingCredentials wraps `credentials.TransportCredentials`
// with a `tls.Config` whose `GetCertificate` hook reloads the
// server key pair from disk at most once per `interval`. The
// `ClientCAs` pool is static (trust bundle rotation is rare per
// ADR-0032 §5.3 — handled by Pod restart).
type ReloadingCredentials struct {
	certPath string
	keyPath  string
	interval time.Duration

	mu        sync.RWMutex
	cached    *tls.Certificate
	cachedAt  time.Time
	timeNow   func() time.Time // injectable for tests
	readFiles func(certPath, keyPath string) (*tls.Certificate, error)
}

// NewServerCredentials returns gRPC server credentials for the
// resolved Config. Plaintext mode returns `nil` so the caller
// passes no `grpc.Creds` option (the v1alpha1 dial path is
// unchanged). Mutual mode returns `credentials.NewTLS(*tls.Config{
// ClientAuth: RequireAndVerifyClientCert, ClientCAs: pool,
// GetCertificate: r.currentCert})` — the adapter requires a
// valid client certificate (per ADR-0032 §4.2) and verifies it
// against the trust bundle.
func NewServerCredentials(cfg Config) (credentials.TransportCredentials, error) {
	if cfg.Mode == ModePlaintext {
		return nil, nil
	}

	reloader := &ReloadingCredentials{
		certPath:  cfg.CertPath,
		keyPath:   cfg.KeyPath,
		interval:  DefaultReloadInterval,
		timeNow:   time.Now,
		readFiles: loadKeyPair,
	}
	if _, err := reloader.currentCert(); err != nil {
		return nil, fmt.Errorf("tls: preload server cert: %w", err)
	}

	caPool, err := loadCAPool(cfg.CAPath)
	if err != nil {
		return nil, err
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  caPool,
		GetCertificate: func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return reloader.currentCert()
		},
	}
	return credentials.NewTLS(tlsCfg), nil
}

// currentCert returns the cached server cert when fresh; reloads
// from disk otherwise. Concurrent handshakes share the cache via
// a RWMutex — readers take the read lock for the fresh-cache
// path; the writer takes the write lock only when the interval
// lapsed. Mirrors the operator's W3.3 ReloadingCredentials.
func (r *ReloadingCredentials) currentCert() (*tls.Certificate, error) {
	r.mu.RLock()
	if r.cached != nil && r.timeNow().Sub(r.cachedAt) < r.interval {
		cert := r.cached
		r.mu.RUnlock()
		return cert, nil
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cached != nil && r.timeNow().Sub(r.cachedAt) < r.interval {
		return r.cached, nil
	}
	fresh, err := r.readFiles(r.certPath, r.keyPath)
	if err != nil {
		return nil, err
	}
	r.cached = fresh
	r.cachedAt = r.timeNow()
	return fresh, nil
}
