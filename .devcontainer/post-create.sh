#!/usr/bin/env bash
# Devcontainer post-create bootstrap.
#
# Runs once on first container build. Idempotent: re-running is safe
# (per-language bootstraps detect already-installed dependencies). The
# script logs progress so the contributor can see where time is going.
# ADR-0018 explains the Devcontainer choice and why this script lives
# here rather than in the image itself (per-clone state — Cargo.lock
# fetches, pnpm-lock.yaml installs, and uv venvs — should track the
# repo, not the image).
set -euo pipefail

cd "$(dirname "$0")/.."
echo "Spectre devcontainer bootstrap starting (cwd: $(pwd))"

echo
echo "[1/5] just bootstrap (proto generation + per-language deps)"
just bootstrap

echo
echo "[2/5] Playwright Chromium install"
just pw-install-browsers

echo
echo "[3/5] SeleniumBase ChromeDriver install"
just sb-install-chromedriver

echo
echo "[4/5] pre-commit hooks"
pre-commit install
pre-commit install --hook-type commit-msg

echo
echo "[5/5] sanity checks"
spectre_versions=$(core/engine/target/release/spectre version 2>/dev/null \
  || echo "spectre binary not yet built — run 'just spectre-build'")
echo "spectre: ${spectre_versions}"
echo "go:      $(go version)"
echo "node:    $(node --version)"
echo "python:  $(python3 --version)"
echo "rustc:   $(rustc --version)"

echo
echo "Devcontainer ready. Try: just check"
