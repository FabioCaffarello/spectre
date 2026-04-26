// SPDX-License-Identifier: Apache-2.0

//! JSONL streaming output sinks.
//!
//! Each `write_row` call serialises one row, writes it followed by a
//! `\n`, and flushes. A long-running job is visible in real time and
//! a panic preserves all rows written so far. See ADR-0012 §5.

use std::fs::{File, OpenOptions};
use std::io::{BufWriter, Write};
use std::path::{Path, PathBuf};

use crate::error::EngineError;

/// Streaming row-by-row writer.
pub trait OutputSink: Send {
    /// Serialise `row` as JSON and write it followed by a newline,
    /// then flush.
    ///
    /// # Errors
    ///
    /// Returns [`EngineError::Output`] on serialisation or I/O
    /// failure.
    fn write_row(&mut self, row: &serde_json::Value) -> Result<(), EngineError>;

    /// Flush any buffered output. Called once at the end of execution
    /// for paranoia; per-row writes already flush.
    ///
    /// # Errors
    ///
    /// Returns [`EngineError::Output`] on I/O failure.
    fn flush(&mut self) -> Result<(), EngineError>;
}

/// Writes JSONL rows to a file, flushing after each row.
pub struct JsonlFileSink {
    path: PathBuf,
    inner: BufWriter<File>,
}

impl JsonlFileSink {
    /// Create or truncate the file at `path` and prepare for writes.
    ///
    /// # Errors
    ///
    /// Returns [`EngineError::Io`] if the file cannot be created.
    pub fn create(path: &Path) -> Result<Self, EngineError> {
        if let Some(parent) = path.parent() {
            if !parent.as_os_str().is_empty() {
                std::fs::create_dir_all(parent)?;
            }
        }
        let file = OpenOptions::new()
            .create(true)
            .write(true)
            .truncate(true)
            .open(path)?;
        Ok(Self {
            path: path.to_path_buf(),
            inner: BufWriter::new(file),
        })
    }

    /// The path the sink is writing to.
    #[must_use]
    pub fn path(&self) -> &Path {
        &self.path
    }
}

impl OutputSink for JsonlFileSink {
    fn write_row(&mut self, row: &serde_json::Value) -> Result<(), EngineError> {
        write_row_to(&mut self.inner, row)
    }

    fn flush(&mut self) -> Result<(), EngineError> {
        self.inner
            .flush()
            .map_err(|e| EngineError::Output(e.to_string()))
    }
}

/// Writes JSONL rows to `stdout`, flushing after each row.
pub struct StdoutSink {
    inner: std::io::Stdout,
}

impl Default for StdoutSink {
    fn default() -> Self {
        Self::new()
    }
}

impl StdoutSink {
    /// Construct a new stdout sink.
    #[must_use]
    pub fn new() -> Self {
        Self {
            inner: std::io::stdout(),
        }
    }
}

impl OutputSink for StdoutSink {
    fn write_row(&mut self, row: &serde_json::Value) -> Result<(), EngineError> {
        let mut handle = self.inner.lock();
        write_row_to(&mut handle, row)
    }

    fn flush(&mut self) -> Result<(), EngineError> {
        self.inner
            .lock()
            .flush()
            .map_err(|e| EngineError::Output(e.to_string()))
    }
}

fn write_row_to<W: Write>(w: &mut W, row: &serde_json::Value) -> Result<(), EngineError> {
    serde_json::to_writer(&mut *w, row).map_err(|e| EngineError::Output(e.to_string()))?;
    w.write_all(b"\n")
        .map_err(|e| EngineError::Output(e.to_string()))?;
    w.flush().map_err(|e| EngineError::Output(e.to_string()))?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use tempfile::TempDir;

    #[test]
    fn jsonl_file_sink_writes_one_row_per_line() {
        let tmp = TempDir::new().unwrap();
        let path = tmp.path().join("out.jsonl");
        {
            let mut sink = JsonlFileSink::create(&path).unwrap();
            sink.write_row(&json!({ "a": 1 })).unwrap();
            sink.write_row(&json!({ "b": 2 })).unwrap();
            sink.flush().unwrap();
        }
        let s = std::fs::read_to_string(&path).unwrap();
        assert_eq!(s, "{\"a\":1}\n{\"b\":2}\n");
    }

    #[test]
    fn jsonl_file_sink_flushes_per_row_so_panics_preserve_prior_rows() {
        let tmp = TempDir::new().unwrap();
        let path = tmp.path().join("out.jsonl");
        let mut sink = JsonlFileSink::create(&path).unwrap();
        sink.write_row(&json!({ "first": 1 })).unwrap();
        // Read the file mid-write — content should already be visible.
        let s = std::fs::read_to_string(&path).unwrap();
        assert_eq!(s, "{\"first\":1}\n");
        sink.write_row(&json!({ "second": 2 })).unwrap();
        let s = std::fs::read_to_string(&path).unwrap();
        assert_eq!(s, "{\"first\":1}\n{\"second\":2}\n");
        // Drop without flush — per-row flush has already persisted.
        drop(sink);
        let s = std::fs::read_to_string(&path).unwrap();
        assert_eq!(s, "{\"first\":1}\n{\"second\":2}\n");
    }

    #[test]
    fn jsonl_file_sink_creates_parent_directories() {
        let tmp = TempDir::new().unwrap();
        let path = tmp.path().join("a/b/c/out.jsonl");
        let mut sink = JsonlFileSink::create(&path).unwrap();
        sink.write_row(&json!({ "x": "y" })).unwrap();
        let s = std::fs::read_to_string(&path).unwrap();
        assert_eq!(s, "{\"x\":\"y\"}\n");
    }
}
