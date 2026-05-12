// SPDX-License-Identifier: Apache-2.0

package tls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// loadKeyPair reads the server cert + key PEM files and parses
// them into a `*tls.Certificate`. Mirrors the operator's W3.3
// loader; only the role label changes (server cert here, client
// cert in the operator).
func loadKeyPair(certPath, keyPath string) (*tls.Certificate, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("tls: read server cert %s: %w", certPath, err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("tls: read server key %s: %w", keyPath, err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("tls: parse server cert+key (cert=%s, key=%s): %w", certPath, keyPath, err)
	}
	return &cert, nil
}

// loadCAPool reads the trust bundle PEM and builds an
// `*x509.CertPool` for client-cert verification.
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
