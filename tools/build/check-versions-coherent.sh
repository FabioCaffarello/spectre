#!/usr/bin/env bash
#
# check-versions-coherent.sh — enforce that build/docker/versions.env,
# docker-bake.hcl variable defaults, and Dockerfile ARG defaults agree.
#
# Single executable contract for the toolchain-pin invariant documented
# in build/docker/README.md. Wired into `just check` (via the
# `check-versions` recipe) and CI's `proto` job (first step) so a
# version drift fails the build before any image rebuild attempts it.
#
# Exit code is the number of mismatches (0 = pass).
#
# Avoids bash 4+ features (associative arrays) so the script runs on
# macOS's default /usr/bin/env bash (3.2) as well as Linux.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

VERSIONS_ENV="build/docker/versions.env"
BAKE="docker-bake.hcl"

if [[ ! -f "$VERSIONS_ENV" ]]; then
    echo "ERROR: $VERSIONS_ENV not found" >&2
    exit 2
fi
if [[ ! -f "$BAKE" ]]; then
    echo "ERROR: $BAKE not found" >&2
    exit 2
fi

# Source versions.env so its KEY=VALUE lines populate shell variables.
# `set -a` exports each assignment so child checks see them too.
set -a
# shellcheck disable=SC1090
source "$VERSIONS_ENV"
set +a

MISMATCHES=0

note_pass() { printf '  %-46s ok\n' "$1"; }
note_fail() {
    printf '  %-46s MISMATCH (%s)\n' "$1" "$2"
    MISMATCHES=$((MISMATCHES + 1))
}

# Extract a bake variable's default. Returns the string between the
# enclosing quotes of `default = "..."` inside a `variable "<NAME>"`
# block. Empty string when the block does not exist.
bake_default() {
    local name="$1"
    awk -v name="$name" '
        $0 ~ "variable \"" name "\"" {in_block=1}
        in_block && /default[[:space:]]*=/ {
            match($0, /"[^"]*"$/)
            if (RSTART > 0) {
                v = substr($0, RSTART+1, RLENGTH-2)
                print v
                exit
            }
        }
        in_block && /^\}/ {in_block=0}
    ' "$BAKE"
}

# Extract `ARG <NAME>=<value>` from a Dockerfile. Empty string if the
# Dockerfile declares `ARG <NAME>` with no default, or omits the ARG
# entirely.
dockerfile_arg_default() {
    local file="$1" name="$2"
    awk -v name="$name" '
        $0 ~ "^ARG[[:space:]]+" name "=" {
            sub("^ARG[[:space:]]+" name "=", "", $0)
            # Strip a trailing comment, if any.
            sub("[[:space:]]*#.*$", "", $0)
            print $0
            exit
        }
    ' "$file"
}

# Detect whether a Dockerfile declares `ARG <NAME>` (with or without a
# default). Used for the buf-base.Dockerfile entry below: bake supplies
# the value via `args:`, so the ARG must be declared but is allowed to
# omit a default. Returns "yes" / "" (empty).
dockerfile_arg_declared() {
    local file="$1" name="$2"
    awk -v name="$name" '
        $0 ~ "^ARG[[:space:]]+" name "([[:space:]]|=|$)" { print "yes"; exit }
    ' "$file"
}

echo "checking versions.env <-> docker-bake.hcl variable defaults"

# Pins that have a corresponding bake variable. CHROME_VERSION is
# informational and intentionally not exposed through bake (see
# build/docker/README.md).
BAKE_PINS=(
    RUST_VERSION
    GO_VERSION
    NODE_VERSION
    PYTHON_VERSION
    PROTOC_VERSION
    BUF_VERSION
    UV_VERSION
    PLAYWRIGHT_VERSION
    CURL_IMPERSONATE_IMAGE
)

