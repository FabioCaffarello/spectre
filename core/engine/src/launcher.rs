// SPDX-License-Identifier: Apache-2.0

//! Driver subprocess launcher.
//!
//! Mirrors the Python conformance harness from PR3
//! (`tools/conformance/src/spectre_conformance/harness.py`). The
//! contract:
//!
//! - Read `<adapters_path>/<driver>/driver.yaml` and pick the first
//!   `grpc-uds` transport's command.
//! - Allocate a UDS path under `/tmp/` (macOS' 104-character `AF_UNIX`
//!   limit makes `tempfile::tempdir()` unsafe; see ADR-0008 / ADR-0012).
//! - Spawn the subprocess with `SPECTRE_DRIVER_SOCKET=<path>` and
//!   `--socket=<path>` appended to argv. Pipe stdout / stderr.
//! - Watch stdout for the readiness line matching
//!   `^ready unix:(\S+)$` (ADR-0008); on match, return a
//!   [`DriverHandle`] holding the socket path.
//! - On `Drop` or explicit `shutdown()`, send SIGTERM, wait up to
//!   10 s, escalate to SIGKILL, unlink the socket file.

use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::sync::Arc;
use std::time::Duration;

use nix::sys::signal::{Signal, kill};
use nix::unistd::Pid;
use regex::Regex;
use serde::Deserialize;
use thiserror::Error;
use tokio::io::{AsyncBufReadExt, BufReader};
use tokio::process::{Child, Command};
use tokio::sync::Mutex;
use tokio::time::timeout;
use tracing::{debug, warn};
use uuid::Uuid;

/// Default deadline for the driver to print its readiness line.
pub const DEFAULT_READY_TIMEOUT: Duration = Duration::from_secs(10);

/// Default deadline for graceful shutdown after SIGTERM. Twice the
/// conformance harness's 5 s default — a real adapter may be holding
/// a `BrowserContext` worth tearing down. See ADR-0012 §4.
pub const DEFAULT_SHUTDOWN_TIMEOUT: Duration = Duration::from_secs(10);

const READY_LINE_REGEX: &str = r"^ready unix:(\S+)$";

/// Errors reported by the launcher.
#[derive(Debug, Error)]
pub enum LauncherError {
    /// `driver.yaml` could not be read.
    #[error("failed to read driver manifest at {path}: {source}")]
    ManifestRead {
        /// The manifest path that failed to read.
        path: PathBuf,
        /// Underlying I/O error.
        #[source]
        source: std::io::Error,
    },

    /// `driver.yaml` could not be parsed.
    #[error("failed to parse driver manifest at {path}: {source}")]
    ManifestParse {
        /// The manifest path that failed to parse.
        path: PathBuf,
        /// Underlying YAML error.
        #[source]
        source: serde_yaml_ng::Error,
    },

    /// The manifest declares no transport whose `kind` is `grpc-uds`.
    #[error("driver manifest at {path} has no grpc-uds transport")]
    NoUdsTransport {
        /// The manifest path.
        path: PathBuf,
    },

    /// The transport entry has an empty command.
    #[error("driver manifest at {path} declares an empty command")]
    EmptyCommand {
        /// The manifest path.
        path: PathBuf,
    },

    /// The subprocess failed to start.
    #[error("failed to spawn driver: {0}")]
    Spawn(#[source] std::io::Error),

    /// The subprocess started but did not signal readiness within
    /// the timeout window. The captured stderr tail is included for
    /// diagnostics.
    #[error(
        "driver did not signal readiness within {timeout_s}s\n--- driver stderr ---\n{stderr_tail}"
    )]
    ReadinessTimeout {
        /// Timeout in seconds (logged as integer for legibility).
        timeout_s: u64,
        /// Tail of the captured stderr at the point of failure.
        stderr_tail: String,
    },

    /// The subprocess exited before signalling readiness.
    #[error(
        "driver exited before becoming ready (status={status})\n--- driver stderr ---\n{stderr_tail}"
    )]
    EarlyExit {
        /// Exit status as a string (`exit code N`, `signal: N`, etc.).
        status: String,
        /// Tail of the captured stderr at the point of failure.
        stderr_tail: String,
    },
}

