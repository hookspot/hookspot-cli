#!/bin/bash
set -eu
set -o pipefail
umask 077

child_pid=
watcher_pid=

die() {
  echo "smoke: $1" >&2
  exit 1
}

usage() {
  echo "usage: scripts/smoke.sh --dist PARENT --environment stage|prod [--target OS/ARCH] [host provenance options]" >&2
  exit 2
}

normalize_os() {
  case "$1" in
    Darwin|darwin) echo darwin ;;
    Linux|linux) echo linux ;;
    *) echo unknown ;;
  esac
}

normalize_arch() {
  case "$1" in
    x86_64|amd64|AMD64) echo amd64 ;;
    arm64|aarch64|ARM64) echo arm64 ;;
    *) echo unknown ;;
  esac
}

valid_fact() {
  case "$1" in
    ''|*[!a-z0-9_-]*) return 1 ;;
  esac
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    die "a standard SHA-256 tool is required"
  fi
}

run_observation() {
  label=$1
  shift
  timeout_marker="$report_dir/$label.timeout.marker"
  : >"$report_dir/$label.stdout"
  : >"$report_dir/$label.stderr"
  env -i HOME="$isolated_home" USERPROFILE="$isolated_home" TMPDIR="$isolated_tmp" \
    PATH=/usr/bin:/bin:/usr/sbin:/sbin "$@" </dev/null >"$report_dir/$label.stdout" 2>"$report_dir/$label.stderr" &
  child_pid=$!
  (
    timer_pid=
    stop_timer() {
      if [ -n "$timer_pid" ]; then
        kill "$timer_pid" 2>/dev/null || true
        wait "$timer_pid" 2>/dev/null || true
      fi
      exit 0
    }
    trap stop_timer HUP INT TERM
    sleep 15 &
    timer_pid=$!
    wait "$timer_pid" 2>/dev/null || exit 0
    timer_pid=
    if kill -0 "$child_pid" 2>/dev/null; then
      : >"$timeout_marker"
      kill -TERM "$child_pid" 2>/dev/null || true
      sleep 1
      kill -KILL "$child_pid" 2>/dev/null || true
    fi
  ) &
  watcher_pid=$!
  set +e
  wait "$child_pid" 2>/dev/null
  observation_exit=$?
  set -e
  kill "$watcher_pid" 2>/dev/null || true
  wait "$watcher_pid" 2>/dev/null || true
  child_pid=
  watcher_pid=
  observation_timeout=false
  if [ -f "$timeout_marker" ]; then
    observation_exit=124
    observation_timeout=true
    rm "$timeout_marker"
  fi
}

dist=
environment=
target=
host_os=unknown
host_arch=unknown
host_kind=unknown
provenance=standalone
operator=unknown
procedure=unknown
execution=unknown
daemon_arch=unknown

while [ "$#" -gt 0 ]; do
  case "$1" in
    --dist) [ "$#" -ge 2 ] || usage; dist=$2; shift 2 ;;
    --environment) [ "$#" -ge 2 ] || usage; environment=$2; shift 2 ;;
    --target) [ "$#" -ge 2 ] || usage; target=$2; shift 2 ;;
    --host-os) [ "$#" -ge 2 ] || usage; host_os=$2; shift 2 ;;
    --host-arch) [ "$#" -ge 2 ] || usage; host_arch=$2; shift 2 ;;
    --host-kind) [ "$#" -ge 2 ] || usage; host_kind=$2; shift 2 ;;
    --provenance) [ "$#" -ge 2 ] || usage; provenance=$2; shift 2 ;;
    --operator) [ "$#" -ge 2 ] || usage; operator=$2; shift 2 ;;
    --procedure) [ "$#" -ge 2 ] || usage; procedure=$2; shift 2 ;;
    --execution) [ "$#" -ge 2 ] || usage; execution=$2; shift 2 ;;
    --daemon-arch) [ "$#" -ge 2 ] || usage; daemon_arch=$2; shift 2 ;;
    *) usage ;;
  esac
done

case "$environment" in stage|prod) ;; *) die "environment must be stage or prod" ;; esac
case "$dist" in /*) ;; *) die "dist must be an absolute retained output parent" ;; esac
[ -d "$dist" ] && [ ! -L "$dist" ] || die "dist is missing or unsafe"
dist=$(cd "$dist" && pwd -P)

collector_os=$(normalize_os "$(uname -s)")
collector_machine=${BASH_VERSINFO[5]%%-*}
collector_arch=$(normalize_arch "$collector_machine")
[ "$collector_os" != unknown ] && [ "$collector_arch" != unknown ] || die "collector platform is unsupported"
if [ -z "$target" ]; then
  target_os=$collector_os
  target_arch=$collector_arch
else
  case "$target" in */*) target_os=${target%/*}; target_arch=${target#*/} ;; *) die "target must be OS/ARCH" ;; esac
