// SPDX-License-Identifier: Apache-2.0

//! Integration test for the mTLS server-side path (W3.3).
//!
//! Generates a self-signed CA + server cert + client cert via
//! `openssl` (assumed present on every CI runner), boots a tonic
//! `Server` with the engine's `build_server_tls_config`, dials it
//! from a tonic client over verified mTLS, and asserts:
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
use std::path::PathBuf;
use std::process::Command;
use std::time::Duration;

use spectre_engine::tls::{build_server_tls_config, install_crypto_provider};
use tempfile::TempDir;
use tokio::net::TcpListener;
use tonic::transport::{Certificate, ClientTlsConfig, Endpoint, Identity, Server};
use tonic_health::pb::HealthCheckRequest;
use tonic_health::pb::health_client::HealthClient;

/// Generate a self-signed CA + server cert + client cert via
/// `openssl`. Returns the temp directory plus the three server-
/// side paths and the two client-side paths (cert + key).
#[allow(clippy::too_many_lines)]
fn generate_mtls_pki(server_cn: &str) -> (TempDir, ServerPaths, ClientMaterial) {
    let dir = TempDir::new().expect("tempdir");
    let p = dir.path();
    let ca_key = p.join("ca.key");
    let ca_crt = p.join("ca.crt");
    let server_key = p.join("server.key");
    let server_csr = p.join("server.csr");
    let server_crt = p.join("server.crt");
    let server_ext = p.join("server.ext");
    let client_key = p.join("client.key");
    let client_csr = p.join("client.csr");
    let client_crt = p.join("client.crt");

    // 1. CA: self-signed root, ECDSA P-256, 1-day lifetime.
    run_openssl(&[
        "ecparam",
        "-name",
        "prime256v1",
        "-genkey",
        "-noout",
        "-out",
        ca_key.to_str().unwrap(),
    ]);
    // The CA cert must be X.509 v3 with `basicConstraints=CA:TRUE`
    // or rustls/webpki rejects it with `UnsupportedCertVersion`.
    // `openssl req -x509` without extensions emits v1 on some
    // toolchains (Ubuntu CI's openssl 3.0 surfaced this where the
    // macOS 3.6 dev environment did not). `-addext` (openssl
    // 1.1.1+) injects the extensions so the cert ends up v3.
    run_openssl(&[
        "req",
        "-new",
        "-x509",
        "-key",
        ca_key.to_str().unwrap(),
        "-out",
        ca_crt.to_str().unwrap(),
        "-days",
        "1",
        "-subj",
        "/CN=spectre-test-ca",
        "-addext",
        "basicConstraints=critical,CA:TRUE",
        "-addext",
        "keyUsage=critical,keyCertSign,cRLSign",
    ]);

    // 2. Server cert with SAN matching server_cn.
    run_openssl(&[
        "ecparam",
        "-name",
        "prime256v1",
        "-genkey",
        "-noout",
        "-out",
        server_key.to_str().unwrap(),
    ]);
    run_openssl(&[
        "req",
        "-new",
        "-key",
        server_key.to_str().unwrap(),
        "-out",
        server_csr.to_str().unwrap(),
        "-subj",
        &format!("/CN={server_cn}"),
    ]);
    std::fs::write(
        &server_ext,
        format!(
            "subjectAltName=DNS:{server_cn},DNS:localhost,IP:127.0.0.1\n\
             extendedKeyUsage=serverAuth\n"
        ),
    )
    .unwrap();
    run_openssl(&[
        "x509",
        "-req",
        "-in",
        server_csr.to_str().unwrap(),
        "-CA",
        ca_crt.to_str().unwrap(),
        "-CAkey",
        ca_key.to_str().unwrap(),
        "-CAcreateserial",
        "-out",
        server_crt.to_str().unwrap(),
        "-days",
        "1",
        "-extfile",
        server_ext.to_str().unwrap(),
    ]);

    // 3. Client cert (CN doesn't need to match anything for the
    //    test; server's `client_ca_root` validates the chain).
    run_openssl(&[
        "ecparam",
        "-name",
        "prime256v1",
        "-genkey",
        "-noout",
        "-out",
        client_key.to_str().unwrap(),
    ]);
    run_openssl(&[
        "req",
        "-new",
        "-key",
        client_key.to_str().unwrap(),
        "-out",
        client_csr.to_str().unwrap(),
        "-subj",
        "/CN=spectre-test-client",
    ]);
    run_openssl(&[
        "x509",
        "-req",
        "-in",
        client_csr.to_str().unwrap(),
        "-CA",
        ca_crt.to_str().unwrap(),
        "-CAkey",
        ca_key.to_str().unwrap(),
        "-CAcreateserial",
        "-out",
        client_crt.to_str().unwrap(),
        "-days",
        "1",
    ]);

    let server_paths = ServerPaths {
        cert: server_crt.clone(),
        key: server_key.clone(),
        ca: ca_crt.clone(),
    };
    let client_material = ClientMaterial {
        cert_pem: std::fs::read(&client_crt).unwrap(),
        key_pem: std::fs::read(&client_key).unwrap(),
        ca_pem: std::fs::read(&ca_crt).unwrap(),
    };
    (dir, server_paths, client_material)
}

struct ServerPaths {
    cert: PathBuf,
    key: PathBuf,
    ca: PathBuf,
}

#[allow(clippy::struct_field_names)]
struct ClientMaterial {
    cert_pem: Vec<u8>,
    key_pem: Vec<u8>,
    ca_pem: Vec<u8>,
}

fn run_openssl(args: &[&str]) {
    let output = Command::new("openssl")
        .args(args)
        .output()
        .expect("openssl invocation failed");
    assert!(
        output.status.success(),
        "openssl {:?} failed: stderr={}",
        args,
        String::from_utf8_lossy(&output.stderr)
    );
}

async fn pick_port() -> u16 {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port();
    drop(listener);
    port
}

/// Spawn a tonic `Server` with the engine's `build_server_tls_config`
/// and a `tonic_health` service. Returns the bind address and a
/// shutdown handle (drop = stop).
async fn spawn_engine_like_server(server_paths: &ServerPaths) -> SocketAddr {
    let tls = build_server_tls_config(&server_paths.cert, &server_paths.key, &server_paths.ca)
        .expect("build_server_tls_config");

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
    let (_dir, server_paths, client_material) = generate_mtls_pki("localhost");
    let addr = spawn_engine_like_server(&server_paths).await;

    let identity = Identity::from_pem(&client_material.cert_pem, &client_material.key_pem);
    let ca = Certificate::from_pem(&client_material.ca_pem);
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
    let (_dir, server_paths, client_material) = generate_mtls_pki("localhost");
    let addr = spawn_engine_like_server(&server_paths).await;

    // No client identity — trust the CA so the server cert
    // verifies, but DO NOT present a client cert.
    let ca = Certificate::from_pem(&client_material.ca_pem);
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