// -- Manifest --------------------------------------------------------------

#[derive(Debug, Deserialize)]
struct RawManifest {
    #[serde(default)]
    transports: Vec<RawTransport>,
}

#[derive(Debug, Deserialize)]
struct RawTransport {
    #[serde(default)]
    kind: String,
    #[serde(default)]
    command: Vec<String>,
}

// -- Public API ------------------------------------------------------------

/// Allocate a fresh socket path under `/tmp`. Public so integration
/// tests can mirror the format.
#[must_use]
pub fn allocate_socket_path() -> PathBuf {
    PathBuf::from(format!("/tmp/spectre-engine-{}.sock", Uuid::new_v4()))
}

/// Resolve the adapters directory.
///
/// Precedence:
///
/// 1. Explicit argument `override_path`, if `Some`.
/// 2. Environment variable `SPECTRE_ADAPTERS_PATH`.
/// 3. The workspace-relative default:
///    `<engine-crate-dir>/../../adapters/`.
#[must_use]
pub fn resolve_adapters_path(override_path: Option<&Path>) -> PathBuf {
    if let Some(p) = override_path {
        return p.to_path_buf();
    }
    if let Ok(p) = std::env::var("SPECTRE_ADAPTERS_PATH") {
        return PathBuf::from(p);
    }
    // CARGO_MANIFEST_DIR points at `core/engine`.
    let manifest_dir = env!("CARGO_MANIFEST_DIR");
    PathBuf::from(manifest_dir)
        .join("..")
        .join("..")
        .join("adapters")
}

/// A handle to a running driver subprocess.
///
/// On `Drop`, [`shutdown`](Self::shutdown) is invoked synchronously
/// inside whatever Tokio runtime the handle was created in. A panic
/// during `Drop` may leak the subprocess; this is a known limit of
/// stdlib `Drop` semantics.
#[derive(Debug)]
pub struct DriverHandle {
    socket: PathBuf,
    child: Arc<Mutex<Option<Child>>>,
    #[allow(dead_code)]
    declared_capabilities: Vec<String>,
    shutdown_timeout: Duration,
    shutdown_done: Arc<std::sync::atomic::AtomicBool>,
}

impl DriverHandle {
    /// Path to the UDS the driver is listening on. Engine clients
    /// dial this path.
    #[must_use]
    pub fn socket_path(&self) -> &Path {
        &self.socket
    }

    /// Send SIGTERM, wait up to `shutdown_timeout`, escalate to
    /// SIGKILL on timeout, then unlink the socket. Idempotent.
    pub async fn shutdown(&self) {
        if self
            .shutdown_done
            .swap(true, std::sync::atomic::Ordering::SeqCst)
        {
            return;
        }
        let mut guard = self.child.lock().await;
        if let Some(mut child) = guard.take() {
            if let Some(pid) = child.id() {
                if let Ok(pid_i32) = i32::try_from(pid) {
                    if let Err(e) = kill(Pid::from_raw(pid_i32), Signal::SIGTERM) {
                        warn!(?e, "failed to send SIGTERM to driver");
                    }
                }
            }
            match timeout(self.shutdown_timeout, child.wait()).await {
                Ok(Ok(status)) => {
                    debug!(?status, "driver exited after SIGTERM");
                }
                Ok(Err(e)) => {
                    warn!(?e, "driver wait() failed after SIGTERM");
                }
                Err(_) => {
                    warn!("driver did not exit within shutdown timeout; sending SIGKILL");
                    let _ = child.kill().await;
                    let _ = child.wait().await;
                }
            }
        }
        // Best-effort unlink. The driver is also responsible for
        // cleanup (ADR-0008), but a SIGKILL'd subprocess never gets
        // there.
        let _ = std::fs::remove_file(&self.socket);
    }
}

