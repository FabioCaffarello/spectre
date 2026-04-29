#!/usr/bin/env bash
# Devcontainer post-create bootstrap.
#
# Runs once on first container build. Idempotent: re-running is safe
# (per-language bootstraps detect already-installed dependencies; kind
# cluster creation skips when `spectre-dev` already exists). The script
# logs progress so the contributor can see where time is going.
#
# ADR-0018 (with §3a R6.3 evolution) explains the Devcontainer choice;
# ADR-0025 §6 R6.3 update records the addition of Docker-in-Docker, the
# kind cluster lifecycle, and the post-R6.3 unified Compose flow.
set -euo pipefail

cd "$(dirname "$0")/.."
echo "Spectre devcontainer bootstrap starting (cwd: $(pwd))"

echo
echo "[1/8] just bootstrap (proto generation + per-language deps)"
just bootstrap

echo
echo "[2/8] Playwright Chromium install"
just pw-install-browsers

echo
echo "[3/8] SeleniumBase ChromeDriver install"
just sb-install-chromedriver

echo
echo "[4/8] pre-commit hooks"
pre-commit install
pre-commit install --hook-type commit-msg

echo
echo "[5/8] kind cluster (R6.3 — ADR-0025 §6 R6.3 update)"
if kind get clusters 2>/dev/null | grep -q '^spectre-dev$'; then
  echo "kind cluster 'spectre-dev' already exists; skipping"
else
  just kind-up
fi

echo
echo "[6/8] CRDs into kind"
just crds-install

echo
echo "[7/8] sanity checks"
spectre_versions=$(core/engine/target/release/spectre version 2>/dev/null \
  || echo "spectre binary not yet built — run 'just spectre-build'")
echo "spectre: ${spectre_versions}"
echo "go:      $(go version | awk '{print $3}')"
echo "node:    $(node --version)"
echo "python:  $(python3 --version)"
echo "rustc:   $(rustc --version | awk '{print $2}')"
echo "kind:    $(kind --version)"
echo "kubectl: $(kubectl version --client 2>/dev/null | head -1)"

echo
echo "[8/8] Devcontainer ready."
echo "Try: just images && just compose-up"
