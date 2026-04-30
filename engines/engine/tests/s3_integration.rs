// SPDX-License-Identifier: Apache-2.0

//! Integration tests for `spectre_engine::s3::S3Uploader`.
//!
//! Each test runs against a real S3-compatible endpoint dialled
//! via `SPECTRE_S3_*` (the same env vars the engine binary reads
//! at startup, ADR-0024 §3). Compose ships `MinIO` at
//! `localhost:9000` (S3 API) plus a one-shot bucket-bootstrap
//! container that pre-creates `spectre-rows`.
//!
//! Marked `#[ignore]` so the standard `cargo test` /
//! `just engine-test` run never depends on a running `MinIO`:
//! contributors without Docker get green results from the
//! unit-level suite, and CI / local-dev workflows opt in via
//! `cargo test --test s3_integration -- --ignored` (or the
//! `engine-s3-test` justfile recipe).
//!
//! Mirrors R4.4's `kafka_integration.rs` pattern — same env-var
//! convention, same `#[ignore]` discipline, same "bring up
//! `docker compose up -d` then opt in" workflow.

#![cfg(test)]

use std::env;
use std::time::Duration;

use aws_config::BehaviorVersion;
use aws_credential_types::Credentials;
use aws_sdk_s3::Client as S3Client;
use aws_sdk_s3::config::Region;
use spectre_engine::s3::{S3Config, S3Uploader, render_key};
use uuid::Uuid;

const ENDPOINT_ENV: &str = "SPECTRE_S3_ENDPOINT";
const REGION_ENV: &str = "SPECTRE_S3_REGION";
const ACCESS_KEY_ENV: &str = "SPECTRE_S3_ACCESS_KEY_ID";
const SECRET_KEY_ENV: &str = "SPECTRE_S3_SECRET_ACCESS_KEY";

const TEST_BUCKET: &str = "spectre-rows";
const STATIC_PROVIDER: &str = "spectre-it";

fn endpoint() -> String {
    env::var(ENDPOINT_ENV)
        .unwrap_or_else(|_| panic!("{ENDPOINT_ENV} must be set to run s3_integration tests"))
}

fn region() -> String {
    env::var(REGION_ENV).unwrap_or_else(|_| "us-east-1".to_owned())
}

fn access_key() -> String {
    env::var(ACCESS_KEY_ENV)
        .unwrap_or_else(|_| panic!("{ACCESS_KEY_ENV} must be set to run s3_integration tests"))
}

fn secret_key() -> String {
    env::var(SECRET_KEY_ENV)
        .unwrap_or_else(|_| panic!("{SECRET_KEY_ENV} must be set to run s3_integration tests"))
}

fn build_uploader() -> S3Uploader {
    let cfg = S3Config {
        endpoint: Some(endpoint()),
        region: region(),
        access_key_id: Some(access_key()),
        secret_access_key: Some(secret_key()),
    };
    S3Uploader::from_config(&cfg)
}

fn build_verifier_client() -> S3Client {
    let creds = Credentials::new(access_key(), secret_key(), None, None, STATIC_PROVIDER);
    let conf = aws_sdk_s3::Config::builder()
        .behavior_version(BehaviorVersion::latest())
        .endpoint_url(endpoint())
        .region(Region::new(region()))
        .credentials_provider(creds)
        .force_path_style(true)
        .build();
    S3Client::from_conf(conf)
}

async fn fetch_object_bytes(client: &S3Client, bucket: &str, key: &str) -> Vec<u8> {
    let resp = client
        .get_object()
        .bucket(bucket)
        .key(key)
        .send()
        .await
        .expect("get_object");
    let body = resp.body.collect().await.expect("collect body");
    body.into_bytes().to_vec()
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
#[ignore = "requires a running S3-compatible endpoint at SPECTRE_S3_ENDPOINT"]
async fn upload_jsonl_round_trips_payload() {
    let uploader = build_uploader();
    let verifier = build_verifier_client();

    let job_id = Uuid::new_v4();
    let key = render_key("it/{{.JobID}}/rows.jsonl", job_id);
    let body = b"{\"row\":1}\n{\"row\":2}\n{\"row\":3}\n".to_vec();
    let expected = body.clone();

    uploader
        .upload_jsonl(TEST_BUCKET, &key, body)
        .await
        .expect("upload ok");

    // Brief settle so the bucket index reflects the new object on
    // some MinIO versions.
    tokio::time::sleep(Duration::from_millis(50)).await;
    let fetched = fetch_object_bytes(&verifier, TEST_BUCKET, &key).await;
    assert_eq!(fetched, expected, "round-tripped payload differs");

    // Verify content type via head_object.
    let head = verifier
        .head_object()
        .bucket(TEST_BUCKET)
        .key(&key)
        .send()
        .await
        .expect("head_object");
    assert_eq!(
        head.content_type().unwrap_or_default(),
        "application/x-ndjson"
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
#[ignore = "requires a running S3-compatible endpoint at SPECTRE_S3_ENDPOINT"]
async fn empty_result_set_uploads_zero_byte_object() {
    // Per ADR-0024 §3: empty-result jobs still upload an object
    // so the post-job presence-or-absence of the key is a
    // reliable signal.
    let uploader = build_uploader();
    let verifier = build_verifier_client();

    let job_id = Uuid::new_v4();
    let key = render_key("it/{{.JobID}}/empty.jsonl", job_id);

    uploader
        .upload_jsonl(TEST_BUCKET, &key, Vec::new())
        .await
        .expect("upload empty ok");

    let fetched = fetch_object_bytes(&verifier, TEST_BUCKET, &key).await;
    assert!(fetched.is_empty(), "empty-result upload must be 0 bytes");
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
#[ignore = "requires a running S3-compatible endpoint at SPECTRE_S3_ENDPOINT"]
async fn key_template_substitutes_job_id() {
    // Sanity check: the engine renders {{.JobID}} before calling
    // the uploader. Verifies via a fresh job_id on each run.
    let job_id = Uuid::new_v4();
    let rendered = render_key("paths/{{.JobID}}/out.jsonl", job_id);
    assert!(rendered.contains(&job_id.to_string()));
    assert!(!rendered.contains("{{.JobID}}"));

    let uploader = build_uploader();
    let verifier = build_verifier_client();
    uploader
        .upload_jsonl(TEST_BUCKET, &rendered, b"{\"x\":1}\n".to_vec())
        .await
        .expect("upload with rendered key");

    let fetched = fetch_object_bytes(&verifier, TEST_BUCKET, &rendered).await;
    assert_eq!(fetched, b"{\"x\":1}\n");
}