impl Drop for DriverHandle {
    fn drop(&mut self) {
        if self.shutdown_done.load(std::sync::atomic::Ordering::SeqCst) {
            return;
        }
        // Fire-and-forget on the current Tokio runtime if one is
        // available. Outside a runtime (e.g. unit tests that drop the
        // handle synchronously) we fall back to a blocking SIGTERM
        // and a 1 s wait; the loud path is the async one.
        let socket = self.socket.clone();
        let child = self.child.clone();
        let shutdown_done = self.shutdown_done.clone();
        let timeout_d = self.shutdown_timeout;
        if let Ok(handle) = tokio::runtime::Handle::try_current() {
            handle.spawn(async move {
                if shutdown_done.swap(true, std::sync::atomic::Ordering::SeqCst) {
                    return;
                }
                let mut guard = child.lock().await;
                if let Some(mut c) = guard.take() {
                    if let Some(pid) = c.id() {
                        if let Ok(pid_i32) = i32::try_from(pid) {
                            let _ = kill(Pid::from_raw(pid_i32), Signal::SIGTERM);
                        }
                    }
                    if (timeout(timeout_d, c.wait()).await).is_err() {
                        let _ = c.kill().await;
                        let _ = c.wait().await;
                    }
                }
                let _ = std::fs::remove_file(&socket);
            });
        } else if let Ok(mut guard) = child.try_lock() {
            if let Some(c) = guard.take() {
                if let Some(pid) = c.id() {
                    if let Ok(pid_i32) = i32::try_from(pid) {
                        let _ = kill(Pid::from_raw(pid_i32), Signal::SIGTERM);
                    }
                }
                let _ = std::fs::remove_file(&socket);
            }
        }
    }
}

/// Read `driver.yaml` and return the launch command for the first
/// `grpc-uds` transport.
///
/// # Errors
///
/// Returns [`LauncherError::ManifestRead`], [`LauncherError::ManifestParse`],
/// [`LauncherError::NoUdsTransport`], or [`LauncherError::EmptyCommand`]
/// on the corresponding failures.
pub fn load_uds_command(manifest_path: &Path) -> Result<Vec<String>, LauncherError> {
    let raw = std::fs::read_to_string(manifest_path).map_err(|e| LauncherError::ManifestRead {
        path: manifest_path.to_path_buf(),
        source: e,
    })?;
    let manifest: RawManifest =
        serde_yaml_ng::from_str(&raw).map_err(|e| LauncherError::ManifestParse {
            path: manifest_path.to_path_buf(),
            source: e,
        })?;
    let transport = manifest
        .transports
        .into_iter()
        .find(|t| t.kind == "grpc-uds")
        .ok_or_else(|| LauncherError::NoUdsTransport {
            path: manifest_path.to_path_buf(),
        })?;
    if transport.command.is_empty() {
        return Err(LauncherError::EmptyCommand {
            path: manifest_path.to_path_buf(),
        });
    }
    Ok(transport.command)
}

