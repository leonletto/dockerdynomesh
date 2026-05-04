#!/usr/bin/env bash
# Dry-run smoke for takeover-phase logic. Sources the function definitions
# in bootstrap.sh into a temp shell, stubs side-effecting commands, and
# asserts that takeover_setup() produces the expected .env mutations
# without touching /etc/resolver or invoking docker for real.
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRC="$SCRIPT_DIR/bootstrap.sh"

trap 'echo "FAIL line $LINENO" >&2; exit 1' ERR

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# Extract everything up to (but not including) the top-level case dispatcher.
# That gives us the function definitions and OS=$(...) without auto-running main.
awk '/^case "\$\{1:-up\}"/{exit} {print}' "$SRC" > "$TMP/prefix.sh"

run_case() {
  (
    cd "$TMP"
    set -e
    # Stub side-effecting commands before sourcing — exported so subshells
    # inside the sourced script see them. shellcheck SC2329: stubs are
    # invoked indirectly by the sourced script, not by this file.
    # shellcheck disable=SC2329
    sudo()   { shift 2>/dev/null || true; cat >/dev/null 2>&1 || true; }
    # shellcheck disable=SC2329
    tee()    { cat >/dev/null; }
    # shellcheck disable=SC2329
    docker() { :; }
    export -f sudo tee docker
    # shellcheck disable=SC1091
    . ./prefix.sh
    # Force macOS branch (the script's OS=$(uname -s) ran when sourced).
    # shellcheck disable=SC2034
    OS=Darwin
    "$@"
  )
}

# Test 1: parse a valid TAKEOVER_SUFFIXES into TAKEOVER_ADDRESS_FLAGS in .env.
cp "$SRC" "$TMP/bootstrap.sh"
: > "$TMP/.env"
TAKEOVER_SUFFIXES="orb.local,colima.local" \
TAKEOVER_DNS_PORT=5301 \
YES=1 \
  run_case takeover_setup

# Address flags emitted in alphabetical order (matches discoverer's
# parseTakeoverSuffixes; keeps .env diffs stable across input reorderings).
grep -q '^TAKEOVER_ADDRESS_FLAGS=--address=/colima.local/127.0.0.1 --address=/orb.local/127.0.0.1$' "$TMP/.env" \
  || { echo "FAIL: TAKEOVER_ADDRESS_FLAGS not as expected:"; cat "$TMP/.env"; exit 1; }

# Test 2: empty TAKEOVER_SUFFIXES is a no-op (does not append to .env).
: > "$TMP/.env"
TAKEOVER_SUFFIXES="" YES=1 run_case takeover_setup
[ ! -s "$TMP/.env" ] || { echo "FAIL: empty suffixes should leave .env empty"; cat "$TMP/.env"; exit 1; }

# Test 3: bash dedup matches Go-side parseTakeoverSuffixes — repeated entries
# collapse, whitespace is trimmed.
: > "$TMP/.env"
TAKEOVER_SUFFIXES=" orb.local , orb.local , colima.local " \
TAKEOVER_DNS_PORT=5302 \
YES=1 \
  run_case takeover_setup
grep -q '^TAKEOVER_ADDRESS_FLAGS=--address=/colima.local/127.0.0.1 --address=/orb.local/127.0.0.1$' "$TMP/.env" \
  || { echo "FAIL: dedup/whitespace handling unexpected:"; cat "$TMP/.env"; exit 1; }

# Test 4: invalid suffix is rejected at the door, no .env mutation.
: > "$TMP/.env"
if TAKEOVER_SUFFIXES="orb.local,BAD..NAME" YES=1 run_case takeover_setup 2>/dev/null; then
  echo "FAIL: takeover_setup should reject invalid suffix"
  exit 1
fi
[ ! -s "$TMP/.env" ] || { echo "FAIL: invalid suffix should leave .env untouched"; cat "$TMP/.env"; exit 1; }

# ---------------------------------------------------------------------------
# teardown_takeover tests
# ---------------------------------------------------------------------------
# teardown_takeover reads TAKEOVER_SUFFIXES from the environment (via load_env,
# which sources .env). We set it directly and also write a .env for load_env.

run_teardown() {
  (
    cd "$TMP"
    set -e
    # shellcheck disable=SC2329
    sudo()   { shift 2>/dev/null || true; cat >/dev/null 2>&1 || true; }
    # shellcheck disable=SC2329
    tee()    { cat >/dev/null; }
    # shellcheck disable=SC2329
    docker() { :; }
    export -f sudo tee docker
    # shellcheck disable=SC1091
    . ./prefix.sh
    # shellcheck disable=SC2034
    OS=Darwin
    # Override load_env to be a no-op — caller controls TAKEOVER_SUFFIXES directly.
    load_env() { :; }
    "$@"
  )
}

# Test 5: teardown_takeover with empty TAKEOVER_SUFFIXES exits 0, no side effects.
: > "$TMP/.env"
if ! TAKEOVER_SUFFIXES="" YES=1 run_teardown teardown_takeover 2>/dev/null; then
  echo "FAIL: teardown_takeover on empty TAKEOVER_SUFFIXES should exit 0"
  exit 1
fi

# Test 6: teardown_takeover with uppercase entries fails charset validation.
# valid_suffix() rejects uppercase — teardown should exit non-zero.
: > "$TMP/.env"
if TAKEOVER_SUFFIXES="orb.local,orb.local,ORB.LOCAL" YES=1 run_teardown teardown_takeover 2>/dev/null; then
  echo "FAIL: teardown_takeover should reject uppercase suffix ORB.LOCAL"
  exit 1
fi

# Test 7: teardown_takeover with bad..suffix rejects via valid_suffix().
: > "$TMP/.env"
if TAKEOVER_SUFFIXES="bad..suffix" YES=1 run_teardown teardown_takeover 2>/dev/null; then
  echo "FAIL: teardown_takeover should reject suffix 'bad..suffix'"
  exit 1
fi

# Test 8: teardown_takeover deduplicates valid repeated entries without error.
: > "$TMP/.env"
# /etc/resolver/orb.local does not exist in $TMP, so teardown will print
# "not present" for it — that is the expected no-op path for a dry run.
if ! TAKEOVER_SUFFIXES="orb.local,colima.local,orb.local" YES=1 run_teardown teardown_takeover 2>/dev/null; then
  echo "FAIL: teardown_takeover with valid dupes should exit 0"
  exit 1
fi

echo PASS
