// SPDX-License-Identifier: Apache-2.0

//! Build script: generate Rust bindings for
//! `spectre.proxy.v1alpha1` via tonic-build (delegates message
//! generation to prost-build). See ADR-0007.
//!
//! Resolves the proto root relative to the crate Cargo.toml:
//! `sdks/rust/proxy/v1alpha1/` → `../../../../proto/` = repo root
//! `proto/`. Engine consumes this SDK as a path dependency in
//! W5.1; future publish-to-crates.io would require vendoring the
//! proto file under the crate, which is out of scope for W5.1.

use std::path::PathBuf;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let proto_root = PathBuf::from("../../../../proto");
    let proto_files: Vec<PathBuf> = ["spectre/proxy/v1alpha1/proxy.proto"]
        .iter()
        .map(|p| proto_root.join(p))
        .collect();

    for f in &proto_files {
        println!("cargo:rerun-if-changed={}", f.display());
    }
    println!("cargo:rerun-if-changed=build.rs");

    tonic_build::configure()
        .build_server(false) // client-only SDK
        .build_client(true)
        .compile_protos(&proto_files, &[proto_root])?;

    Ok(())
}