/// Launch the named driver. Reads
/// `<adapters_path>/<driver>/driver.yaml`, allocates a UDS path,
/// spawns the subprocess, waits for the readiness line, and returns
/// a [`DriverHandle`].
///
/// # Errors
///
/// Surfaces every [`LauncherError`] variant; the most common are
/// [`LauncherError::ReadinessTimeout`] (the adapter started but did
/// not print the expected line) and [`LauncherError::EarlyExit`] (the
/// adapter crashed before printing it). Both include a tail of the
/// captured stderr.
///
/// # Panics
///
/// Panics if the manifest's command is empty after passing
/// [`load_uds_command`] — the function checks for empty commands
/// and the `expect` here is a sanity check for that invariant.
#[allow(clippy::too_many_lines)]
pub async fn launch(
    driver: &str,
    adapters_path: &Path,
    ready_timeout: Duration,
) -> Result<DriverHandle, LauncherError> {
    let driver_dir = adapters_path.join(driver);
    let manifest_path = driver_dir.join("driver.yaml");
    let command = load_uds_command(&manifest_path)?;

    let socket = allocate_socket_path();
    if socket.exists() {
        let _ = std::fs::remove_file(&socket);
    }

    let mut full_argv: Vec<String> = command.clone();
    full_argv.push(format!("--socket={}", socket.display()));

    let (program, args) = full_argv
        .split_first()
        .expect("non-empty command checked above");

    let mut cmd = Command::new(program);
    cmd.args(args)
        .current_dir(&driver_dir)
        .env("SPECTRE_DRIVER_SOCKET", &socket)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(false); // we manage shutdown explicitly

    let mut child = cmd.spawn().map_err(LauncherError::Spawn)?;

    let stdout = child.stdout.take().expect("piped stdout");
    let stderr = child.stderr.take().expect("piped stderr");

    let stderr_tail: Arc<Mutex<Vec<String>>> = Arc::new(Mutex::new(Vec::new()));
    {
        let stderr_tail = stderr_tail.clone();
        tokio::spawn(async move {
            let mut lines = BufReader::new(stderr).lines();
            while let Ok(Some(line)) = lines.next_line().await {
                let mut guard = stderr_tail.lock().await;
                guard.push(line);
                if guard.len() > 50 {
                    let drop_n = guard.len() - 50;
                    guard.drain(..drop_n);
                }
            }
        });
    }

    let ready_re = Regex::new(READY_LINE_REGEX).expect("static regex");
    let mut stdout_lines = BufReader::new(stdout).lines();
    let ready_fut = async {
        while let Some(line) = stdout_lines.next_line().await? {
            debug!(target: "spectre::engine::launcher", "stdout: {line}");
            if ready_re.is_match(&line) {
                return Ok::<(), std::io::Error>(());
            }
        }
        Err(std::io::Error::new(
            std::io::ErrorKind::UnexpectedEof,
            "stdout closed before readiness line",
        ))
    };

    let outcome = tokio::select! {
        result = timeout(ready_timeout, ready_fut) => Some(result),
        status = child.wait() => {
            let stderr_tail = collect_tail(&stderr_tail).await;
            let _ = std::fs::remove_file(&socket);
            return Err(LauncherError::EarlyExit {
                status: format!("{status:?}"),
                stderr_tail,
            });
        }
    };

    match outcome {
        Some(Ok(Ok(()))) => Ok(DriverHandle {
            socket,
            child: Arc::new(Mutex::new(Some(child))),
            declared_capabilities: Vec::new(),
            shutdown_timeout: DEFAULT_SHUTDOWN_TIMEOUT,
            shutdown_done: Arc::new(std::sync::atomic::AtomicBool::new(false)),
        }),
        Some(Ok(Err(io_err))) => {
            let stderr_tail = collect_tail(&stderr_tail).await;
            // Best-effort: send SIGTERM and unlink.
            if let Some(pid) = child.id() {
                if let Ok(pid_i32) = i32::try_from(pid) {
                    let _ = kill(Pid::from_raw(pid_i32), Signal::SIGTERM);
                }
            }
            let _ = child.wait().await;
            let _ = std::fs::remove_file(&socket);
            Err(LauncherError::EarlyExit {
                status: io_err.to_string(),
                stderr_tail,
            })
        }
        Some(Err(_elapsed)) => {
            let stderr_tail = collect_tail(&stderr_tail).await;
            if let Some(pid) = child.id() {
                if let Ok(pid_i32) = i32::try_from(pid) {
                    let _ = kill(Pid::from_raw(pid_i32), Signal::SIGTERM);
                }
            }
            let _ = child.wait().await;
            let _ = std::fs::remove_file(&socket);
            Err(LauncherError::ReadinessTimeout {
                timeout_s: ready_timeout.as_secs(),
                stderr_tail,
            })
        }
        None => unreachable!(),
    }
}

