// SPDX-License-Identifier: Apache-2.0

//! Build script: generate Rust bindings for the Driver Protocol via
//! tonic-build (which delegates message generation to prost-build) and
//! emit a small `protocol_version.rs` file alongside them so the
//! crate has a single, generation-derived `PROTOCOL_VERSION`
//! constant. See `docs/adr/0007-protocol-code-generation.md`.

use std::env;
use std::path::PathBuf;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let proto_root = PathBuf::from("../../proto");
    let proto_files: Vec<PathBuf> = [
        "spectre/driver/v1alpha1/driver.proto",
        "spectre/driver/v1alpha1/capabilities.proto",
        "spectre/driver/v1alpha1/errors.proto",
        "spectre/driver/v1alpha1/extraction.proto",
    ]
    .iter()
    .map(|p| proto_root.join(p))
    .collect();

    for f in &proto_files {
        println!("cargo:rerun-if-changed={}", f.display());
    }
    println!("cargo:rerun-if-changed=build.rs");

    tonic_build::configure()
        .build_client(true)
        .build_server(true)
        .compile_protos(&proto_files, &[proto_root])?;

    let out_dir = PathBuf::from(env::var("OUT_DIR")?);
    std::fs::write(
        out_dir.join("protocol_version.rs"),
        "pub const PROTOCOL_VERSION: &str = \"spectre.driver.v1alpha1\";\n",
    )?;

    Ok(())
}
