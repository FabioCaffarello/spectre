/*
Copyright 2026 Fabio Caffarello.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package tls

import (
	"crypto/tls"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// DefaultReloadInterval — re-read the client cert + key off disk
// at most once every 30 seconds. ADR-0032 §5.1's literal value;
// cert-manager rotations propagate within this window without a
// service restart. The trust bundle (RootCAs) is loaded once and
// not reloaded — bundle rotation per ADR-0032 §5.3 is rare and
// handled by a Pod restart.
const DefaultReloadInterval = 30 * time.Second

// ReloadingCredentials wraps `credentials.TransportCredentials`
// with a tls.Config whose `GetClientCertificate` hook reloads
// the client key pair from disk at most once per `interval`.
//
// One ReloadingCredentials instance is shared across all gRPC
// dials the operator makes against the same peer (the engine in
// W3.3 first auth PR). The reload is lazy — handshakes within
// the cache window reuse the in-memory `tls.Certificate`; the
// first handshake after the window re-reads the file.
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

// NewClientCredentials returns gRPC ClientCredentials configured
// from the resolved `Config`. Plaintext mode returns
// `insecure.NewCredentials()` so the dial path looks identical
// to the v1alpha1 default. Mutual mode returns a TLS credentials
// wrapper backed by a `ReloadingCredentials` that auto-reloads
// the client key pair on the 30-second cadence and verifies the
// engine's server cert against the configured trust bundle.
//
// `serverName` overrides SNI — the dial endpoint may be a raw
// host:port, but the cert's SAN list keys off the in-cluster
// Service DNS (e.g. `spectre-engine.spectre-system.svc.cluster.local`).
// Callers pass that DNS name so the handshake verifies the cert
// the chart's `_helpers.tpl::spectre.certificate` issued.
func NewClientCredentials(cfg Config, serverName string) (credentials.TransportCredentials, error) {
	if cfg.Mode == ModePlaintext {
		return insecure.NewCredentials(), nil
	}

	reloader := &ReloadingCredentials{
		certPath:  cfg.CertPath,
		keyPath:   cfg.KeyPath,
		interval:  DefaultReloadInterval,
		timeNow:   time.Now,
		readFiles: loadKeyPair,
	}
	if _, err := reloader.currentCert(); err != nil {
		// Surface the file-read failure at startup so the Pod
		// crashloops on misconfigured mounts rather than failing
		// the first dial.
		return nil, fmt.Errorf("tls: preload client cert: %w", err)
	}

	caPool, err := loadCAPool(cfg.CAPath)
	if err != nil {
		return nil, err
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: serverName,
		RootCAs:    caPool,
		GetClientCertificate: func(_ *tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return reloader.currentCert()
		},
	}
	return credentials.NewTLS(tlsCfg), nil
}

// currentCert returns the cached client cert when fresh; reloads
// from disk otherwise. Concurrent dials share the cache via a
// RWMutex — readers take the read lock for the fresh-cache path;
// the writer takes the write lock only when the interval lapsed.
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
	// Re-check inside the write lock — another goroutine may
	// have reloaded between our RUnlock and Lock.
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
