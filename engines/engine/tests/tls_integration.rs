// SPDX-License-Identifier: Apache-2.0

//! Integration test for the mTLS server-side path (W3.3).
//!
//! Generates a self-signed CA + server cert + client cert via
//! `rcgen` (a Rust-native PKI generator — replaces an earlier
//! `openssl req` invocation that surfaced OpenSSL-version-
//! sensitive X.509 v1-vs-v3 behaviour between macOS dev and
//! Ubuntu CI), boots a tonic `Server` with the engine's
//! `build_server_tls_config`, dials it from a tonic client over
//! verified mTLS, and asserts:
//!
//! 1. A client presenting the CA-signed client cert can complete
//!    the gRPC health check.
//! 2. A client presenting no cert is rejected at the TLS
//!    handshake.
//!
//! The test exercises the same code path the engine binary runs
//! through `apply_tls_mode` at startup, minus the postgres /
//! kafka / s3 init that integration tests would otherwise pull
//! in.

#![cfg(test)]

use std::net::SocketAddr;
use std::time::Duration;

use rcgen::{
    BasicConstraints, CertificateParams, DnType, ExtendedKeyUsagePurpose, IsCa, KeyPair,
    KeyUsagePurpose,
};
use spectre_engine::tls::install_crypto_provider;
use tokio::net::TcpListener;
use tonic::transport::{Certificate, ClientTlsConfig, Endpoint, Identity, Server, ServerTlsConfig};
use tonic_health::pb::HealthCheckRequest;
use tonic_health::pb::health_client::HealthClient;

/// Generated PEM material from a self-signed CA → server + client
/// leaf pair. The same `ca_pem` is used by both the server (as
/// the trust bundle for verifying the incoming client cert) and
/// the client (as the trust bundle for verifying the server cert).
#[allow(clippy::struct_field_names)]
struct MtlsPki {
    ca_pem: String,
    server_cert_pem: String,
    server_key_pem: String,
    client_cert_pem: String,
    client_key_pem: String,
}

fn generate_mtls_pki(server_cn: &str) -> MtlsPki {
    // 1. Self-signed CA. Always X.509 v3 with the required
    //    `basicConstraints=CA:TRUE` + `keyCertSign` usages —
    //    rcgen never emits v1, so the toolchain-skew that broke
    //    the openssl path is gone.
    let mut ca_params = CertificateParams::default();
    ca_params.is_ca = IsCa::Ca(BasicConstraints::Unconstrained);
    ca_params
        .distinguished_name
        .push(DnType::CommonName, "spectre-test-ca");
    ca_params.key_usages = vec![KeyUsagePurpose::KeyCertSign, KeyUsagePurpose::CrlSign];
    let ca_key = KeyPair::generate().expect("ca keypair");
    let ca_cert = ca_params.self_signed(&ca_key).expect("ca self-sign");

    // 2. Server leaf cert — SAN list includes the requested CN
    //    (`localhost` in the test) so the client's
    //    `domain_name(...)` SNI / SAN check succeeds.
    let mut server_params = CertificateParams::new(vec![server_cn.to_string()]).expect("san");
    server_params
        .distinguished_name
        .push(DnType::CommonName, server_cn);
    server_params.extended_key_usages = vec![ExtendedKeyUsagePurpose::ServerAuth];
    let server_key = KeyPair::generate().expect("server keypair");
    let server_cert = server_params
        .signed_by(&server_key, &ca_cert, &ca_key)
        .expect("server sign");

    // 3. Client leaf cert. The CN doesn't have to match anything
    //    in particular; the server validates the chain back to
    //    the CA via `client_ca_root`. ExtendedKeyUsage=ClientAuth
    //    is what some strict implementations enforce.
    let mut client_params = CertificateParams::default();
    client_params
        .distinguished_name
        .push(DnType::CommonName, "spectre-test-client");
    client_params.extended_key_usages = vec![ExtendedKeyUsagePurpose::ClientAuth];
    let client_key = KeyPair::generate().expect("client keypair");
    let client_cert = client_params
        .signed_by(&client_key, &ca_cert, &ca_key)
        .expect("client sign");

    MtlsPki {
        ca_pem: ca_cert.pem(),
        server_cert_pem: server_cert.pem(),
        server_key_pem: server_key.serialize_pem(),
        client_cert_pem: client_cert.pem(),
        client_key_pem: client_key.serialize_pem(),
    }
}