fi
case "$target_os" in darwin|linux) ;; *) die "Unix collector target must be darwin or linux" ;; esac
case "$target_arch" in amd64|arm64) ;; *) die "target architecture must be amd64 or arm64" ;; esac
[ "$target_os" = "$collector_os" ] || die "selected binary cannot run on this collector OS"
for fact in "$host_os" "$host_arch" "$host_kind" "$provenance" "$operator" "$procedure" "$execution" "$daemon_arch"; do
  valid_fact "$fact" || die "host provenance contains invalid data"
done
case "$host_kind" in physical|vm|unknown) ;; *) die "host kind is unsupported" ;; esac
case "$provenance" in operator|docker-daemon|standalone) ;; *) die "provenance is unsupported" ;; esac
case "$execution" in native|emulated|unknown) ;; *) die "execution must be native, emulated, or unknown" ;; esac

translated=unknown

receipt="$dist/receipt.json"
[ -f "$receipt" ] && [ ! -L "$receipt" ] || die "receipt is missing or unsafe"
archives=( "$dist"/artifacts/"hookspot_${environment}_"*"_${target_os}_${target_arch}.tar.gz" )
[ "${#archives[@]}" -eq 1 ] && [ -f "${archives[0]}" ] && [ ! -L "${archives[0]}" ] || die "expected exactly one matching retained archive"
archive=${archives[0]}
archive_name=$(basename "$archive")
binary_name=hookspot
[ "$environment" = stage ] && binary_name=hookspot-stage

report_root="$dist/native/reports/${target_os}-${target_arch}"
[ ! -L "$dist/native" ] && [ ! -L "$dist/native/reports" ] && [ ! -L "$report_root" ] || die "native report path is unsafe"
mkdir -p "$report_root"
report_dir=$(mktemp -d "$report_root/report.XXXXXX") || die "cannot allocate native report directory"
report_dir=$(cd "$report_dir" && pwd -P)
report_id=$(basename "$report_dir")
extract_dir=$(mktemp -d "$report_dir/extract.XXXXXX") || die "cannot allocate extraction directory"
cleanup() {
  if [ -n "${child_pid-}" ]; then
    kill -TERM "$child_pid" 2>/dev/null || true
    kill -KILL "$child_pid" 2>/dev/null || true
    wait "$child_pid" 2>/dev/null || true
  fi
  if [ -n "${watcher_pid-}" ]; then
    kill "$watcher_pid" 2>/dev/null || true
    wait "$watcher_pid" 2>/dev/null || true
  fi
  if [ -n "${extract_dir-}" ] && [ -d "$extract_dir" ]; then
    rm -rf "$extract_dir"
  fi
}
stop() {
  cleanup
  trap - HUP INT TERM EXIT
  exit 130
}
trap cleanup EXIT
trap stop HUP INT TERM

tar -xzf "$archive" -C "$extract_dir" "$binary_name" || die "cannot extract selected binary"
binary="$extract_dir/$binary_name"
[ -f "$binary" ] && [ ! -L "$binary" ] && [ -x "$binary" ] || die "extracted binary is missing or unsafe"
binary=$(cd "$extract_dir" && pwd -P)/$binary_name

receipt_sha=$(sha256_file "$receipt")
archive_sha=$(sha256_file "$archive")
binary_sha=$(sha256_file "$binary")
isolated_home="$extract_dir/home"
isolated_tmp="$extract_dir/tmp"
mkdir "$isolated_home" "$isolated_tmp"

run_observation version "$binary" version --json
version_exit=$observation_exit
version_timeout=$observation_timeout
run_observation help "$binary" --help
help_exit=$observation_exit
help_timeout=$observation_timeout

record_temp="$report_dir/.record.env.tmp"
cat >"$record_temp" <<EOF
SCHEMA_VERSION=1
REPORT_ID=$report_id
ENVIRONMENT=$environment
TARGET_OS=$target_os
TARGET_ARCH=$target_arch
COLLECTOR_OS=$collector_os
COLLECTOR_ARCH=$collector_arch
HOST_OS=$host_os
HOST_ARCH=$host_arch
HOST_KIND=$host_kind
PROVENANCE=$provenance
OPERATOR=$operator
PROCEDURE=$procedure
EXECUTION=$execution
DAEMON_ARCH=$daemon_arch
REQUESTED_PLATFORM=$target_os/$target_arch
COLLECTOR_TRANSLATED=$translated
RECEIPT_SHA256=$receipt_sha
ARCHIVE_NAME=$archive_name
ARCHIVE_SHA256=$archive_sha
BINARY_NAME=$binary_name
BINARY_SHA256=$binary_sha
VERSION_EXIT=$version_exit
VERSION_TIMEOUT=$version_timeout
VERSION_CAPTURE_FAILED=false
HELP_EXIT=$help_exit
HELP_TIMEOUT=$help_timeout
HELP_CAPTURE_FAILED=false
EOF
mv "$record_temp" "$report_dir/record.env"

cleanup
extract_dir=
echo "smoke report: $report_dir"
if [ "$version_exit" -ne 0 ] || [ "$version_timeout" != false ] || [ "$help_exit" -ne 0 ] || [ "$help_timeout" != false ]; then
  die "version or help observation failed; report retained"
fi
