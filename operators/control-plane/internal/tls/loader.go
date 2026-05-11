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
	"crypto/x509"
	"fmt"
	"os"
)

// loadKeyPair reads the cert + key PEM files and parses them into
// a `*tls.Certificate`. Surfaces which file failed (cert or key)
// in the error message so cert-manager mount issues are easy to
// diagnose from operator logs.
func loadKeyPair(certPath, keyPath string) (*tls.Certificate, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("tls: read client cert %s: %w", certPath, err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("tls: read client key %s: %w", keyPath, err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("tls: parse client cert+key (cert=%s, key=%s): %w", certPath, keyPath, err)
	}
	return &cert, nil
}

// loadCAPool reads a PEM bundle and builds an `*x509.CertPool`.
// The bundle is `ca.crt` from cert-manager's per-service Secret —
// when the SelfSigned → CA → leaf chain renders, `ca.crt`
// carries the CA cert and pool.AppendCertsFromPEM consumes it.
func loadCAPool(caPath string) (*x509.CertPool, error) {
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("tls: read ca bundle %s: %w", caPath, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("tls: ca bundle %s contains no PEM-encoded certificates", caPath)
	}
	return pool, nil
}
