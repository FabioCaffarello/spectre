// SPDX-License-Identifier: Apache-2.0

//! `hello-hackernews` example binary.
//!
//! Reads `examples/hello-hackernews/job.yaml` (or a `--job=<path>`
//! override), runs it through the engine, and exits 0 on success.
//! With `--verbose`, the compiled [`Plan`](spectre_engine::Plan) is
//! printed to stderr before execution.
//!
//! ```bash
//! cd core/engine
//! cargo run --example hello-hackernews -- --verbose
//! ```
//!
//! See ADR-0012 for the architecture; see the project README for
//! status.

use std::path::PathBuf;
use std::process::ExitCode;

use anyhow::{Context, Result};
use spectre_engine::Engine;
use tracing::info;

fn default_job_path() -> PathBuf {
    // `CARGO_MANIFEST_DIR` is `core/engine` — the example file lives
    // at the repo's `examples/hello-hackernews/job.yaml`.
    let manifest_dir = env!("CARGO_MANIFEST_DIR");
    PathBuf::from(manifest_dir)
        .join("..")
        .join("..")
        .join("examples")
        .join("hello-hackernews")
        .join("job.yaml")
}

#[derive(Debug)]
struct Args {
    job: PathBuf,
    verbose: bool,
}

fn parse_args() -> Args {
    let mut job: Option<PathBuf> = None;
    let mut verbose = false;
    for arg in std::env::args().skip(1) {
        if arg == "--verbose" || arg == "-v" {
            verbose = true;
        } else if let Some(rest) = arg.strip_prefix("--job=") {
            job = Some(PathBuf::from(rest));
        } else if arg == "--help" || arg == "-h" {
            print_usage();
            std::process::exit(0);
        } else {
            eprintln!("unknown argument: {arg}");
            print_usage();
            std::process::exit(2);
        }
    }
    Args {
        job: job.unwrap_or_else(default_job_path),
        verbose,
    }
}

fn print_usage() {
    eprintln!("usage: hello-hackernews [--job=<path>] [--verbose]");
    eprintln!();
    eprintln!("Runs a DSL job through the Spectre engine. Without");
    eprintln!("--job, defaults to examples/hello-hackernews/job.yaml.");
}

#[tokio::main(flavor = "multi_thread")]
async fn main() -> ExitCode {
    if let Err(e) = run().await {
        eprintln!("error: {e:#}");
        return ExitCode::from(1);
    }
    ExitCode::SUCCESS
}

async fn run() -> Result<()> {
    let args = parse_args();

    let level = if args.verbose { "debug" } else { "info" };
    tracing_subscriber::fmt()
        .with_writer(std::io::stderr)
        .with_env_filter(format!("spectre_engine={level},hello_hackernews={level}"))
        .with_target(false)
        .compact()
        .init();

    let yaml = std::fs::read_to_string(&args.job)
        .with_context(|| format!("reading job at {}", args.job.display()))?;

    let job_dir = args
        .job
        .parent()
        .map_or_else(|| PathBuf::from("."), std::path::Path::to_path_buf);

    if args.verbose {
        let parsed = Engine::parse_job(&yaml)?;
        let plan = Engine::plan_job(&parsed);
        eprintln!("--- compiled plan ---");
        for (i, step) in plan.steps.iter().enumerate() {
            eprintln!("  [{i}] {step:?}");
        }
        eprintln!("required capabilities: {:?}", plan.required_capabilities);
        eprintln!("output: {:?}", plan.output);
        eprintln!("---");
    }

    let engine = Engine::new(None);
    let rows = engine.run_job(&yaml, &job_dir).await?;
    info!(rows, "hello-hackernews complete");
    Ok(())
}
