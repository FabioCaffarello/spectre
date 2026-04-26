// SPDX-License-Identifier: Apache-2.0

//! `spectre` — the Spectre CLI.
//!
//! ADR-0013 records why the CLI lives in the engine crate as
//! `src/bin/spectre.rs` rather than as a separate binary. The crate
//! `spectre-engine` produces the binary `spectre` (the `[[bin]]` name
//! in `Cargo.toml`); `cargo install spectre-engine` installs it under
//! that name.
//!
//! Three subcommands at v1alpha1:
//!
//! - `spectre run <job.yaml>` — parse, plan, launch the driver,
//!   execute, write JSONL.
//! - `spectre validate <job.yaml>` — parse, plan, check declared
//!   capabilities; print the compiled plan; never launch a driver.
//! - `spectre version` — print engine and protocol versions.

use std::path::{Path, PathBuf};
use std::process::ExitCode;

use anyhow::{Context, Result};
use clap::{Parser, Subcommand};
use spectre_engine::{ENGINE_VERSION, Engine, PROTOCOL_VERSION, Plan};
use tracing_subscriber::EnvFilter;

#[derive(Debug, Parser)]
#[command(
    name = "spectre",
    bin_name = "spectre",
    version = ENGINE_VERSION,
    about = "Spectre — driver-agnostic browser automation",
    long_about = None,
)]
struct Cli {
    #[command(subcommand)]
    command: Command,
}

#[derive(Debug, Subcommand)]
enum Command {
    /// Run a Spectre job to completion.
    Run {
        /// Path to the YAML job file.
        job: PathBuf,
        /// Print the compiled plan to stderr before execution.
        #[arg(short, long)]
        verbose: bool,
        /// Override the YAML's `output.path`. Use `-` for stdout.
        #[arg(short, long)]
        output: Option<String>,
        /// Override the adapters directory (default: workspace
        /// `adapters/` or `$SPECTRE_ADAPTERS_PATH`).
        #[arg(long)]
        adapters_path: Option<PathBuf>,
    },
    /// Parse, plan, and check capabilities without running the driver.
    Validate {
        /// Path to the YAML job file.
        job: PathBuf,
        /// Override the adapters directory.
        #[arg(long)]
        adapters_path: Option<PathBuf>,
    },
    /// Print engine and protocol versions.
    Version,
}

#[tokio::main(flavor = "multi_thread")]
async fn main() -> ExitCode {
    let cli = Cli::parse();

    init_tracing(matches!(cli.command, Command::Run { verbose: true, .. }));

    match dispatch(cli.command).await {
        Ok(code) => code,
        Err(e) => {
            eprintln!("error: {e:#}");
            ExitCode::from(1)
        }
    }
}

async fn dispatch(cmd: Command) -> Result<ExitCode> {
    match cmd {
        Command::Run {
            job,
            verbose,
            output,
            adapters_path,
        } => cmd_run(&job, verbose, output.as_deref(), adapters_path.as_deref()).await,
        Command::Validate { job, adapters_path } => cmd_validate(&job, adapters_path.as_deref()),
        Command::Version => {
            cmd_version();
            Ok(ExitCode::SUCCESS)
        }
    }
}

fn cmd_version() {
    println!("spectre {ENGINE_VERSION}");
    println!("protocol {PROTOCOL_VERSION}");
}

fn cmd_validate(job_path: &Path, adapters_path: Option<&Path>) -> Result<ExitCode> {
    let yaml = std::fs::read_to_string(job_path)
        .with_context(|| format!("reading job at {}", job_path.display()))?;
    let engine = Engine::new(adapters_path);
    let plan = engine.validate_only(&yaml)?;
    print_plan(&plan, &mut std::io::stdout())?;
    Ok(ExitCode::SUCCESS)
}

async fn cmd_run(
    job_path: &Path,
    verbose: bool,
    output_override: Option<&str>,
    adapters_path: Option<&Path>,
) -> Result<ExitCode> {
    let yaml = std::fs::read_to_string(job_path)
        .with_context(|| format!("reading job at {}", job_path.display()))?;
    let job_dir = job_path
        .parent()
        .map_or_else(|| PathBuf::from("."), Path::to_path_buf);

    let job = Engine::parse_job(&yaml)?;
    let mut plan = Engine::plan_job(&job);
    if let Some(path) = output_override {
        path.clone_into(&mut plan.output.path);
    }

    if verbose {
        eprintln!("--- compiled plan ---");
        print_plan(&plan, &mut std::io::stderr())?;
        eprintln!("---");
    }

    let engine = Engine::new(adapters_path);
    let rows = engine.run_plan(plan, &job_dir).await?;
    tracing::info!(rows, "spectre run complete");
    Ok(ExitCode::SUCCESS)
}

fn print_plan(plan: &Plan, out: &mut impl std::io::Write) -> std::io::Result<()> {
    writeln!(out, "Driver: {}", plan.driver)?;
    let mut caps: Vec<&String> = plan.required_capabilities.iter().collect();
    caps.sort();
    writeln!(out, "Required capabilities: {caps:?}")?;
    writeln!(
        out,
        "Output: format={:?} path={:?}",
        plan.output.format, plan.output.path
    )?;
    writeln!(out, "Steps:")?;
    for (i, step) in plan.steps.iter().enumerate() {
        writeln!(out, "  [{i}] {step:?}")?;
    }
    Ok(())
}

fn init_tracing(verbose: bool) {
    // RUST_LOG always wins. Otherwise: WARN by default, INFO with
    // --verbose. tracing-subscriber writes to stderr so JSONL on stdout
    // stays clean for piping.
    let default_level = if verbose { "info" } else { "warn" };
    let filter = EnvFilter::try_from_default_env().unwrap_or_else(|_| {
        EnvFilter::new(format!(
            "spectre_engine={default_level},spectre={default_level}"
        ))
    });
    let _ = tracing_subscriber::fmt()
        .with_writer(std::io::stderr)
        .with_env_filter(filter)
        .with_target(false)
        .compact()
        .try_init();
}
