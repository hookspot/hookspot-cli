#!/usr/bin/env bash
# Tests scripts/smoke.sh against locally built fixtures (no GitHub access):
# a stub hookspot binary packed the way GoReleaser packs a release archive.
# Runs on Linux and macOS; ci.yml runs it on ubuntu.
set -euo pipefail

cd "$(dirname "$0")/.."
smoke=scripts/smoke.sh
version=1.2.3

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "smoke_test: unsupported OS $(uname -s)" >&2; exit 2 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "smoke_test: unsupported architecture $(uname -m)" >&2; exit 2 ;;
esac
archive="hookspot_${version}_${os}_${arch}.tar.gz"
checksums="hookspot_${version}_checksums.txt"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failures=0

# Writes an executable that answers `version --json` with the given fields.
stub() {
  printf '#!/bin/sh\nprintf '"'"'%%s\\n'"'"' '"'"'{"version":"%s","build_kind":"%s","environment":"%s"}'"'"'\n' "$2" "$3" "$4" >"$1"
  chmod 0755 "$1"
}

sha256() {
  if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}

# Builds SMOKE_ASSETS_DIR contents in $1 for a stub reporting version $2.
assets() {
  mkdir -p "$1/pack"
  stub "$1/pack/hookspot" "$2" release prod
  tar -C "$1/pack" -czf "$1/$archive" hookspot
  (cd "$1" && sha256 "$archive" >"$checksums")
}

expect() {
  local name=$1 want=$2
  shift 2
  local status=0
  "$@" >"$work/output" 2>&1 || status=$?
  if [ "$status" -eq "$want" ]; then
    echo "ok   $name"
  else
    echo "FAIL $name: exit $status, want $want" >&2
    sed 's/^/     /' "$work/output" >&2
    failures=$((failures + 1))
  fi
}

assets "$work/good" "$version"
expect "release archive passes" 0 env SMOKE_ASSETS_DIR="$work/good" "$smoke" "$version"

assets "$work/wrong-version" 9.9.9
expect "binary reporting another version fails" 1 env SMOKE_ASSETS_DIR="$work/wrong-version" "$smoke" "$version"

cp -R "$work/good" "$work/corrupt"
sed -i.bak 's/^......../00000000/' "$work/corrupt/$checksums" && rm "$work/corrupt/$checksums.bak"
expect "corrupted checksum fails" 1 env SMOKE_ASSETS_DIR="$work/corrupt" "$smoke" "$version"

cp -R "$work/good" "$work/unlisted"
: >"$work/unlisted/$checksums"
expect "archive missing from the checksum file fails" 1 env SMOKE_ASSETS_DIR="$work/unlisted" "$smoke" "$version"

stub "$work/installed" "$version" release prod
expect "installed command passes" 0 "$smoke" "$version" "$work/installed"

stub "$work/snapshot" 0.0.0-snapshot.0123456 snapshot prod
expect "installed snapshot build fails" 1 "$smoke" "$version" "$work/snapshot"

printf '#!/bin/sh\nexit 3\n' >"$work/crashing" && chmod 0755 "$work/crashing"
expect "crashing command fails" 1 "$smoke" "$version" "$work/crashing"

expect "missing version is a usage error" 2 "$smoke"

[ "$failures" -eq 0 ] || exit 1