for name in "${BAKE_PINS[@]}"; do
    expected="${!name-}"
    if [[ -z "$expected" ]]; then
        note_fail "$name (bake)" "missing in versions.env"
        continue
    fi
    actual="$(bake_default "$name")"
    if [[ -z "$actual" ]]; then
        note_fail "$name (bake)" "no variable block in $BAKE"
    elif [[ "$actual" != "$expected" ]]; then
        note_fail "$name (bake)" "versions.env=$expected, $BAKE=$actual"
    else
        note_pass "$name (bake)"
    fi
done

echo
echo "checking versions.env <-> Dockerfile ARG defaults"

# (Dockerfile, ARG name) pairs that carry an inline default. The core
# Dockerfiles (engine, control-plane) declare ARG without defaults and
# rely on bake to inject the value; they are checked indirectly via
# the bake block above.
ARG_CHECKS=(
    "adapters/curl-impersonate/Dockerfile|GO_VERSION"
    "adapters/curl-impersonate/Dockerfile|BUF_VERSION"
    "adapters/curl-impersonate/Dockerfile|CURL_IMPERSONATE_IMAGE"
    "adapters/playwright/Dockerfile|NODE_VERSION"
    "adapters/playwright/Dockerfile|BUF_VERSION"
    "adapters/playwright/Dockerfile|PLAYWRIGHT_VERSION"
    "adapters/seleniumbase/Dockerfile|PYTHON_VERSION"
    "adapters/seleniumbase/Dockerfile|BUF_VERSION"
    "adapters/seleniumbase/Dockerfile|UV_VERSION"
)

for entry in "${ARG_CHECKS[@]}"; do
    file="${entry%%|*}"
    name="${entry##*|}"
    expected="${!name-}"
    label="$(printf '%s (%s)' "$name" "$file")"
    if [[ ! -f "$file" ]]; then
        note_fail "$label" "Dockerfile not found"
        continue
    fi
    actual="$(dockerfile_arg_default "$file" "$name")"
    if [[ -z "$actual" ]]; then
        note_fail "$label" "no ARG default"
    elif [[ "$actual" != "$expected" ]]; then
        note_fail "$label" "versions.env=$expected, ARG=$actual"
    else
        note_pass "$label"
    fi
done

# R6.5.4 — Dockerfiles where the ARG is declared without a default
# because bake supplies the value via per-target `args:`. The check
# verifies the ARG is declared (so bake's args propagate); a default
# is intentionally absent.
ARG_DECLARED_CHECKS=(
    "build/docker/buf-base.Dockerfile|BUF_VERSION"
)

for entry in "${ARG_DECLARED_CHECKS[@]}"; do
    file="${entry%%|*}"
    name="${entry##*|}"
    label="$(printf '%s (%s, declared)' "$name" "$file")"
    if [[ ! -f "$file" ]]; then
        note_fail "$label" "Dockerfile not found"
        continue
    fi
    if [[ -z "$(dockerfile_arg_declared "$file" "$name")" ]]; then
        note_fail "$label" "ARG $name not declared"
    else
        note_pass "$label"
    fi
done

echo
echo "checking docker-bake.hcl labels schema"

# Sanity: the labels function exists and emits the expected OCI keys.
# R6.5.2 may extend this with per-target invocation checks; for R6.5.1
# the canary is just "the schema is intact".
LABEL_KEYS=(
    "org.opencontainers.image.title"
    "org.opencontainers.image.description"
    "org.opencontainers.image.vendor"
    "org.opencontainers.image.source"
    "org.opencontainers.image.revision"
)

if ! grep -qE '^function "labels"' "$BAKE"; then
    note_fail "labels() function" "missing from $BAKE"
else
    for key in "${LABEL_KEYS[@]}"; do
        if grep -qF "\"$key\"" "$BAKE"; then
            note_pass "labels: $key"
        else
            note_fail "labels: $key" "key absent from $BAKE"
        fi
    done
fi

echo
if [[ "$MISMATCHES" -eq 0 ]]; then
    echo "RESULT: 0 mismatches"
    exit 0
fi
echo "RESULT: $MISMATCHES mismatch(es)"
exit "$MISMATCHES"