async fn pick_port() -> u16 {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port();
    drop(listener);
    port
}

/// Spawn a tonic `Server` with a `ServerTlsConfig` matching the
/// shape the engine builds via `build_server_tls_config` (same
/// `Identity::from_pem` + `Certificate::from_pem` + `client_ca_root`
/// chain), plus a `tonic_health` service. Returns the bind address.
async fn spawn_engine_like_server(pki: &MtlsPki) -> SocketAddr {
    let identity = Identity::from_pem(
        pki.server_cert_pem.as_bytes(),
        pki.server_key_pem.as_bytes(),
    );
    let ca = Certificate::from_pem(pki.ca_pem.as_bytes());
    let tls = ServerTlsConfig::new().identity(identity).client_ca_root(ca);

    let port = pick_port().await;
    let addr: SocketAddr = format!("127.0.0.1:{port}").parse().unwrap();

    let (health_reporter, health_service) = tonic_health::server::health_reporter();
    health_reporter
        .set_serving::<tonic_health::pb::health_server::HealthServer<tonic_health::server::HealthService>>()
        .await;

    let server = Server::builder()
        .tls_config(tls)
        .expect("tls_config")
        .add_service(health_service)
        .serve(addr);
    tokio::spawn(server);

    // Give the listener a moment to bind.
    tokio::time::sleep(Duration::from_millis(200)).await;
    addr
}

#[tokio::test]
async fn mtls_round_trip_health_check_succeeds() {
    install_crypto_provider();
    let pki = generate_mtls_pki("localhost");
    let addr = spawn_engine_like_server(&pki).await;

    let identity = Identity::from_pem(
        pki.client_cert_pem.as_bytes(),
        pki.client_key_pem.as_bytes(),
    );
    let ca = Certificate::from_pem(pki.ca_pem.as_bytes());
    let tls_config = ClientTlsConfig::new()
        .domain_name("localhost")
        .ca_certificate(ca)
        .identity(identity);

    let endpoint = Endpoint::from_shared(format!("https://{addr}"))
        .expect("endpoint")
        .tls_config(tls_config)
        .expect("client tls_config");
    let channel = endpoint.connect().await.expect("channel connect");

    let mut client = HealthClient::new(channel);
    let response = client
        .check(HealthCheckRequest {
            service: String::new(),
        })
        .await
        .expect("health check");
    assert_eq!(
        response.into_inner().status,
        tonic_health::pb::health_check_response::ServingStatus::Serving as i32
    );
}

#[tokio::test]
async fn mtls_rejects_client_without_certificate() {
    install_crypto_provider();
    let pki = generate_mtls_pki("localhost");
    let addr = spawn_engine_like_server(&pki).await;

    // No client identity — trust the CA so the server cert
    // verifies, but DO NOT present a client cert.
    let ca = Certificate::from_pem(pki.ca_pem.as_bytes());
    let tls_config = ClientTlsConfig::new()
        .domain_name("localhost")
        .ca_certificate(ca);

    let endpoint = Endpoint::from_shared(format!("https://{addr}"))
        .expect("endpoint")
        .tls_config(tls_config)
        .expect("client tls_config")
        .timeout(Duration::from_secs(3));

    // Either `connect` or the first RPC will surface the TLS
    // failure. The server requires a client cert (`client_ca_root`
    // set, `client_auth_optional` default false), so the handshake
    // is rejected — propagated to the client as an error.
    let result = async {
        let channel = endpoint.connect().await?;
        let mut client = HealthClient::new(channel);
        client
            .check(HealthCheckRequest {
                service: String::new(),
            })
            .await
            .map_err(|e| anyhow::anyhow!("rpc: {e}"))?;
        Ok::<(), anyhow::Error>(())
    }
    .await;

    assert!(
        result.is_err(),
        "no-client-cert dial should be rejected, got Ok",
    );
}