async fn collect_tail(buf: &Arc<Mutex<Vec<String>>>) -> String {
    let guard = buf.lock().await;
    guard.join("\n")
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;
    use tempfile::TempDir;

    fn write_manifest(dir: &Path, transports_yaml: &str) -> PathBuf {
        let path = dir.join("driver.yaml");
        let yaml = format!(
            "name: mock\nversion: 0.0.0\nprotocol_version: spectre.driver.v1alpha1\n{transports_yaml}"
        );
        fs::write(&path, yaml).expect("write");
        path
    }

    #[test]
    fn loads_uds_command_from_manifest() {
        let tmp = TempDir::new().unwrap();
        let manifest = write_manifest(
            tmp.path(),
            "transports:\n  - kind: grpc-uds\n    command: [\"node\", \"dist/index.js\"]\n",
        );
        let cmd = load_uds_command(&manifest).expect("load");
        assert_eq!(cmd, vec!["node".to_string(), "dist/index.js".to_string()]);
    }

    #[test]
    fn rejects_manifest_without_uds_transport() {
        let tmp = TempDir::new().unwrap();
        let manifest = write_manifest(
            tmp.path(),
            "transports:\n  - kind: grpc-tcp\n    command: [\"x\"]\n",
        );
        match load_uds_command(&manifest) {
            Err(LauncherError::NoUdsTransport { .. }) => {}
            other => panic!("expected NoUdsTransport, got {other:?}"),
        }
    }

    #[test]
    fn rejects_empty_command() {
        let tmp = TempDir::new().unwrap();
        let manifest = write_manifest(
            tmp.path(),
            "transports:\n  - kind: grpc-uds\n    command: []\n",
        );
        match load_uds_command(&manifest) {
            Err(LauncherError::EmptyCommand { .. }) => {}
            other => panic!("expected EmptyCommand, got {other:?}"),
        }
    }

    #[test]
    fn allocates_unique_socket_paths() {
        let a = allocate_socket_path();
        let b = allocate_socket_path();
        assert_ne!(a, b);
        let s = a.to_string_lossy();
        assert!(s.starts_with("/tmp/spectre-engine-"), "{s}");
        assert!(s.ends_with(".sock"), "{s}");
    }

    /// Mock-adapter test: spawn a small shell script that emits the
    /// readiness line and waits on stdin. Verifies the launcher's
    /// happy path without depending on a real driver. macOS and Linux
    /// have `sh` and basic POSIX utilities available; Windows is out
    /// of scope (ADR-0008).
    #[tokio::test]
    async fn launches_mock_adapter_and_shuts_down() {
        let tmp = TempDir::new().unwrap();
        let driver_dir = tmp.path().join("mock-driver");
        fs::create_dir_all(&driver_dir).unwrap();

        // The script reads `--socket=<path>` from argv (last arg) and
        // prints the readiness line. It then sleeps until SIGTERM.
        let script = driver_dir.join("mock.sh");
        fs::write(
            &script,
            "#!/bin/sh\nfor a in \"$@\"; do\n  case \"$a\" in --socket=*) sock=\"${a#--socket=}\";; esac\ndone\necho \"ready unix:$sock\"\nwhile true; do sleep 1; done\n",
        )
        .unwrap();
        // chmod +x
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mut perm = fs::metadata(&script).unwrap().permissions();
            perm.set_mode(0o755);
            fs::set_permissions(&script, perm).unwrap();
        }

        write_manifest(
            &driver_dir,
            &format!(
                "transports:\n  - kind: grpc-uds\n    command: [\"{}\"]\n",
                script.display()
            ),
        );

        let handle = launch("mock-driver", tmp.path(), Duration::from_secs(5))
            .await
            .expect("launch should succeed");
        assert!(
            handle
                .socket_path()
                .to_string_lossy()
                .contains("spectre-engine-")
        );
        handle.shutdown().await;
    }

    #[tokio::test]
    async fn readiness_timeout_surfaces_stderr_tail() {
        let tmp = TempDir::new().unwrap();
        let driver_dir = tmp.path().join("slow-driver");
        fs::create_dir_all(&driver_dir).unwrap();

        // Script that prints to stderr but never to stdout. Readiness
        // detection must time out.
        let script = driver_dir.join("slow.sh");
        fs::write(
            &script,
            "#!/bin/sh\necho 'something on stderr' 1>&2\nwhile true; do sleep 1; done\n",
        )
        .unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mut perm = fs::metadata(&script).unwrap().permissions();
            perm.set_mode(0o755);
            fs::set_permissions(&script, perm).unwrap();
        }
        write_manifest(
            &driver_dir,
            &format!(
                "transports:\n  - kind: grpc-uds\n    command: [\"{}\"]\n",
                script.display()
            ),
        );

        match launch("slow-driver", tmp.path(), Duration::from_millis(500)).await {
            Err(LauncherError::ReadinessTimeout {
                timeout_s,
                stderr_tail,
            }) => {
                assert_eq!(timeout_s, 0); // 500ms rounds down
                assert!(stderr_tail.contains("something on stderr"));
            }
            other => panic!("expected ReadinessTimeout, got {other:?}"),
        }
    }
}
