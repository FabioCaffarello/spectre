// SPDX-License-Identifier: Apache-2.0

//! Integration tests for `spectre_engine::kafka::KafkaProducer`.
//! Each test runs against a real Kafka broker dialled via
//! `SPECTRE_KAFKA_BROKERS` (the same env var the engine binary
//! reads at startup, ADR-0023 §12).
//!
//! Marked `#[ignore]` so the standard `cargo test` /
//! `just engine-test` run never depends on a Kafka broker:
//! contributors without a broker get green results from the
//! unit-level suite, and CI / local-dev workflows opt in via
//! `cargo test --test kafka_integration -- --ignored` (or the
//! `engine-kafka-test` justfile recipe added alongside).
//!
//! Mirrors the R4.2 `db_integration.rs` pattern — same env-var
//! convention, same `#[ignore]` discipline, same "bring up
//! `docker compose up -d` then opt in" workflow. Production
//! parity with the R7.1 Strimzi-managed Apache Kafka broker is
//! guaranteed because both speak the Apache Kafka API; the
//! Compose broker is `apache/kafka:3.7.1` in `KRaft` mode
//! (ADR-0023 §3 R4.4 addendum).

#![cfg(test)]

use std::env;
use std::time::Duration;

use rdkafka::Message;
use rdkafka::config::ClientConfig;
use rdkafka::consumer::{Consumer, StreamConsumer};
use rdkafka::message::Headers;
use spectre_engine::kafka::{KafkaConfig, KafkaProducer};
use uuid::Uuid;

const BROKERS_ENV: &str = "SPECTRE_KAFKA_BROKERS";

fn brokers() -> String {
    env::var(BROKERS_ENV)
        .unwrap_or_else(|_| panic!("{BROKERS_ENV} must be set to run kafka_integration tests"))
}

fn unique_topic(stem: &str) -> String {
    // Topic auto-creation is enabled in the Compose broker
    // (KAFKA_AUTO_CREATE_TOPICS_ENABLE=true). Each test uses a
    // unique topic so concurrent runs do not interfere; cleanup
    // is left to broker retention since topics carry minimal
    // overhead in dev.
    format!("spectre-it-{stem}-{}", Uuid::new_v4())
}

async fn build_producer(brokers: &str) -> KafkaProducer {
    let cfg = KafkaConfig {
        brokers: brokers.to_owned(),
        linger_ms: 5,
    };
    KafkaProducer::from_config(cfg)
        .await
        .expect("kafka producer")
}

fn build_consumer(brokers: &str, topic: &str, group: &str) -> StreamConsumer {
    let consumer: StreamConsumer = ClientConfig::new()
        .set("bootstrap.servers", brokers)
        .set("group.id", group)
        .set("auto.offset.reset", "earliest")
        .set("enable.auto.commit", "false")
        .create()
        .expect("consumer");
    consumer.subscribe(&[topic]).expect("consumer.subscribe");
    consumer
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
#[ignore = "requires a running Kafka broker at SPECTRE_BROKERS / SPECTRE_KAFKA_BROKERS"]
async fn publish_row_round_trips_payload_and_headers() {
    let brokers = brokers();
    let topic = unique_topic("round-trip");
    let producer = build_producer(&brokers).await;
    let consumer = build_consumer(&brokers, &topic, &format!("g-{}", Uuid::new_v4()));

    let job_id = Uuid::new_v4().to_string();
    let row_index = 7_i64;
    let driver = "playwright";
    let timestamp = "2026-04-28T12:00:00Z";
    let body = br#"{"title":"hi","url":"https://example.com"}"#;

    producer
        .publish_row(&topic, &job_id, row_index, driver, timestamp, body)
        .await
        .expect("publish_row");

    let message = tokio::time::timeout(Duration::from_secs(15), consumer.recv())
        .await
        .expect("consumer.recv timed out — broker reachable but no message arrived")
        .expect("consumer.recv");

    let payload = message.payload().expect("payload bytes");
    assert_eq!(payload, body, "body must round-trip unchanged");

    let key = message.key().expect("key bytes");
    assert_eq!(
        std::str::from_utf8(key).unwrap(),
        job_id,
        "partition key must be the job UUID (ADR-0023 §3)",
    );

    let headers = message.headers().expect("headers must be present");
    let mut found_job_id = false;
    let mut found_row_index = false;
    let mut found_driver = false;
    let mut found_timestamp = false;
    for i in 0..headers.count() {
        let h = headers.get(i);
        let value = h.value.unwrap_or(&[]);
        match h.key {
            "job_id" => {
                assert_eq!(std::str::from_utf8(value).unwrap(), job_id);
                found_job_id = true;
            }
            "row_index" => {
                assert_eq!(std::str::from_utf8(value).unwrap(), row_index.to_string());
                found_row_index = true;
            }
            "driver" => {
                assert_eq!(std::str::from_utf8(value).unwrap(), driver);
                found_driver = true;
            }
            "timestamp" => {
                assert_eq!(std::str::from_utf8(value).unwrap(), timestamp);
                found_timestamp = true;
            }
            other => panic!("unexpected header {other}"),
        }
    }
    assert!(found_job_id, "job_id header missing");
    assert!(found_row_index, "row_index header missing");
    assert!(found_driver, "driver header missing");
    assert!(found_timestamp, "timestamp header missing");
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
#[ignore = "requires a running Kafka broker at SPECTRE_KAFKA_BROKERS"]
async fn publish_row_keys_all_rows_to_same_partition_for_a_job() {
    // Section 3 / §3 R4.4 addendum commits to job_id as the
    // partition key. Asserts via header equality, since any two
    // messages keyed by the same string land on the same
    // partition under librdkafka's default partitioner.
    let brokers = brokers();
    let topic = unique_topic("partition-key");
    let producer = build_producer(&brokers).await;
    let consumer = build_consumer(&brokers, &topic, &format!("g-{}", Uuid::new_v4()));

    let job_id = Uuid::new_v4().to_string();
    for i in 0..3_i64 {
        producer
            .publish_row(
                &topic,
                &job_id,
                i,
                "curl-impersonate",
                "2026-04-28T12:00:00Z",
                format!(r#"{{"i":{i}}}"#).as_bytes(),
            )
            .await
            .expect("publish_row");
    }

    let mut partitions = Vec::with_capacity(3);
    for _ in 0..3 {
        let m = tokio::time::timeout(Duration::from_secs(15), consumer.recv())
            .await
            .expect("recv timeout")
            .expect("recv");
        partitions.push(m.partition());
        assert_eq!(std::str::from_utf8(m.key().expect("key")).unwrap(), job_id,);
    }
    let first = partitions[0];
    assert!(
        partitions.iter().all(|p| *p == first),
        "all rows for one job must land on the same partition; got {partitions:?}",
    );
}
