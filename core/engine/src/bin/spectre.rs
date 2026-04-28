// SPDX-License-Identifier: Apache-2.0

//! `spectre` — engine binary placeholder.
//!
//! The CLI subcommands (`run`, `validate`, `version`) were retired
//! in R2.3 alongside the subprocess launcher. The replacement is a
//! gRPC service entry point that lands later in this PR — once
//! `server.rs` (R2.3 step 7) is in place. Until that step, this
//! placeholder lets the workspace continue to build.

fn main() {
    eprintln!(
        "spectre engine: service entry point not yet wired (R2.3 in progress). \
         Run the engine via `just engine-run` once the server module lands."
    );
    std::process::exit(2);
}
