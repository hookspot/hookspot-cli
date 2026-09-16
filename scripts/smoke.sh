#!/usr/bin/env bash
# Post-release smoke test, run by release.yml on ubuntu, macOS, and Windows
# (Git Bash). Downloads this machine's archive from the GitHub Release,
# verifies its SHA-256, and asserts the extracted binary reports VERSION as a
# prod release build. With COMMAND, only asserts that an already installed
# command (npm, Homebrew) reports the same.
set -euo pipefail

usage() {
  echo "usage: scripts/smoke.sh VERSION [COMMAND]" >&2
  exit 2
}

fail() {
  echo "smoke: $1" >&2
  exit 1
}

assert_version() {
  local output
  output=$("$1" version --json)
  echo "$output"
  jq -e --arg version "$version" \
    '.version == $version and .build_kind == "release" and .environment == "prod"' \
    <<<"$output" >/dev/null || fail "$1 does not report $version as a prod release build"
}

[ "$#" -ge 1 ] && [ "$#" -le 2 ] || usage
version=$1

if [ "$#" -eq 2 ]; then
  assert_version "$2"
  exit 0
fi

case "$(uname -s)" in
  Darwin) os=darwin; ext=tar.gz; binary=hookspot ;;
  Linux) os=linux; ext=tar.gz; binary=hookspot ;;
  MINGW* | MSYS*) os=windows; ext=zip; binary=hookspot.exe ;;
  *) fail "unsupported OS $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported architecture $(uname -m)" ;;
esac

archive="hookspot_${version}_${os}_${arch}.${ext}"
checksums="hookspot_${version}_checksums.txt"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# SMOKE_ASSETS_DIR: take the archive and checksum file from a local directory
# instead of the GitHub Release. Used only by the script's own tests.
if [ -n "${SMOKE_ASSETS_DIR:-}" ]; then
  cp "$SMOKE_ASSETS_DIR/$archive" "$SMOKE_ASSETS_DIR/$checksums" "$work"
else
  gh release download "v$version" --repo hookspot/hookspot-cli --dir "$work" \
    --pattern "$archive" --pattern "$checksums"
fi
cd "$work"

awk -v file="$archive" '$2 == file' "$checksums" >archive.sha256
[ -s archive.sha256 ] || fail "$archive is not listed in $checksums"
if command -v sha256sum >/dev/null; then
  sha256sum -c archive.sha256
else
  shasum -a 256 -c archive.sha256
fi

# Git Bash has no unzip, and its GNU tar does not read zip; 7-Zip is on the
# Windows runner's PATH.
if [ "$ext" = zip ]; then
  7z x -y "$archive" >/dev/null
else
  tar -xf "$archive"
fi

assert_version "./$binary"
