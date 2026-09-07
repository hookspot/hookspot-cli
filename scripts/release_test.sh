#!/bin/bash
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd -P)
SCRIPT="$ROOT/scripts/release.sh"
REAL_DOCKER=$(command -v docker)
TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/hookspot-release-test.XXXXXX")
TMP_ROOT=$(cd "$TMP_ROOT" && pwd -P)
cleanup_integration_container() {
  cidfile=${integration_cidfile-}
  [ -n "$cidfile" ] || return 0
  [ -f "$cidfile" ] && [ ! -L "$cidfile" ] || return 1
  [ "$(wc -c <"$cidfile" | tr -d ' ')" -eq 64 ] || return 1
  cid=$(cat "$cidfile")
  printf '%s\n' "$cid" | grep -Eq '^[0-9a-f]{64}$' || return 1
  if "$REAL_DOCKER" rm -f "$cid" >/dev/null 2>&1; then
    integration_cidfile=
    return 0
  fi
  if remaining=$("$REAL_DOCKER" ps -aq --no-trunc --filter "id=$cid" 2>/dev/null) && [ -z "$remaining" ]; then
    integration_cidfile=
    return 0
  fi
  return 1
}
repair_integration_ownership() {
  [ -n "${release_test_image_id-}" ] || return 0
  if [ -n "${integration_repo-}" ] && [ -d "$integration_repo" ]; then
    "$REAL_DOCKER" run --rm --network none \
      -e HTTP_PROXY= -e http_proxy= -e HTTPS_PROXY= -e https_proxy= -e NO_PROXY= -e no_proxy= \
      -e ALL_PROXY= -e all_proxy= -e FTP_PROXY= -e ftp_proxy= \
      -v "$integration_repo:/fixture" --entrypoint chown "$release_test_image_id" \
      -R "$(id -u):$(id -g)" /fixture >/dev/null || return 1
  fi
  if [ -n "${integration_evidence-}" ] && [ -d "$integration_evidence" ]; then
    "$REAL_DOCKER" run --rm --network none \
      -e HTTP_PROXY= -e http_proxy= -e HTTPS_PROXY= -e https_proxy= -e NO_PROXY= -e no_proxy= \
      -e ALL_PROXY= -e all_proxy= -e FTP_PROXY= -e ftp_proxy= \
      -v "$integration_evidence:/evidence" --entrypoint chown "$release_test_image_id" \
      -R "$(id -u):$(id -g)" /evidence >/dev/null || return 1
  fi
}
cleanup_test() {
  cleanup_failed=
  cleanup_integration_container || cleanup_failed=1
  repair_integration_ownership || true
  if [ -n "$cleanup_failed" ]; then
    echo "integration container cleanup is unconfirmed; inspect CID file: $integration_cidfile" >&2
  fi
  if [ "${KEEP_RELEASE_TEST_TEMP-}" = 1 ] || [ -n "$cleanup_failed" ]; then
    echo "release test files retained at $TMP_ROOT" >&2
  else
    rm -rf "$TMP_ROOT"
  fi
}
stop_for_signal() {
  status=$1
  trap - EXIT HUP INT TERM
  cleanup_test
  exit "$status"
}
trap cleanup_test EXIT
trap 'stop_for_signal 129' HUP
trap 'stop_for_signal 130' INT
trap 'stop_for_signal 143' TERM

fail() {
  echo "release test failed: $1" >&2
  exit 1
}

write_lock() {
  mkdir -p "$1/release"
  sed -n '1,200p' "$ROOT/release/toolchain.env" >"$1/release/toolchain.env"
}

expect_failure() {
  name=$1
  shift
  if "$@" >"$TMP_ROOT/$name.out" 2>"$TMP_ROOT/$name.err"; then
    fail "$name unexpectedly succeeded"
  fi
}

expect_success() {
  name=$1
  shift
  if ! "$@" >"$TMP_ROOT/$name.out" 2>"$TMP_ROOT/$name.err"; then
    sed -n '1,20p' "$TMP_ROOT/$name.err" >&2
    fail "$name unexpectedly failed"
  fi
}

case_dir="$TMP_ROOT/strict-lock"
mkdir -p "$case_dir/scripts"
write_lock "$case_dir"
cp "$SCRIPT" "$case_dir/scripts/release.sh"
chmod 755 "$case_dir/scripts/release.sh"
printf '%s\n' 'GO_VERSION=1.26.8' >>"$case_dir/release/toolchain.env"
expect_failure duplicate-lock "$case_dir/scripts/release.sh" _toolchain-value GO_IMAGE
test ! -s "$TMP_ROOT/duplicate-lock.out" || fail "invalid lock produced a value"

lock_case=0
for replacement in \
  'GO_VERSION=1..26.8' \
  'GH_VERSION=2.100' \
  'AIR_VERSION=v1.67.3;touch${IFS}SYNTHETIC_MARKER' \
  'GO_IMAGE=$(touch${IFS}SYNTHETIC_MARKER)golang:1.26.8@sha256:9d2f36f06329b2a141b9db99ffa32765cf695ee57b813ca29e245e8670bcbfff'
do
  lock_case=$((lock_case + 1))
  invalid_dir="$TMP_ROOT/invalid-lock-$lock_case"
  mkdir -p "$invalid_dir/scripts"
  write_lock "$invalid_dir"
  cp "$SCRIPT" "$invalid_dir/scripts/release.sh"
  chmod 755 "$invalid_dir/scripts/release.sh"
  key=${replacement%%=*}
  sed "s|^${key}=.*$|${replacement}|" "$invalid_dir/release/toolchain.env" >"$invalid_dir/release/changed.env"
  mv "$invalid_dir/release/changed.env" "$invalid_dir/release/toolchain.env"
  expect_failure "invalid-lock-$lock_case" "$invalid_dir/scripts/release.sh" _toolchain-value GO_IMAGE
  test ! -e "$invalid_dir/SYNTHETIC_MARKER" || fail "invalid lock data executed"
done

make_dir="$TMP_ROOT/make-lock"
mkdir -p "$make_dir/scripts" "$make_dir/release"
cp "$ROOT/Makefile" "$make_dir/Makefile"
cp "$SCRIPT" "$make_dir/scripts/release.sh"
chmod 755 "$make_dir/scripts/release.sh"
printf '%s\n' '$(shell touch INVALID_LOCK_EXECUTED)' >"$make_dir/release/toolchain.env"
expect_failure make-lock make -C "$make_dir" release-check ENV=stage
test ! -e "$make_dir/INVALID_LOCK_EXECUTED" || fail "Make evaluated toolchain data"

fixture="$TMP_ROOT/repository"
mkdir -p "$fixture/scripts" "$fixture/release" "$fixture/docker" "$fixture/tools/releasecheck" "$fixture/tools/releasebootstrap" "$fixture/fake-bin" "$fixture/fake-tools" "$fixture/.githooks"
cp "$SCRIPT" "$fixture/scripts/release.sh"
chmod 755 "$fixture/scripts/release.sh"
write_lock "$fixture"
cp "$ROOT/docker/release.Dockerfile" "$fixture/docker/release.Dockerfile"
cp "$ROOT/.goreleaser.yaml" "$fixture/.goreleaser.yaml"
cp "$ROOT/Makefile" "$fixture/Makefile"
cp "$ROOT/tools/releasebootstrap/main.go" "$fixture/tools/releasebootstrap/main.go"
printf '%s\n' '{"schema_version":1,"repository":"example/release-fixture","stage":{"server_url":"https://stage.example.invalid","branch":"stage"},"prod":{"server_url":"https://prod.example.invalid","branch":"main"}}' >"$fixture/release/environments.json"
printf '%s\n' 'package main' >"$fixture/tools/releasecheck/main.go"
printf '%s\n' 'fixture' >"$fixture/payload"
printf '%s\n' '/dist/' >"$fixture/.gitignore"
printf '%s\n' 'payload filter=observe' >"$fixture/.gitattributes"
printf '%s\n' '#!/bin/sh' 'test -z "${HOST_BOUNDARY_SENTINEL-}" || touch "$HOST_BOUNDARY_MARKER"' >"$fixture/.githooks/post-checkout"
chmod 755 "$fixture/.githooks/post-checkout"
printf '%s\n' '#!/bin/sh' 'test -z "${HOST_BOUNDARY_SENTINEL-}" || touch "$HOST_BOUNDARY_MARKER"' 'cat' >"$fixture/scripts/filter-observe"
chmod 755 "$fixture/scripts/filter-observe"
printf '%s\n' '#!/bin/sh' 'test -z "${HOST_BOUNDARY_SENTINEL-}" || touch "$HOST_BOUNDARY_MARKER"' "printf 'token\\0'" >"$fixture/scripts/fsmonitor-observe"
chmod 755 "$fixture/scripts/fsmonitor-observe"
cat >"$fixture/fake-tools/go" <<'EOF'
#!/bin/sh
printf 'go version go1.26.8 %s\n' "${FAKE_IMAGE_PLATFORM:-linux/amd64}"
[ "${FAKE_GO_FAILURE-}" != 1 ] || exit 17
EOF
cat >"$fixture/fake-tools/goreleaser" <<'EOF'
#!/bin/sh
printf '%s\n' 'GitVersion:    2.17.1'
EOF
cat >"$fixture/fake-tools/gh" <<'EOF'
#!/bin/sh
printf '%s\n' 'gh version 2.100.0 (fixture)'
EOF
chmod 755 "$fixture/fake-tools/go" "$fixture/fake-tools/goreleaser" "$fixture/fake-tools/gh"
cat >"$fixture/fake-bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_DOCKER_LOG"
fake_platform=${FAKE_IMAGE_PLATFORM:-linux/amd64}
if [ "$1 $2" = "image inspect" ]; then
  case "$*" in
    *lock-sha256*) if [ "${FAKE_WRONG_LABEL-}" = 1 ]; then printf '%064d\n' 0 | tr 0 c; else printf '%064d\n' 0 | tr 0 a; fi ;;
    *recipe-sha256*) printf '%064d\n' 0 | tr 0 b ;;
    *bootstrap-sha256*) if [ "${FAKE_WRONG_BOOTSTRAP_LABEL-}" = 1 ]; then printf '%064d\n' 0 | tr 0 d; else printf '%064d\n' 0 | tr 0 c; fi ;;
    *) printf 'sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56 %s\n' "$fake_platform" ;;
  esac
  exit 0
fi
if [ "$1" = version ]; then
  if [ "${FAKE_WRONG_PLATFORM-}" = 1 ]; then
    if [ "$fake_platform" = linux/amd64 ]; then printf '%s\n' linux/arm64; else printf '%s\n' linux/amd64; fi
  else
    printf '%s\n' "$fake_platform"
  fi
  exit 0
fi
case "$*" in
  *'toolchain.env'*'sha256sum'*)
    if [ "${FAKE_RETAINED_LOCK_MISMATCH-}" = 1 ]; then
      case "$*" in *'/retained/controls/release/toolchain.env:'*) printf '%064d  /input\n' 0 | tr 0 d; exit 0 ;; esac
    fi
    printf '%064d  /input\n' 0 | tr 0 a
    ;;
  *'release.Dockerfile'*'sha256sum'*) printf '%064d  /input\n' 0 | tr 0 b ;;
  *'releasebootstrap/main.go'*'sha256sum'*) printf '%064d  /input\n' 0 | tr 0 c ;;
  *'go version; goreleaser --version; gh --version'*)
    if [ "${FAKE_EXECUTE_COMPOUND-}" = 1 ]; then
      while [ "$#" -gt 0 ] && [ "$1" != -c ]; do shift; done
      [ "$#" -ge 2 ] || exit 91
      PATH="$FAKE_COMPOUND_TOOLS_DIR:$PATH" /bin/sh -c "$2"
    elif [ "${FAKE_WRONG_VERSIONS-}" = 1 ]; then
      printf 'go version go1.26.80 %s\n%s\n%s\n' "$fake_platform" 'goreleaser version 2.17.10' 'gh version 2.100.00 (fixture)'
    else
      printf 'go version go1.26.8 %s\n%s\n%s\n' "$fake_platform" 'goreleaser version 2.17.1' 'gh version 2.100.0 (fixture)'
    fi
    ;;
  *'--entrypoint release-helper-check'*) [ "${FAKE_HELPER_INVALID-}" != 1 ] || exit 23 ;;
  *'--entrypoint /out/tools/releasecheck-linux-'*) touch "$FAKE_RETAINED_HELPER_MARKER" ;;
  *'env-url'*)
    if [ -n "${FAKE_DIRTY_SOURCE-}" ]; then printf '%s\n' dirty >>"$FAKE_DIRTY_SOURCE"; fi
    printf '%s\n' 'https://stage.example.invalid'
    ;;
  *'releasecheck repository'*) printf '%s\n' 'example/release-fixture' ;;
  *'goreleaser release'*)
    [ "${FAKE_BUILD_FAILURE-}" != 1 ] || exit 19
    ;;
esac
EOF
chmod 755 "$fixture/fake-bin/docker"
REAL_GIT=$(command -v git)
git_log="$TMP_ROOT/git.log"
cat >"$fixture/fake-bin/git" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >>"$git_log"
if [ -f "$TMP_ROOT/source-temp-retarget-request" ] && [ ! -e "$TMP_ROOT/source-temp-retarget-done" ]; then
  : >"$TMP_ROOT/source-temp-retarget-done"
  rm -f "$TMP_ROOT/source-temp-alias"
  ln -s "$TMP_ROOT/source-temp-victim" "$TMP_ROOT/source-temp-alias"
fi
case "\$*" in
  *'status --porcelain'*)
    if [ -n "\${GITHUB_TOKEN-}\${GH_TOKEN-}" ]; then printf '%s\\n' 'status received publisher token' >>"$git_log"; fi
    if [ "\${FAKE_GIT_STATUS_FAILURE-}" = 1 ]; then exit 42; fi
    ;;
esac
exec "$REAL_GIT" "\$@"
EOF
chmod 755 "$fixture/fake-bin/git"
REAL_MKTEMP=$(command -v mktemp)
cat >"$fixture/fake-bin/mktemp" <<EOF
#!/bin/sh
set -eu
result=\$("$REAL_MKTEMP" "\$@")
case "\$*" in
  *hookspot-release-source.*)
    resolved=\$(cd "\$result" && pwd -P)
    if [ "\${SOURCE_TEMP_RETARGET-}" = 1 ]; then
      child=\$(basename "\$resolved")
      sentinel_dir="$TMP_ROOT/source-temp-victim/\$child"
      mkdir -p "\$sentinel_dir"
      printf '%s\n' source-temp-sentinel >"\$sentinel_dir/sentinel"
      printf '%s\n' "\$resolved" >"$TMP_ROOT/source-temp-record"
      printf '%s\n' "\$sentinel_dir/sentinel" >"$TMP_ROOT/source-temp-sentinel-record"
      : >"$TMP_ROOT/source-temp-retarget-request"
    fi
    ;;
esac
printf '%s\n' "\$result"
EOF
chmod 755 "$fixture/fake-bin/mktemp"

printf '%s\n' 'test -z "${HOST_BOUNDARY_SENTINEL-}" || touch "$HOST_BOUNDARY_MARKER"' >>"$fixture/scripts/release.sh"
(cd "$fixture" && "$REAL_GIT" init -q && "$REAL_GIT" config user.email fixture@example.invalid && "$REAL_GIT" config user.name Fixture && "$REAL_GIT" add . && "$REAL_GIT" commit -qm initial)
cp "$SCRIPT" "$fixture/scripts/release.sh"
chmod 755 "$fixture/scripts/release.sh"
printf '%s\n' second >>"$fixture/payload"
(cd "$fixture" && "$REAL_GIT" add scripts/release.sh payload && "$REAL_GIT" commit -qm second)
"$REAL_GIT" -C "$fixture" config core.fsmonitor "$fixture/scripts/fsmonitor-observe"

source_temp_root="$TMP_ROOT/source-temp-root"
source_temp_victim="$TMP_ROOT/source-temp-victim"
source_temp_alias="$TMP_ROOT/source-temp-alias"
source_temp_record="$TMP_ROOT/source-temp-record"
source_temp_sentinel_record="$TMP_ROOT/source-temp-sentinel-record"
mkdir "$source_temp_root" "$source_temp_victim"
ln -s "$source_temp_root" "$source_temp_alias"
docker_log="$TMP_ROOT/docker.log"
: >"$docker_log"
: >"$git_log"
expect_success source-temp-symlink-retarget env PATH="$fixture/fake-bin:$PATH" TMPDIR="$source_temp_alias" \
  SOURCE_TEMP_RETARGET=1 \
  FAKE_DOCKER_LOG="$docker_log" FAKE_IMAGE_PLATFORM=linux/arm64 \
  "$fixture/scripts/release.sh" snapshot --environment stage --ref HEAD^
source_temp_physical=$(sed -n '1p' "$source_temp_record")
test -n "$source_temp_physical" || fail "source temp regression did not record physical path"
test ! -e "$source_temp_physical" || fail "source temp physical directory was not cleaned"
grep -Fq "$source_temp_physical/" "$git_log" || fail "Git did not use the physical source temp path"
grep -Fq "$source_temp_physical/" "$docker_log" || fail "Docker did not use the physical source temp path"
if grep -Fq "$source_temp_alias/" "$git_log" "$docker_log"; then
  fail "source temp retained a symlinked path spelling"
fi
source_temp_sentinel=$(sed -n '1p' "$source_temp_sentinel_record")
test -n "$source_temp_sentinel" || fail "source temp regression did not record victim sentinel"
grep -Fqx source-temp-sentinel "$source_temp_sentinel" || fail "TMPDIR retarget damaged the victim"


docker_log="$TMP_ROOT/docker.log"
: >"$git_log"
status_git_before=$(wc -l <"$git_log" | tr -d ' ')
: >"$docker_log"
expect_failure status-missing-token env -u GITHUB_TOKEN -u GH_TOKEN PATH="$fixture/fake-bin:$PATH" \
  FAKE_DOCKER_LOG="$docker_log" "$fixture/scripts/release.sh" status --environment stage --tag v1.2.3-stage.1
grep -Fq 'status requires a read-capable GITHUB_TOKEN' "$TMP_ROOT/status-missing-token.err" || fail "status missing-token diagnostic changed"
test ! -s "$docker_log" || fail "status without token reached Docker"
status_git_after=$(wc -l <"$git_log" | tr -d ' ')
test "$status_git_before" = "$status_git_after" || fail "status without token reached Git"
marker="$TMP_ROOT/host-boundary-marker"
PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" FAKE_EXECUTE_COMPOUND=1 FAKE_COMPOUND_TOOLS_DIR="$fixture/fake-tools" \
  "$fixture/scripts/release.sh" tools >"$TMP_ROOT/tools.out"
for fake_platform in linux/amd64 linux/arm64; do
  fake_arch=${fake_platform#linux/}
  expect_success "tools-identity-$fake_arch" env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" \
    FAKE_IMAGE_PLATFORM="$fake_platform" "$fixture/scripts/release.sh" _tools-identity
  grep -Eq "^sha256:[0-9a-f]{64}[[:space:]]+$fake_platform$" "$TMP_ROOT/tools-identity-$fake_arch.out" || \
    fail "validated $fake_arch tools identity was not machine-readable"
done
grep -q -- '--build-arg HTTP_PROXY= --build-arg http_proxy=' "$docker_log" || fail "Docker build proxy arguments were not cleared"
expect_failure wrong-tool-versions env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" FAKE_WRONG_VERSIONS=1 "$fixture/scripts/release.sh" tools
expect_failure early-tool-exit env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" FAKE_EXECUTE_COMPOUND=1 FAKE_COMPOUND_TOOLS_DIR="$fixture/fake-tools" FAKE_GO_FAILURE=1 "$fixture/scripts/release.sh" tools
expect_failure wrong-image-label env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" FAKE_WRONG_LABEL=1 "$fixture/scripts/release.sh" tools
expect_failure wrong-bootstrap-label env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" FAKE_WRONG_BOOTSTRAP_LABEL=1 "$fixture/scripts/release.sh" tools
expect_failure wrong-image-platform env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" FAKE_WRONG_PLATFORM=1 "$fixture/scripts/release.sh" tools
: >"$docker_log"
PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" FAKE_GIT_LOG="$git_log" \
  HOST_BOUNDARY_SENTINEL=present HOST_BOUNDARY_MARKER="$marker" \
  GIT_CONFIG_COUNT=2 GIT_CONFIG_KEY_0=core.hooksPath GIT_CONFIG_VALUE_0=.githooks \
  GIT_CONFIG_KEY_1=filter.observe.smudge GIT_CONFIG_VALUE_1="$fixture/scripts/filter-observe" \
  GITHUB_TOKEN=TOKEN_BOUNDARY_SENTINEL GH_TOKEN=TOKEN_BOUNDARY_SENTINEL \
  HOOKSPOT_DEV_CLI_KEY=TOKEN_BOUNDARY_SENTINEL \
  "$fixture/scripts/release.sh" snapshot --environment stage --ref HEAD^ >"$TMP_ROOT/snapshot.out"
test ! -e "$marker" || fail "selected host script executed"
grep -q 'release output:' "$TMP_ROOT/snapshot.out" || fail "snapshot did not report retained output"
grep -q -- '-e HTTP_PROXY= -e http_proxy=' "$docker_log" || fail "container proxy environment was not cleared"
if grep -q 'TOKEN_BOUNDARY_SENTINEL' "$docker_log"; then
  fail "container arguments included an inherited token"
fi
while IFS= read -r docker_call; do
  case "$docker_call" in
    run*)
      for cleared in HTTP_PROXY http_proxy HTTPS_PROXY https_proxy NO_PROXY no_proxy ALL_PROXY all_proxy FTP_PROXY ftp_proxy; do
        case "$docker_call" in *"-e $cleared="*) ;; *) fail "container launch did not clear $cleared" ;; esac
      done
      case "$docker_call" in *'-e GITHUB_TOKEN'*|*'-e GH_TOKEN'*|*'-e HOOKSPOT_'*) fail "container launch forwards credential variables" ;; esac
      ;;
  esac
done <"$docker_log"
if grep -Eq '(^| )((tag|push|fetch)( |$))' "$git_log"; then
  fail "nonpublishing snapshot attempted a remote or tag mutation"
fi
grep -q 'remote add origin https://github.com/example/release-fixture.git' "$git_log" || fail "disposable source did not retain its public producer origin"

"$REAL_GIT" -C "$fixture" rm -q tools/releasebootstrap/main.go
(cd "$fixture" && "$REAL_GIT" commit -qm 'old interface without helper checker')
old_source_commit=$("$REAL_GIT" -C "$fixture" rev-parse HEAD)
mkdir -p "$fixture/tools/releasebootstrap"
cp "$ROOT/tools/releasebootstrap/main.go" "$fixture/tools/releasebootstrap/main.go"
(cd "$fixture" && "$REAL_GIT" add tools/releasebootstrap/main.go && "$REAL_GIT" commit -qm 'restore current release tooling')
: >"$docker_log"
expect_failure old-source env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" "$fixture/scripts/release.sh" snapshot --environment stage --ref "$old_source_commit"
test ! -s "$docker_log" || fail "unsupported selected source reached Docker"

retained="$TMP_ROOT/retained"
mkdir -p "$retained/controls/release" "$retained/tools"
retained=$(cd "$retained" && pwd -P)
cp "$fixture/release/toolchain.env" "$retained/controls/release/toolchain.env"
retained_marker="$TMP_ROOT/retained-helper-ran"
expect_failure substituted-helper env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" FAKE_HELPER_INVALID=1 FAKE_RETAINED_HELPER_MARKER="$retained_marker" \
  "$fixture/scripts/release.sh" verify --environment stage --dist "$retained"
test ! -e "$retained_marker" || fail "substituted retained helper executed"
if grep -q 'release output is intact' "$TMP_ROOT/substituted-helper.out"; then
  fail "substituted retained helper reported intact output"
fi
expect_failure retained-lock env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" FAKE_RETAINED_LOCK_MISMATCH=1 FAKE_RETAINED_HELPER_MARKER="$retained_marker" \
  "$fixture/scripts/release.sh" verify --environment stage --dist "$retained"
test ! -e "$retained_marker" || fail "unsupported retained lock reached helper execution"
for fake_platform in linux/amd64 linux/arm64; do
  fake_arch=${fake_platform#linux/}
  rm -f "$retained_marker"
  : >"$docker_log"
  PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" FAKE_RETAINED_HELPER_MARKER="$retained_marker" \
    FAKE_IMAGE_PLATFORM="$fake_platform" "$fixture/scripts/release.sh" verify --environment stage --dist "$retained" \
    >"$TMP_ROOT/valid-helper-$fake_arch.out"
  test -e "$retained_marker" || fail "validated $fake_arch retained helper did not execute"
  grep -q -- "--entrypoint /out/tools/releasecheck-linux-$fake_arch" "$docker_log" || \
    fail "$fake_arch retained helper was not selected from the image platform"
  grep -q 'release output is intact' "$TMP_ROOT/valid-helper-$fake_arch.out" || \
    fail "validated $fake_arch output was not reported intact"
done

make_marker="$TMP_ROOT/make-transport-marker"
expect_failure make-dist env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" make -C "$fixture" release-verify ENV=stage "DIST=/tmp/retained\"; touch $make_marker; #"
test ! -e "$make_marker" || fail "Make executed DIST data"
make_payload="\"; touch $make_marker; #"
expect_failure make-env env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" make -C "$fixture" release-check "ENV=$make_payload"
expect_failure make-ref env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" make -C "$fixture" release-snapshot ENV=stage "REF=$make_payload"
expect_failure make-tag env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" make -C "$fixture" release-build ENV=stage "TAG=$make_payload"
expect_failure make-notes env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" make -C "$fixture" stage-release TAG=v0.1.0-stage.1 "NOTES_FILE=$make_payload"
expect_failure make-stage-inputs env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" make -C "$fixture" prod-release TAG=v0.1.0 "FROM_STAGE_TAG=$make_payload" "STAGE_ACCEPTANCE=$make_payload"
expect_failure make-function env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" make -C "$fixture" release-verify ENV=stage 'DIST=$(shell touch MAKE_FUNCTION_EXECUTED)'
test ! -e "$make_marker" || fail "Make executed operator data"
test ! -e "$fixture/MAKE_FUNCTION_EXECUTED" || fail "Make evaluated an original command-line variable while exporting it"

clean_payload="$TMP_ROOT/clean-payload"
cp "$fixture/payload" "$clean_payload"
dirty_notes="$TMP_ROOT/dirty-release-notes.md"
printf '%s\n' 'Fixture release notes' >"$dirty_notes"
for dirty_kind in unstaged staged untracked; do
  case "$dirty_kind" in
    unstaged) printf '%s\n' dirty >>"$fixture/payload" ;;
    staged)
      printf '%s\n' dirty >>"$fixture/payload"
      "$REAL_GIT" -C "$fixture" add payload
      ;;
    untracked) printf '%s\n' dirty >"$fixture/untracked-change" ;;
  esac
  refs_before=$("$REAL_GIT" -C "$fixture" for-each-ref --format='%(refname) %(objectname)')
  for mutation in tools snapshot build publish resume acceptance-template native-requirements native-review-template native-manual native-evidence; do
    set -- "$fixture/scripts/release.sh" "$mutation"
    case "$mutation" in
      tools) ;;
      snapshot) set -- "$@" --environment stage ;;
      build) set -- "$@" --environment stage --tag v0.2.0-stage.1 ;;
      publish) set -- "$@" --environment stage --tag v0.2.0-stage.1 --notes "$dirty_notes" --dist "$retained" ;;
      resume) set -- "$@" --environment stage --tag v0.2.0-stage.1 --dist "$retained" ;;
      acceptance-template) set -- "$@" --dist "$retained" --output "$TMP_ROOT/blocked-acceptance.json" ;;
      native-review-template) set -- "$@" --environment stage --dist "$retained" --baseline-dist "$retained" --output "$TMP_ROOT/blocked-review.json" ;;
      native-manual) set -- "$@" --environment stage --dist "$retained" --target linux/amd64 --check network --result pass --operator fixture --procedure network_release_v1 ;;
      *) set -- "$@" --environment stage --dist "$retained" ;;
    esac
    : >"$docker_log"
    : >"$git_log"
    expect_failure "$dirty_kind-$mutation" env PATH="$fixture/fake-bin:$PATH" \
      FAKE_DOCKER_LOG="$docker_log" GITHUB_TOKEN=TOKEN_BOUNDARY_SENTINEL GH_TOKEN=TOKEN_BOUNDARY_SENTINEL "$@"
    grep -Fq 'uncommitted changes' "$TMP_ROOT/$dirty_kind-$mutation.err" || fail "$mutation did not explain the $dirty_kind checkout"
    test ! -s "$docker_log" || fail "$mutation reached Docker with $dirty_kind changes"
    if grep -Fq 'status received publisher token' "$git_log"; then fail "$mutation exposed publisher credentials to Git status"; fi
    test ! -e "$fixture/.git/hookspot-publication.lock" || fail "$mutation created a lock with $dirty_kind changes"
    test "$("$REAL_GIT" -C "$fixture" for-each-ref --format='%(refname) %(objectname)')" = "$refs_before" || fail "$mutation changed refs with $dirty_kind changes"
  done
  for mutation in build tidy get release-tools release-snapshot release-build stage-release prod-release release-resume; do
    mutation_environment=stage
    mutation_tag=v0.2.0-stage.1
    if [ "$mutation" = prod-release ]; then mutation_environment=prod; mutation_tag=v0.2.0; fi
    : >"$docker_log"
    : >"$git_log"
    expect_failure "$dirty_kind-make-$mutation" env PATH="$fixture/fake-bin:$PATH" \
      FAKE_DOCKER_LOG="$docker_log" GITHUB_TOKEN=TOKEN_BOUNDARY_SENTINEL GH_TOKEN=TOKEN_BOUNDARY_SENTINEL \
      make -C "$fixture" "$mutation" ENV="$mutation_environment" TAG="$mutation_tag" \
      FROM_STAGE_TAG=v0.2.0-stage.1 STAGE_ACCEPTANCE="$dirty_notes" \
      NOTES_FILE="$dirty_notes" DIST="$retained" SERVER_URL=https://stage.example.invalid PKG=example.invalid/module
    grep -Fq 'uncommitted changes' "$TMP_ROOT/$dirty_kind-make-$mutation.err" || fail "make $mutation did not explain the $dirty_kind checkout"
    test ! -s "$docker_log" || fail "make $mutation reached Docker with $dirty_kind changes"
    if grep -Fq 'status received publisher token' "$git_log"; then fail "make $mutation exposed publisher credentials to Git status"; fi
  done
  expect_success "$dirty_kind-check" env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" \
    "$fixture/scripts/release.sh" check --environment stage
  expect_success "$dirty_kind-verify" env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" \
    FAKE_RETAINED_HELPER_MARKER="$retained_marker" "$fixture/scripts/release.sh" verify --environment stage --dist "$retained"
  case "$dirty_kind" in
    staged) "$REAL_GIT" -C "$fixture" reset -q HEAD -- payload ;;
    untracked) rm "$fixture/untracked-change" ;;
  esac
  cp "$clean_payload" "$fixture/payload"
done
test ! -e "$TMP_ROOT/blocked-acceptance.json" || fail "dirty checkout created stage acceptance"
test ! -e "$TMP_ROOT/blocked-review.json" || fail "dirty checkout created a native review"

for mutation in build tidy get; do
  : >"$docker_log"
  expect_success "clean-make-$mutation" env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" \
    make -C "$fixture" "$mutation" SERVER_URL=https://stage.example.invalid PKG=example.invalid/module
  expected_operation="go $mutation"
  [ "$mutation" != tidy ] || expected_operation='go mod tidy'
  grep -Fq "$expected_operation" "$docker_log" || fail "clean make $mutation did not reach its operation"
done

for tag_environment in stage prod; do
  created_tag=v0.2.0
  [ "$tag_environment" != stage ] || created_tag=v0.2.0-stage.1
  expected_commit=$("$REAL_GIT" -C "$fixture" rev-parse HEAD)
  : >"$docker_log"
  : >"$git_log"
  expect_success "create-$tag_environment-tag" env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" \
    make -C "$fixture" release-build ENV="$tag_environment" TAG="$created_tag"
  test "$("$REAL_GIT" -C "$fixture" cat-file -t "refs/tags/$created_tag")" = tag || fail "new $tag_environment tag was not annotated"
  test "$("$REAL_GIT" -C "$fixture" rev-parse "$created_tag^{commit}")" = "$expected_commit" || fail "new tag did not select committed HEAD"
  grep -Fq "$created_tag ($expected_commit)" "$TMP_ROOT/create-$tag_environment-tag.out" || fail "build did not identify the selected tag and commit"
  if grep -Eq '(^| )(push|fetch|ls-remote)( |$)' "$git_log"; then fail "build contacted a remote"; fi
  tag_object_before=$("$REAL_GIT" -C "$fixture" rev-parse "refs/tags/$created_tag")
  expect_success "reuse-$tag_environment-tag" env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" \
    "$fixture/scripts/release.sh" build --environment "$tag_environment" --tag "$created_tag"
  test "$("$REAL_GIT" -C "$fixture" rev-parse "refs/tags/$created_tag")" = "$tag_object_before" || fail "build recreated an existing tag"
done

: >"$docker_log"
expect_failure new-tag-preflight env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" FAKE_WRONG_LABEL=1 \
  "$fixture/scripts/release.sh" build --environment stage --tag v0.2.0-stage.2
if "$REAL_GIT" -C "$fixture" show-ref --verify --quiet refs/tags/v0.2.0-stage.2; then fail "failed preflight created a tag"; fi

expect_failure new-tag-dirty-during-preflight env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" \
  FAKE_DIRTY_SOURCE="$fixture/payload" "$fixture/scripts/release.sh" build --environment stage --tag v0.2.0-stage.2
grep -Fq 'uncommitted changes' "$TMP_ROOT/new-tag-dirty-during-preflight.err" || fail "tag creation ignored changes made during preflight"
if "$REAL_GIT" -C "$fixture" show-ref --verify --quiet refs/tags/v0.2.0-stage.2; then fail "dirty preflight created a tag"; fi
cp "$clean_payload" "$fixture/payload"

expect_failure new-tag-build-failure env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" FAKE_BUILD_FAILURE=1 \
  "$fixture/scripts/release.sh" build --environment stage --tag v0.2.0-stage.2
test "$("$REAL_GIT" -C "$fixture" cat-file -t refs/tags/v0.2.0-stage.2)" = tag || fail "failed build did not preserve its new annotated tag"
test "$("$REAL_GIT" -C "$fixture" rev-parse 'v0.2.0-stage.2^{commit}')" = "$("$REAL_GIT" -C "$fixture" rev-parse HEAD)" || fail "failed build tag lost its commit identity"

"$REAL_GIT" -C "$fixture" tag v0.1.0-stage.1 HEAD~2
"$REAL_GIT" -C "$fixture" tag v0.1.0-stage.2 HEAD
: >"$docker_log"
existing_tag_object=$("$REAL_GIT" -C "$fixture" rev-parse refs/tags/v0.1.0-stage.1)
PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" "$fixture/scripts/release.sh" build --environment stage --tag v0.1.0-stage.1 >"$TMP_ROOT/build.out"
grep -q -- '-e RELEASE_TAG=v0.1.0-stage.1' "$docker_log" || fail "tagged build did not use the explicit tag"
test "$("$REAL_GIT" -C "$fixture" rev-parse refs/tags/v0.1.0-stage.1)" = "$existing_tag_object" || fail "build moved an existing tag"
grep -Fq "v0.1.0-stage.1 ($existing_tag_object)" "$TMP_ROOT/build.out" || fail "build did not select the older tagged commit"
expect_failure wrong-channel env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" "$fixture/scripts/release.sh" build --environment prod --tag v0.1.0-stage.1

printf '%s\n' dirty >>"$fixture/payload"
: >"$docker_log"
expect_failure dirty-snapshot env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" FAKE_GIT_LOG="$git_log" "$fixture/scripts/release.sh" snapshot --environment stage
test ! -s "$docker_log" || fail "dirty snapshot reached Docker"
"$REAL_GIT" -C "$fixture" checkout -q -- payload
expect_failure status-error env PATH="$fixture/fake-bin:$PATH" FAKE_GIT_STATUS_FAILURE=1 FAKE_DOCKER_LOG="$docker_log" "$fixture/scripts/release.sh" snapshot --environment stage

"$REAL_GIT" -C "$fixture" replace HEAD HEAD^
: >"$docker_log"
expect_failure replacement env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" FAKE_GIT_LOG="$git_log" "$fixture/scripts/release.sh" check --environment stage
test ! -s "$docker_log" || fail "replacement source reached Docker"
"$REAL_GIT" -C "$fixture" replace -d HEAD >/dev/null

"$REAL_GIT" -C "$fixture" config extensions.worktreeConfig true
"$REAL_GIT" -C "$fixture" config --worktree remote.fixture.promisor true
expect_failure promisor env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" FAKE_GIT_LOG="$git_log" "$fixture/scripts/release.sh" check --environment stage
for mutation in snapshot build publish resume; do
  set -- "$fixture/scripts/release.sh" "$mutation" --environment stage
  case "$mutation" in
    build) set -- "$@" --tag v0.2.0-stage.1 ;;
    publish) set -- "$@" --tag v0.2.0-stage.1 --notes "$dirty_notes" --dist "$retained" ;;
    resume) set -- "$@" --tag v0.2.0-stage.1 --dist "$retained" ;;
  esac
  : >"$git_log"
  : >"$docker_log"
  expect_failure "promisor-$mutation" env PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" \
    GITHUB_TOKEN=TOKEN_BOUNDARY_SENTINEL "$@"
  grep -Fq 'partial or promisor checkout' "$TMP_ROOT/promisor-$mutation.err" || fail "$mutation did not reject partial source"
  if grep -Fq 'status --porcelain' "$git_log"; then fail "$mutation inspected partial checkout files before rejecting it"; fi
  test ! -s "$docker_log" || fail "$mutation reached Docker in a partial checkout"
done
"$REAL_GIT" -C "$fixture" config --worktree --unset remote.fixture.promisor

linked="$TMP_ROOT/linked"
"$REAL_GIT" -C "$fixture" worktree add -q --detach "$linked" HEAD
: >"$docker_log"
PATH="$fixture/fake-bin:$PATH" FAKE_DOCKER_LOG="$docker_log" FAKE_GIT_LOG="$git_log" "$linked/scripts/release.sh" check --environment stage >"$TMP_ROOT/linked.out"
grep -q 'configuration is valid' "$TMP_ROOT/linked.out" || fail "linked worktree check failed"

smoke_dist="$TMP_ROOT/smoke output"
smoke_source="$TMP_ROOT/smoke-source"
mkdir -p "$smoke_dist/artifacts" "$smoke_source"
case "$(uname -s)/$(uname -m)" in
  Darwin/arm64) smoke_os=darwin; smoke_arch=arm64 ;;
  Darwin/x86_64) smoke_os=darwin; smoke_arch=amd64 ;;
  Linux/aarch64) smoke_os=linux; smoke_arch=arm64 ;;
  Linux/x86_64) smoke_os=linux; smoke_arch=amd64 ;;
  *) fail "unsupported smoke shell test platform" ;;
esac
cat >"$smoke_source/hookspot-stage" <<'EOF'
#!/bin/sh
env | grep '^HOOKSPOT_' >/dev/null && exit 40
[ "$HOME" = "$USERPROFILE" ] && [ -d "$HOME" ] || exit 42
case "$HOME" in /*/extract.*/home) ;; *) exit 43 ;; esac
case "$0" in /*/extract.*/hookspot-stage) ;; *) exit 44 ;; esac
if IFS= read -r unexpected; then exit 45; fi
case "$*" in
  'version --json') printf '%s\n' '{"fixture":true}' ;;
  '--help') printf '%s\n' 'fixture help' ;;
  *) exit 41 ;;
esac
EOF
chmod 755 "$smoke_source/hookspot-stage"
tar -czf "$smoke_dist/artifacts/hookspot_stage_fixture_${smoke_os}_${smoke_arch}.tar.gz" -C "$smoke_source" hookspot-stage
printf '%s\n' '{}' >"$smoke_dist/receipt.json"
HOOKSPOT_DEV_CLI_KEY=TOKEN_BOUNDARY_SENTINEL HOOKSPOT_CONFIG="$TMP_ROOT/missing-config" \
  "$ROOT/scripts/smoke.sh" --dist "$smoke_dist" --environment stage --target "$smoke_os/$smoke_arch" >"$TMP_ROOT/smoke.out"
smoke_report=$(find "$smoke_dist/native/reports/$smoke_os-$smoke_arch" -mindepth 1 -maxdepth 1 -type d | head -1)
test -n "$smoke_report" || fail "smoke collector did not retain a report"
for smoke_file in record.env version.stdout version.stderr help.stdout help.stderr; do
  test -f "$smoke_report/$smoke_file" || fail "smoke collector omitted $smoke_file"
done
test ! -d "$smoke_report"/extract.* || fail "smoke collector retained extracted executable"
grep -q '^EXECUTION=unknown$' "$smoke_report/record.env" || fail "smoke collector invented native status"

cat >"$smoke_source/hookspot-stage" <<'EOF'
#!/bin/sh
case "$*" in
  'version --json') printf 'version-out\r\n'; printf 'version-error\r\n' >&2; exit 7 ;;
  '--help') printf 'help-out\r\n'; printf 'help-error\r\n' >&2; exit 9 ;;
  *) exit 41 ;;
esac
EOF
chmod 755 "$smoke_source/hookspot-stage"
tar -czf "$smoke_dist/artifacts/hookspot_stage_fixture_${smoke_os}_${smoke_arch}.tar.gz" -C "$smoke_source" hookspot-stage
expect_failure smoke-command-failure "$ROOT/scripts/smoke.sh" --dist "$smoke_dist" --environment stage --target "$smoke_os/$smoke_arch"
failed_record=$(grep -l '^VERSION_EXIT=7$' "$smoke_dist/native/reports/$smoke_os-$smoke_arch"/*/record.env)
test -n "$failed_record" || fail "smoke collector did not retain failed status"
failed_report=${failed_record%/record.env}
grep -q '^HELP_EXIT=9$' "$failed_record" || fail "smoke collector lost help failure status"
grep -q '^VERSION_TIMEOUT=false$' "$failed_record" || fail "smoke collector invented version timeout"
grep -q '^HELP_TIMEOUT=false$' "$failed_record" || fail "smoke collector invented help timeout"
printf 'version-out\r\n' >"$TMP_ROOT/want-version.out"
printf 'version-error\r\n' >"$TMP_ROOT/want-version.err"
printf 'help-out\r\n' >"$TMP_ROOT/want-help.out"
printf 'help-error\r\n' >"$TMP_ROOT/want-help.err"
cmp "$TMP_ROOT/want-version.out" "$failed_report/version.stdout" >/dev/null || fail "smoke collector changed version stdout"
cmp "$TMP_ROOT/want-version.err" "$failed_report/version.stderr" >/dev/null || fail "smoke collector changed version stderr"
cmp "$TMP_ROOT/want-help.out" "$failed_report/help.stdout" >/dev/null || fail "smoke collector changed help stdout"
cmp "$TMP_ROOT/want-help.err" "$failed_report/help.stderr" >/dev/null || fail "smoke collector changed help stderr"

cat >"$smoke_source/hookspot-stage" <<'EOF'
#!/bin/sh
case "$*" in
  'version --json') trap '' TERM; exec /bin/sleep 30 ;;
  '--help') printf '%s\n' 'fixture help' ;;
  *) exit 41 ;;
esac
EOF
chmod 755 "$smoke_source/hookspot-stage"
tar -czf "$smoke_dist/artifacts/hookspot_stage_fixture_${smoke_os}_${smoke_arch}.tar.gz" -C "$smoke_source" hookspot-stage
expect_failure smoke-timeout "$ROOT/scripts/smoke.sh" --dist "$smoke_dist" --environment stage --target "$smoke_os/$smoke_arch"
timeout_record=$(grep -l '^VERSION_TIMEOUT=true$' "$smoke_dist/native/reports/$smoke_os-$smoke_arch"/*/record.env)
test -n "$timeout_record" || fail "smoke collector did not retain timeout status"
grep -q '^VERSION_EXIT=124$' "$timeout_record" || fail "smoke collector lost timeout exit"
grep -q '^HELP_EXIT=0$' "$timeout_record" || fail "smoke collector skipped help after timeout"
if grep -q 'Killed: 9' "$TMP_ROOT/smoke-timeout.err"; then
  fail "smoke collector printed shell kill diagnostics"
fi

publication_fixture="$TMP_ROOT/publication-fixture"
publication_dist="$publication_fixture/stage"
publication_published_dist="$publication_fixture/published-stage"
publication_prod_dist="$publication_fixture/prod"
publication_bin="$TMP_ROOT/publication-bin"
publication_state="$TMP_ROOT/publication-state"
publication_docker_log="$TMP_ROOT/publication-docker.log"
publication_git_log="$TMP_ROOT/publication-git.log"
publication_write_log="$TMP_ROOT/publication-write.log"
publication_upload_attempts="$TMP_ROOT/publication-upload-attempts.log"
publication_hook_marker="$TMP_ROOT/publication-hook-token"
publication_baseline_dist="$publication_fixture/baseline"
publication_review="$publication_fixture/routine-review.json"
mkdir -p "$publication_fixture/tools" "$publication_dist" "$publication_published_dist" "$publication_prod_dist" "$publication_bin" "$publication_state"
if ! publication_tools_identity=$(env -u GITHUB_TOKEN -u GH_TOKEN "$SCRIPT" _tools-identity 2>"$TMP_ROOT/publication-tools-identity-initial.err"); then
  if ! env -u GITHUB_TOKEN -u GH_TOKEN "$SCRIPT" tools >"$TMP_ROOT/publication-tools.out" 2>"$TMP_ROOT/publication-tools.err"; then
    sed -n '1,20p' "$TMP_ROOT/publication-tools.err" >&2
    fail "release tools bootstrap failed"
  fi
  if ! publication_tools_identity=$(env -u GITHUB_TOKEN -u GH_TOKEN "$SCRIPT" _tools-identity 2>"$TMP_ROOT/publication-tools-identity.err"); then
    sed -n '1,20p' "$TMP_ROOT/publication-tools-identity.err" >&2
    fail "release tools identity remained unavailable after bootstrap"
  fi
fi
set -- $publication_tools_identity
[ "$#" -eq 2 ] || fail "release tools identity was not a two-field record"
release_test_image_id=$1
release_test_image_platform=$2
printf '%s\n' "$release_test_image_id" | grep -Eq '^sha256:[0-9a-f]{64}$' || fail "release tools image ID was invalid"
case "$release_test_image_platform" in linux/amd64|linux/arm64) ;; *) fail "release tools platform was unsupported" ;; esac
release_test_helper_arch=${release_test_image_platform#linux/}

cleanup_fake_docker="$TMP_ROOT/cleanup-fake-docker"
cleanup_fake_log="$TMP_ROOT/cleanup-fake-docker.log"
cat >"$cleanup_fake_docker" <<'EOF'
#!/bin/sh
: "${CLEANUP_FAKE_LOG:?}"
printf '%s\n' "$*" >>"$CLEANUP_FAKE_LOG"
exit 1
EOF
chmod +x "$cleanup_fake_docker"
export CLEANUP_FAKE_LOG="$cleanup_fake_log"
cleanup_real_docker=$REAL_DOCKER
integration_cidfile="$TMP_ROOT/cleanup-unconfirmed.cid"
printf '%064d' 0 >"$integration_cidfile"
REAL_DOCKER=$cleanup_fake_docker
if cleanup_integration_container; then
  fail "failed Docker removal and query were treated as confirmed absence"
fi
[ "$integration_cidfile" = "$TMP_ROOT/cleanup-unconfirmed.cid" ] || \
  fail "unconfirmed container cleanup discarded its CID file"
cleanup_fake_calls=$(wc -l <"$cleanup_fake_log" | tr -d ' ')
integration_cidfile="$TMP_ROOT/cleanup-invalid.cid"
printf 'not-a-container-id' >"$integration_cidfile"
if cleanup_integration_container; then
  fail "invalid container CID was treated as cleaned up"
fi
[ "$integration_cidfile" = "$TMP_ROOT/cleanup-invalid.cid" ] || \
  fail "invalid container cleanup discarded its CID file"
[ "$(wc -l <"$cleanup_fake_log" | tr -d ' ')" -eq "$cleanup_fake_calls" ] || \
  fail "invalid container CID reached Docker"
REAL_DOCKER=$cleanup_real_docker
unset CLEANUP_FAKE_LOG
integration_cidfile=

integration_cidfile="$TMP_ROOT/cleanup-probe.cid"
"$REAL_DOCKER" run --detach --rm --cidfile "$integration_cidfile" --network none \
  -e HTTP_PROXY= -e http_proxy= -e HTTPS_PROXY= -e https_proxy= -e NO_PROXY= -e no_proxy= \
  -e ALL_PROXY= -e all_proxy= -e FTP_PROXY= -e ftp_proxy= \
  --entrypoint /bin/sh "$release_test_image_id" -c 'while :; do sleep 60; done' >/dev/null
cleanup_probe_id=$(cat "$integration_cidfile")
cleanup_integration_container
if ! cleanup_probe_remaining=$("$REAL_DOCKER" ps -aq --no-trunc --filter "id=$cleanup_probe_id" 2>/dev/null); then
  fail "cidfile-owned integration container absence could not be checked"
fi
if [ -n "$cleanup_probe_remaining" ]; then
  fail "cidfile-owned integration container was not removed"
fi

integration_home="$TMP_ROOT/integration-home"
integration_repo="$TMP_ROOT/binary-integration-repository"
mkdir -p "$integration_home"
integration_git() {
  env -i PATH="$PATH" HOME="$integration_home" GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null \
    GIT_CONFIG_COUNT=0 GIT_NO_REPLACE_OBJECTS=1 "$REAL_GIT" "$@"
}
integration_git clone --quiet --no-local "$ROOT" "$integration_repo"
"$REAL_GIT" -C "$ROOT" -c core.fsmonitor=false diff --binary --no-ext-diff HEAD -- . ':!.superpowers' >"$TMP_ROOT/binary-integration.patch"
if [ -s "$TMP_ROOT/binary-integration.patch" ]; then
  integration_git -C "$integration_repo" apply "$TMP_ROOT/binary-integration.patch"
fi
cat >"$integration_repo/release/environments.json" <<'EOF'
{
  "schema_version": 1,
  "repository": "example/hookspot-cli-fixture",
  "stage": { "server_url": "https://stage.release.invalid:18443/gateway/hookspot", "branch": "stage" },
  "prod": { "server_url": "https://prod.release.invalid", "branch": "main" }
}
EOF
integration_git -C "$integration_repo" config user.email fixture@example.invalid
integration_git -C "$integration_repo" config user.name Fixture
integration_git -C "$integration_repo" add .
integration_git -C "$integration_repo" commit --quiet -m 'retained binary integration endpoints'
integration_git -C "$integration_repo" show HEAD:release/environments.json | grep -q 'stage.release.invalid:18443/gateway/hookspot' || \
  fail "stage integration endpoint was not committed"
integration_git -C "$integration_repo" show HEAD:release/environments.json | grep -q '"https://prod.release.invalid"' || \
  fail "production integration endpoint was not committed"

expect_success integration-stage-build "$integration_repo/scripts/release.sh" build --environment stage --tag v0.3.0-stage.1
test "$(integration_git -C "$integration_repo" cat-file -t refs/tags/v0.3.0-stage.1)" = tag || fail "integration build did not create an annotated tag"
test "$(integration_git -C "$integration_repo" rev-parse 'v0.3.0-stage.1^{commit}')" = "$(integration_git -C "$integration_repo" rev-parse HEAD)" || \
  fail "integration tag did not select committed HEAD"
expect_success integration-prod-snapshot "$integration_repo/scripts/release.sh" snapshot --environment prod
integration_stage_dist=$(sed -n 's/^release output: //p' "$TMP_ROOT/integration-stage-build.out")
integration_prod_dist=$(sed -n 's/^release output: //p' "$TMP_ROOT/integration-prod-snapshot.out")
[ -d "$integration_stage_dist/artifacts" ] && [ -d "$integration_prod_dist/artifacts" ] || \
  fail "retained binary integration builds were not created"
integration_evidence="$TMP_ROOT/binary-integration-evidence"
mkdir -p "$integration_evidence"
integration_cidfile="$TMP_ROOT/binary-integration.cid"

if ! "$REAL_DOCKER" run --rm --cidfile "$integration_cidfile" --network none \
  --add-host stage.release.invalid:127.0.0.1 --add-host prod.release.invalid:127.0.0.1 \
  -e GOTOOLCHAIN=local -e GOFLAGS=-mod=readonly -e CGO_ENABLED=0 \
  -e HOOKSPOT_INTEGRATION_STAGE_DIST=/fixtures/stage \
  -e HOOKSPOT_INTEGRATION_PROD_DIST=/fixtures/prod \
  -e HOOKSPOT_INTEGRATION_RUNNER_PLATFORM="$release_test_image_platform" \
  -e HOOKSPOT_INTEGRATION_EVIDENCE_DIR=/evidence \
  -e HTTP_PROXY= -e http_proxy= -e HTTPS_PROXY= -e https_proxy= -e NO_PROXY= -e no_proxy= \
  -e ALL_PROXY= -e all_proxy= -e FTP_PROXY= -e ftp_proxy= \
  -v "$ROOT:/src:ro" -v "$integration_stage_dist:/fixtures/stage:ro" -v "$integration_prod_dist:/fixtures/prod:ro" \
  -v "$integration_evidence:/evidence" \
  -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build -w /src \
  --entrypoint /bin/sh "$release_test_image_id" -c \
  'go test -mod=readonly -count=2 -timeout=2m -v ./tools/releasecheck -run ^TestRetainedStageAndProdBinariesUseTLSAPIAndPhoenix$' \
  >"$TMP_ROOT/binary-integration.out" 2>"$TMP_ROOT/binary-integration.err"; then
  sed -n '1,40p' "$TMP_ROOT/binary-integration.out" >&2
  sed -n '1,40p' "$TMP_ROOT/binary-integration.err" >&2
  fail "retained binary TLS/API/Phoenix integration failed"
fi
cleanup_integration_container
repair_integration_ownership || fail "retained binary integration outputs could not be returned to the caller"
for integration_environment in stage prod; do
  [ "$(find "$integration_evidence" -type f -path "*/$integration_environment-*/retained/native/manual/*/*.json" | wc -l | tr -d ' ')" -eq 2 ] || \
    fail "$integration_environment validated network observations were not preserved"
done

"$REAL_DOCKER" run --rm --network none \
  -e GOTOOLCHAIN=local -e GOFLAGS=-mod=readonly -e CGO_ENABLED=0 \
  -e HOOKSPOT_SHELL_FIXTURE_TOOLS=/fixture/tools \
  -e HOOKSPOT_SHELL_FIXTURE_OUTPUT=/fixture/stage \
  -e HOOKSPOT_SHELL_PUBLISHED_FIXTURE_OUTPUT=/fixture/published-stage \
  -e HOOKSPOT_SHELL_PROD_FIXTURE_OUTPUT=/fixture/prod \
  -e HOOKSPOT_SHELL_BASELINE_FIXTURE_OUTPUT=/fixture/baseline \
  -e HOOKSPOT_SHELL_REVIEW_FIXTURE_OUTPUT=/fixture/routine-review.json \
  -e HOOKSPOT_SHELL_PUBLISHER_PLATFORM="$release_test_image_platform" \
  -e FIXTURE_HOST_OS="$smoke_os" -e FIXTURE_HOST_ARCH="$smoke_arch" \
  -e HTTP_PROXY= -e http_proxy= -e HTTPS_PROXY= -e https_proxy= -e NO_PROXY= -e no_proxy= \
  -e ALL_PROXY= -e all_proxy= -e FTP_PROXY= -e ftp_proxy= \
  -v "$ROOT:/src:ro" -v "$publication_fixture:/fixture" \
  -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build -w /src \
  --entrypoint /bin/sh "$release_test_image_id" -c \
  'GOOS=linux GOARCH=amd64 go build -o /fixture/tools/releasecheck-linux-amd64 ./tools/releasecheck &&
   GOOS=linux GOARCH=arm64 go build -o /fixture/tools/releasecheck-linux-arm64 ./tools/releasecheck &&
   GOOS="$FIXTURE_HOST_OS" GOARCH="$FIXTURE_HOST_ARCH" go build -o /fixture/tools/releasecheck-host ./tools/releasecheck &&
   go test -mod=readonly -count=1 ./tools/releasecheck -run ^TestWriteShellPublicationFixture$' >/dev/null
if [ ! -O "$publication_dist/publication.json" ] || [ ! -O "$publication_fixture/tools/releasecheck-host" ] || \
   [ ! -r "$publication_dist/publication.json" ] || [ ! -x "$publication_fixture/tools/releasecheck-host" ]; then
  "$REAL_DOCKER" run --rm --network none -v "$publication_fixture:/fixture" \
    -e HTTP_PROXY= -e http_proxy= -e HTTPS_PROXY= -e https_proxy= -e NO_PROXY= -e no_proxy= \
    -e ALL_PROXY= -e all_proxy= -e FTP_PROXY= -e ftp_proxy= \
    --entrypoint chown "$release_test_image_id" -R "$(id -u):$(id -g)" /fixture
fi
[ -O "$publication_dist/publication.json" ] && [ -r "$publication_dist/publication.json" ] || \
  fail "generated publication fixture is not owned and readable by the caller"
[ -O "$publication_fixture/tools/releasecheck-host" ] && [ -x "$publication_fixture/tools/releasecheck-host" ] || \
  fail "generated typed helper is not owned and executable by the caller"
[ -O "$publication_review" ] && [ -r "$publication_review" ] && \
  [ -O "$publication_baseline_dist/receipt.json" ] && [ -r "$publication_baseline_dist/receipt.json" ] || \
  fail "generated routine inputs are not owned and readable by the caller"
publication_dist=$(cd "$publication_dist" && pwd -P)
publication_published_dist=$(cd "$publication_published_dist" && pwd -P)
publication_prod_dist=$(cd "$publication_prod_dist" && pwd -P)
publication_baseline_dist=$(cd "$publication_baseline_dist" && pwd -P)
publication_review="$(cd "$(dirname "$publication_review")" && pwd -P)/$(basename "$publication_review")"
publication_initial="$TMP_ROOT/publication-initial.json"
cp "$publication_dist/publication.json" "$publication_initial"
publication_identity="$TMP_ROOT/publication-identity"
"$REAL_DOCKER" run --rm --network none -v "$publication_dist:/out:ro" \
  --entrypoint "/out/tools/releasecheck-linux-$release_test_helper_arch" \
  "$release_test_image_id" \
  publication describe --environment stage --dist /out >"$publication_identity"
publication_tag=$(awk -F '\t' '$1=="tag" {print $2}' "$publication_identity")
publication_tag_object=$(awk -F '\t' '$1=="tag_object" {print $2}' "$publication_identity")
publication_tag_commit=$(awk -F '\t' '$1=="tag_commit" {print $2}' "$publication_identity")
publication_marker=$(awk -F '\t' '$1=="marker" {print $2}' "$publication_identity")
awk -F '\t' '$1=="asset" {print $2}' "$publication_identity" >"$publication_state/asset-names"
publication_first_asset=$(sed -n '1p' "$publication_state/asset-names")
publication_second_asset=$(sed -n '2p' "$publication_state/asset-names")
cp "$publication_dist/release-notes.md" "$TMP_ROOT/release-notes.initial"
cp "$publication_dist/native/evidence.json" "$TMP_ROOT/native-evidence.initial"
cp "$publication_dist/controls/release/environments.json" "$TMP_ROOT/environment-manifest.initial"
printf '%s\n' draft >"$publication_state/release"
printf '%s\n' owned >"$publication_state/body"
: >"$publication_state/assets"
touch "$publication_state/tag"
"$REAL_GIT" -C "$fixture" remote add origin chain:hookspot-cli.git
"$REAL_GIT" -C "$fixture" config url.https://github.com/example/.insteadOf chain:
"$REAL_GIT" -C "$fixture" config url.file://"$TMP_ROOT"/unexpected/.insteadOf https://github.com/example/

cat >"$publication_bin/docker" <<EOF
#!/bin/bash
set -eu
printf '%s\n' "\$*" >>"\$PUBLICATION_DOCKER_LOG"
if [ "\$1 \$2" = "image inspect" ]; then
  case "\$*" in
    *lock-sha256*) printf '%064d\n' 0 | tr 0 a ;;
    *recipe-sha256*) printf '%064d\n' 0 | tr 0 b ;;
    *bootstrap-sha256*) printf '%064d\n' 0 | tr 0 c ;;
    *) printf '%s\n' 'sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56 $release_test_image_platform' ;;
  esac
  exit 0
fi
if [ "\$1" = version ]; then printf '%s\n' '$release_test_image_platform'; exit 0; fi
case "\$*" in
  *'toolchain.env'*'sha256sum'*) printf '%064d  /input\n' 0 | tr 0 a; exit 0 ;;
  *'release.Dockerfile'*'sha256sum'*) printf '%064d  /input\n' 0 | tr 0 b; exit 0 ;;
  *'releasebootstrap/main.go'*'sha256sum'*) printf '%064d  /input\n' 0 | tr 0 c; exit 0 ;;
  *'go version; goreleaser --version; gh --version'*) printf '%s\n' 'go version go1.26.8 $release_test_image_platform' 'goreleaser version 2.17.1' 'gh version 2.100.0 (fixture)'; exit 0 ;;
  *' env-url '*|*' env-url') printf '%s\n' 'https://stage.example.invalid'; exit 0 ;;
  *' repository '*|*' repository') printf '%s\n' "\${PUBLICATION_SOURCE_REPOSITORY:-example/hookspot-cli}"; exit 0 ;;
  *'--entrypoint release-helper-check'*)
    args=()
    for arg in "\$@"; do
      if [ "\$arg" = sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56 ]; then
        args+=($release_test_image_id)
      else
        args+=("\$arg")
      fi
    done
    exec "$REAL_DOCKER" "\${args[@]}"
    ;;
  *'--entrypoint /out/tools/releasecheck-linux-'*)
    out_mount=
    state_mount=
    review_mount=
    baseline_mount=
    destination_mount=
    after_image=false
    helper_args=()
    for arg in "\$@"; do
      case "\$arg" in
        *:/out|*:/out:ro) out_mount=\${arg%%:/out*} ;;
        *:/state|*:/state:ro) state_mount=\${arg%%:/state*} ;;
        *:/review:ro) review_mount=\${arg%%:/review:ro} ;;
        *:/baseline:ro) baseline_mount=\${arg%%:/baseline:ro} ;;
        *:/destination) destination_mount=\${arg%%:/destination} ;;
      esac
      if [ "\$after_image" = true ]; then
        case "\$arg" in
          /out) arg=\$out_mount ;; /out/*) arg=\$out_mount/\${arg#/out/} ;;
          /state) arg=\$state_mount ;; /state/*) arg=\$state_mount/\${arg#/state/} ;;
          /review) arg=\$review_mount ;;
          /baseline) arg=\$baseline_mount ;; /baseline/*) arg=\$baseline_mount/\${arg#/baseline/} ;;
          /destination) arg=\$destination_mount ;; /destination/*) arg=\$destination_mount/\${arg#/destination/} ;;
        esac
        helper_args+=("\$arg")
      elif [ "\$arg" = sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56 ]; then
        after_image=true
      fi
    done
    [ -n "\$out_mount" ] && [ "\$after_image" = true ] || exit 92
    exec env -u GITHUB_TOKEN -u GH_TOKEN "$publication_fixture/tools/releasecheck-host" "\${helper_args[@]}"
    ;;
esac
if [[ "\$*" == *'--entrypoint gh'* ]]; then
  if [ "\${PUBLICATION_DENY_ACCESS-}" = 1 ] && [[ "\$*" == *' api --method GET repos/example/hookspot-cli'* ]]; then exit 1; fi
  if [[ "\$*" == *' release view '* ]]; then
    [ "\${PUBLICATION_STATUS_VIEW_FAIL-}" != 1 ] || exit 1
    printf '%s\n' '{"databaseId":42,"tagName":"$publication_tag","isDraft":true,"isPrerelease":true}'
    exit 0
  fi
  if [[ "\$*" == *'releases?per_page=100'* ]]; then
    state=\$(cat "$publication_state/release")
    if [ "\$state" = absent ]; then printf '%s\n' '[[]]'; exit 0; fi
    draft=true
    [ "\$state" = published ] && draft=false
    body='Fixture notes.\\n\\n<!-- hookspot-publication:$publication_marker -->\\n'
    [ "\$(cat "$publication_state/body")" = owned ] || body='foreign body'
    response="$publication_state/releases-response.tmp"
    printf '[[{"id":42,"tag_name":"$publication_tag","draft":%s,"prerelease":true,"body":"%s","assets":[' "\$draft" "\$body" >"\$response"
    separator=
    while IFS=$'\t' read -r id name; do
      [ -n "\$id" ] || continue
      size=\$(wc -c <"$publication_dist/artifacts/\$name" | tr -d ' ')
      printf '%s{"id":%s,"name":"%s","size":%s,"state":"uploaded"}' "\$separator" "\$id" "\$name" "\$size" >>"\$response"
      separator=,
    done <"$publication_state/assets"
    printf ']}]]\n' >>"\$response"
    mv "\$response" "$publication_state/releases-response.json"
    cat "$publication_state/releases-response.json"
    exit 0
  fi
  if [[ "\$*" == *'releases/assets/'* ]]; then
    id=\${*: -1}; id=\${id##*/}
    name=\$(awk -F '\t' -v id="\$id" '\$1==id {print \$2}' "$publication_state/assets")
    [ -n "\$name" ] || exit 1
    if [ "\${PUBLICATION_CORRUPT_ASSET-}" = "\$name" ]; then printf '%s\n' corrupt; else cat "$publication_dist/artifacts/\$name"; fi
    exit 0
  fi
  if [[ "\$*" == *' release create '* ]]; then
    printf '%s\n' 'WRITE create' >>"$publication_write_log"
    printf '%s\n' draft >"$publication_state/release"
    printf '%s\n' owned >"$publication_state/body"
    [ "\${PUBLICATION_FAIL_AFTER-}" != create ] || exit 17
    exit 0
  fi
  if [[ "\$*" == *' release upload '* ]]; then
    requested=
    for arg in "\$@"; do
      case "\$arg" in /assets/*)
        name=\${arg##*/}
        if awk -F '\t' -v name="\$name" '\$2==name {found=1} END {exit !found}' "$publication_state/assets"; then
          exit 1
        fi
        while IFS= read -r previous; do
          [ "\$previous" != "\$name" ] || exit 1
        done <<<"\$requested"
        requested="\${requested}\${requested:+
}\$name"
        ;;
      esac
    done
    [ -n "\$requested" ] || exit 1
    count=0
    while IFS= read -r name; do
      [ -n "\$name" ] || continue
      id=100
      while IFS= read -r expected; do
        [ "\$expected" = "\$name" ] && break
        id=\$((id + 1))
      done <"$publication_state/asset-names"
      printf '%s\n' "\$name" >>"$publication_upload_attempts"
      printf '%s\t%s\n' "\$id" "\$name" >>"$publication_state/assets"
      printf 'WRITE upload %s\n' "\$name" >>"$publication_write_log"
      count=\$((count + 1))
      [ "\${PUBLICATION_FAIL_AFTER-}" != "upload:\$count" ] || exit 18
    done <<<"\$requested"
    exit 0
  fi
  if [[ "\$*" == *' release edit '* ]]; then
    printf '%s\n' 'WRITE edit' >>"$publication_write_log"
    printf '%s\n' published >"$publication_state/release"
    [ "\${PUBLICATION_FAIL_AFTER-}" != edit ] || exit 19
    exit 0
  fi
  if [[ "\$*" == *' api --method GET repos/example/hookspot-cli'* ]]; then printf '%s\n' '{}'; exit 0; fi
fi
exit 1
EOF
chmod 755 "$publication_bin/docker"

cat >"$publication_bin/git" <<EOF
#!/bin/bash
set -eu
printf '%s\n' "\$*" >>"$publication_git_log"
case "\$*" in
  *'ls-remote origin'*)
    if [ -f "$publication_state/tag" ]; then
      tag_object="$publication_tag_object"; tag_commit="$publication_tag_commit"
      [ "\${PUBLICATION_TAG_MISMATCH-}" = 1 ] && tag_object=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
      printf '%s\t%s\n%s\t%s\n' "\$tag_object" "refs/tags/$publication_tag" "\$tag_commit" "refs/tags/$publication_tag^{}"
    fi
    exit 0
    ;;
esac
case "\$*" in
  *'core.hooksPath=/dev/null'*) if [ -n "\${GITHUB_TOKEN-}\${GH_TOKEN-}" ]; then touch "$publication_hook_marker"; fi ;;
esac
case "\$*" in
  *' push --porcelain origin '*)
    touch "$publication_state/tag"
    printf '%s\n' 'WRITE push' >>"$publication_write_log"
    [ "\${PUBLICATION_FAIL_AFTER-}" != push ] || exit 16
    exit 0
    ;;
esac
exec "$REAL_GIT" "\$@"
EOF
chmod 755 "$publication_bin/git"

: >"$publication_docker_log"
expect_failure publication-token env -u GITHUB_TOKEN -u GH_TOKEN PATH="$publication_bin:$PATH" \
  PUBLICATION_DOCKER_LOG="$publication_docker_log" PUBLICATION_STATE="$publication_state" \
  "$fixture/scripts/release.sh" resume --environment stage --tag v1.2.3-stage.1 --dist "$publication_dist"
test ! -s "$publication_docker_log" || fail "missing publication token reached Docker"

publication_lock="$fixture/.git/hookspot-publication.lock"
mkdir "$publication_lock"
printf '%s\n' 'pid=another started=2026-09-06T00:00:00Z' >"$publication_lock/owner"
expect_failure publication-lock env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" resume --environment stage --tag v1.2.3-stage.1 --dist "$publication_dist"
grep -q '^pid=another ' "$publication_lock/owner" || fail "publisher stole an existing local lock"
rm -rf "$publication_lock"

signal_marker="$TMP_ROOT/publication-after-signal"
signal_probe="$TMP_ROOT/publication-signal-probe.sh"
cat >"$signal_probe" <<'EOF'
#!/bin/bash
set -eu
production=$1
repository=$2
marker=$3
signal_name=$4
eval "$(sed '/^command=${1:-}/,$d' "$production")"
cd "$repository"
acquire_publication_lock
kill -"$signal_name" "$$"
touch "$marker"
EOF
chmod 755 "$signal_probe"
for signal_case in HUP:129 INT:130 TERM:143; do
  signal_name=${signal_case%%:*}
  expected_status=${signal_case#*:}
  case_marker="$signal_marker-$signal_name"
  set +e
  {
    "$signal_probe" "$SCRIPT" "$fixture" "$case_marker" "$signal_name" >"$TMP_ROOT/publication-signal-$signal_name.out"
    signal_status=$?
  } 2>"$TMP_ROOT/publication-signal-$signal_name.err"
  set -e
  [ "$signal_status" -eq "$expected_status" ] || fail "publication SIG$signal_name did not preserve signal exit status"
  test ! -e "$case_marker" || fail "publication continued after SIG$signal_name"
  test ! -d "$publication_lock" || fail "publication SIG$signal_name did not release its owned lock"
done

latest_probe="$TMP_ROOT/publication-latest-probe.sh"
cat >"$latest_probe" <<'EOF'
#!/bin/bash
set -eu
production=$1
scratch=$2
mode=$3
dist=$4
fake_bin=$5
image_platform=$6
eval "$(sed '/^command=${1:-}/,$d' "$production")"
trap - EXIT HUP INT TERM
PUBLICATION_TEMP=$scratch
environment=prod
tag=v1.2.3
repository=example/hookspot-cli
resume_mode=true
IMAGE_ID=sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56
IMAGE_PLATFORM=$image_platform
PATH="$fake_bin:$PATH"
: >"$scratch/refresh-calls"
: >"$scratch/mutations"
verify_output() { :; }
allocate_publication_temp() { :; }
validate_remote_identity() { :; }
check_publisher_access() { :; }
verify_remote_stage_acceptance() { :; }
require_exact_remote_tag() { :; }
push_exact_tag() { printf '%s\n' push >>"$scratch/mutations"; }
create_draft() { printf '%s\n' create >>"$scratch/mutations"; printf 'status\tdraft\n' >"$PUBLICATION_TEMP/plan"; }
refresh_remote_state() {
  printf 'call\n' >>"$scratch/refresh-calls"
  if [ "$mode" = known-newer ]; then
    printf 'status\tabsent\n' >"$PUBLICATION_TEMP/plan"
    printf '[[{"id":90,"tag_name":"v1.2.4","draft":false,"prerelease":false,"body":"","assets":[]}]]\n' >"$PUBLICATION_TEMP/releases.json"
  else
    printf 'status\tdraft\n' >"$PUBLICATION_TEMP/plan"
    calls=$(wc -l <"$scratch/refresh-calls" | tr -d ' ')
    if [ "$calls" -eq 1 ]; then
      printf '[[]]\n' >"$PUBLICATION_TEMP/releases.json"
    else
      printf '[[{"id":91,"tag_name":"v1.2.2","draft":false,"prerelease":false,"body":"","assets":[]}]]\n' >"$PUBLICATION_TEMP/releases.json"
    fi
  fi
}
reconcile_remote() { printf 'status\tdraft\n' >"$PUBLICATION_TEMP/reconciled"; }
run_gh() {
  case "$1 $2" in
    'release upload') printf '%s\n' upload >>"$scratch/mutations"; return 0 ;;
    'release edit') printf '%s\n' edit >>"$scratch/mutations"; return 0 ;;
  esac
  if [ "$1 $2" = 'api --method' ]; then return 1; fi
}
if [ "$mode" = positive-control ]; then
  push_exact_tag
  create_draft
  printf 'missing\tfixture.asset\n' >"$PUBLICATION_TEMP/plan"
  upload_missing_assets
  run_gh release edit "$tag"
  exit 0
fi
publish_retained true
EOF
chmod 755 "$latest_probe"
latest_control="$TMP_ROOT/latest-positive-control"
mkdir "$latest_control"
expect_success latest-positive-control env PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$latest_probe" "$SCRIPT" "$latest_control" positive-control "$publication_prod_dist" "$publication_bin" "$release_test_image_platform"
printf '%s\n' push create upload edit >"$latest_control/expected-mutations"
cmp -s "$latest_control/expected-mutations" "$latest_control/mutations" || \
  fail "production policy observer did not record every mutation kind"
for latest_case in known-newer first-stable-changed; do
  latest_dir="$TMP_ROOT/latest-$latest_case"
  mkdir "$latest_dir"
  expect_failure "latest-$latest_case" env PUBLICATION_DOCKER_LOG="$publication_docker_log" \
    "$latest_probe" "$SCRIPT" "$latest_dir" "$latest_case" "$publication_prod_dist" "$publication_bin" "$release_test_image_platform"
  test ! -s "$latest_dir/mutations" || fail "$latest_case production policy ran after remote mutation"
done
grep -q 'production stable release history rejected this version' "$TMP_ROOT/latest-known-newer.err" || \
  fail "known-newer probe did not reach the production history policy"
grep -q 'cannot establish the current stable latest release' "$TMP_ROOT/latest-first-stable-changed.err" || \
  fail "first-stable-change probe did not reach the final latest policy"
[ "$(wc -l <"$TMP_ROOT/latest-first-stable-changed/refresh-calls" | tr -d ' ')" -eq 3 ] || \
  fail "first-stable production policy was not refreshed before finalization"

native_workflow_dist="$TMP_ROOT/native-workflow"
cp -R "$publication_dist" "$native_workflow_dist"
native_workflow_dist=$(cd "$native_workflow_dist" && pwd -P)
rm -f "$native_workflow_dist/native/requirements.json" "$native_workflow_dist/native/evidence.json"
rm -rf "$native_workflow_dist/native/manual"
: >"$publication_docker_log"
expect_success native-requirements-wrapper env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  GITHUB_TOKEN=TOKEN_BOUNDARY_SENTINEL GH_TOKEN=TOKEN_BOUNDARY_SENTINEL \
  "$fixture/scripts/release.sh" native-requirements --environment stage --dist "$native_workflow_dist"
test -s "$native_workflow_dist/native/requirements.json" || fail "native requirements wrapper did not retain requirements"
expect_failure native-requirements-exclusive env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" native-requirements --environment stage --dist "$native_workflow_dist"

routine_workflow_dist="$TMP_ROOT/native-routine-workflow"
cp -R "$publication_dist" "$routine_workflow_dist"
routine_workflow_dist=$(cd "$routine_workflow_dist" && pwd -P)
rm -f "$routine_workflow_dist/native/requirements.json" "$routine_workflow_dist/native/evidence.json"
rm -rf "$routine_workflow_dist/native/manual"
: >"$publication_docker_log"
expect_success native-requirements-routine env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" native-requirements --environment stage --dist "$routine_workflow_dist" \
  --review "$publication_review" --baseline-dist "$publication_baseline_dist"
grep -Fq -- "-v $publication_review:/review:ro" "$publication_docker_log" || \
  fail "routine review was not mounted read-only"
grep -Fq -- "-v $publication_baseline_dist:/baseline:ro" "$publication_docker_log" || \
  fail "routine baseline was not mounted read-only"
grep -Fq -- 'native-requirements --dist /out --review /review --baseline-dist /baseline' "$publication_docker_log" || \
  fail "routine review and baseline options did not reach the typed helper"
test -s "$routine_workflow_dist/native/requirements.json" || fail "routine requirements were not retained"

for root_case in current baseline; do
  root_current=$publication_dist
  root_baseline=$publication_baseline_dist
  [ "$root_case" = current ] && root_current=/
  [ "$root_case" = baseline ] && root_baseline=/
  : >"$publication_docker_log"
  expect_failure "native-requirements-root-$root_case" env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
    "$fixture/scripts/release.sh" native-requirements --environment stage --dist "$root_current" \
    --review "$publication_review" --baseline-dist "$root_baseline"
  grep -Fq 'routine retained parents cannot be filesystem root' "$TMP_ROOT/native-requirements-root-$root_case.err" || \
    fail "routine root rejection missed its policy gate"
  test ! -s "$publication_docker_log" || fail "routine root input reached Docker"
done
: >"$publication_docker_log"
expect_failure native-requirements-equal-parents env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" native-requirements --environment stage --dist "$publication_baseline_dist" \
  --review "$publication_review" --baseline-dist "$publication_baseline_dist"
grep -Fq 'routine baseline and current retained tree must be disjoint' "$TMP_ROOT/native-requirements-equal-parents.err" || \
  fail "equal routine parents missed their disjointness gate"
test ! -s "$publication_docker_log" || fail "equal routine parents reached Docker"

routine_overlap_current="$TMP_ROOT/routine-overlap-current"
cp -R "$publication_dist" "$routine_overlap_current"
rm -f "$routine_overlap_current/native/requirements.json" "$routine_overlap_current/native/evidence.json"
rm -rf "$routine_overlap_current/native/manual"
mkdir "$routine_overlap_current/nested-baseline"
cp -R "$routine_overlap_current" "$TMP_ROOT/routine-overlap-current-before"
: >"$publication_docker_log"
expect_failure native-requirements-baseline-inside-current env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" native-requirements --environment stage --dist "$routine_overlap_current" \
  --review "$publication_review" --baseline-dist "$routine_overlap_current/nested-baseline"
grep -Fq 'routine baseline and current retained tree must be disjoint' "$TMP_ROOT/native-requirements-baseline-inside-current.err" || \
  fail "nested routine baseline missed its disjointness gate"
test ! -s "$publication_docker_log" || fail "nested routine baseline reached Docker"
diff -r "$TMP_ROOT/routine-overlap-current-before" "$routine_overlap_current" >/dev/null || \
  fail "nested routine baseline rejection changed current retained tree"

routine_overlap_baseline="$TMP_ROOT/routine-overlap-baseline"
cp -R "$publication_baseline_dist" "$routine_overlap_baseline"
mkdir "$routine_overlap_baseline/nested-current"
cp -R "$routine_overlap_baseline" "$TMP_ROOT/routine-overlap-baseline-before"
: >"$publication_docker_log"
expect_failure native-requirements-current-inside-baseline env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" native-requirements --environment stage --dist "$routine_overlap_baseline/nested-current" \
  --review "$publication_review" --baseline-dist "$routine_overlap_baseline"
grep -Fq 'routine baseline and current retained tree must be disjoint' "$TMP_ROOT/native-requirements-current-inside-baseline.err" || \
  fail "nested routine current parent missed its disjointness gate"
test ! -s "$publication_docker_log" || fail "nested routine current parent reached Docker"
diff -r "$TMP_ROOT/routine-overlap-baseline-before" "$routine_overlap_baseline" >/dev/null || \
  fail "nested routine current rejection changed baseline retained tree"

routine_review_inside="$TMP_ROOT/routine-review-inside"
cp -R "$publication_dist" "$routine_review_inside"
rm -f "$routine_review_inside/native/requirements.json" "$routine_review_inside/native/evidence.json"
rm -rf "$routine_review_inside/native/manual"
cp "$publication_review" "$routine_review_inside/review.json"
cp -R "$routine_review_inside" "$TMP_ROOT/routine-review-inside-before"
: >"$publication_docker_log"
expect_failure native-requirements-review-inside-current env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" native-requirements --environment stage --dist "$routine_review_inside" \
  --review "$routine_review_inside/review.json" --baseline-dist "$publication_baseline_dist"
grep -Fq 'routine review must be outside current retained tree' "$TMP_ROOT/native-requirements-review-inside-current.err" || \
  fail "routine review inside current missed its containment gate"
test ! -s "$publication_docker_log" || fail "routine review inside current reached Docker"
diff -r "$TMP_ROOT/routine-review-inside-before" "$routine_review_inside" >/dev/null || \
  fail "routine review containment rejection changed current retained tree"

for existing_destination in baseline review.json requirements.json; do
  existing_dist="$TMP_ROOT/routine-existing-$existing_destination"
  cp -R "$publication_dist" "$existing_dist"
  rm -f "$existing_dist/native/requirements.json" "$existing_dist/native/evidence.json"
  rm -rf "$existing_dist/native/manual"
  mkdir -p "$existing_dist/native"
  if [ "$existing_destination" = baseline ]; then
    mkdir "$existing_dist/native/baseline"
  else
    printf '%s\n' existing >"$existing_dist/native/$existing_destination"
  fi
  cp -R "$existing_dist" "$TMP_ROOT/routine-existing-$existing_destination-before"
  : >"$publication_docker_log"
  expect_failure "native-requirements-existing-$existing_destination" env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
    "$fixture/scripts/release.sh" native-requirements --environment stage --dist "$existing_dist" \
    --review "$publication_review" --baseline-dist "$publication_baseline_dist"
  grep -Fq 'routine native destination already exists' "$TMP_ROOT/native-requirements-existing-$existing_destination.err" || \
    fail "existing routine destination missed its preflight gate"
  test ! -s "$publication_docker_log" || fail "existing routine destination reached Docker"
  diff -r "$TMP_ROOT/routine-existing-$existing_destination-before" "$existing_dist" >/dev/null || \
    fail "existing routine destination caused a partial write"
done

: >"$publication_docker_log"
expect_failure native-requirements-unpaired env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" native-requirements --environment stage --dist "$routine_workflow_dist" \
  --review "$publication_review"
test ! -s "$publication_docker_log" || fail "unpaired routine input reached Docker"

review_real_parent="$TMP_ROOT/routine-review-real"
review_link_parent="$TMP_ROOT/routine-review-link"
mkdir "$review_real_parent"
cp "$publication_review" "$review_real_parent/review.json"
ln -s "$review_real_parent" "$review_link_parent"
: >"$publication_docker_log"
expect_failure native-requirements-review-parent-symlink env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" native-requirements --environment stage --dist "$routine_workflow_dist" \
  --review "$review_link_parent/review.json" --baseline-dist "$publication_baseline_dist"
grep -Fq 'output path has an unsafe existing component' "$TMP_ROOT/native-requirements-review-parent-symlink.err" || \
  fail "symlinked routine review missed its path-safety gate"
test ! -s "$publication_docker_log" || fail "symlinked routine review parent reached Docker"

review_template_dir="$TMP_ROOT/native-review-template"
mkdir "$review_template_dir"
review_template_dir=$(cd "$review_template_dir" && pwd -P)
review_template_output="$review_template_dir/routine-review.json"
retained_current_snapshot="$TMP_ROOT/native-review-current-before"
retained_baseline_snapshot="$TMP_ROOT/native-review-baseline-before"
cp -R "$publication_dist" "$retained_current_snapshot"
cp -R "$publication_baseline_dist" "$retained_baseline_snapshot"
for root_case in current baseline; do
  root_current=$publication_dist
  root_baseline=$publication_baseline_dist
  [ "$root_case" = current ] && root_current=/
  [ "$root_case" = baseline ] && root_baseline=/
  : >"$publication_docker_log"
  expect_failure "native-review-template-root-$root_case" env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
    "$fixture/scripts/release.sh" native-review-template --environment stage --dist "$root_current" \
    --baseline-dist "$root_baseline" --output "$review_template_dir/root-$root_case.json"
  grep -Fq 'review template retained parents cannot be filesystem root' "$TMP_ROOT/native-review-template-root-$root_case.err" || \
    fail "review template root rejection missed its policy gate"
  test ! -s "$publication_docker_log" || fail "review template root input reached Docker"
done
for retained_overlap in equal baseline-inside-current current-inside-baseline; do
  overlap_current=$publication_dist
  overlap_baseline=$publication_dist
  if [ "$retained_overlap" = baseline-inside-current ]; then
    overlap_current=$routine_overlap_current
    overlap_baseline=$routine_overlap_current/nested-baseline
  elif [ "$retained_overlap" = current-inside-baseline ]; then
    overlap_current=$routine_overlap_baseline/nested-current
    overlap_baseline=$routine_overlap_baseline
  fi
  overlap_output="$review_template_dir/retained-overlap-$retained_overlap.json"
  : >"$publication_docker_log"
  expect_failure "native-review-template-retained-$retained_overlap" env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
    "$fixture/scripts/release.sh" native-review-template --environment stage --dist "$overlap_current" \
    --baseline-dist "$overlap_baseline" --output "$overlap_output"
  grep -Fq 'review template current and baseline retained trees must be disjoint' \
    "$TMP_ROOT/native-review-template-retained-$retained_overlap.err" || fail "review template retained overlap missed its policy gate"
  test ! -e "$overlap_output" || fail "review template retained overlap created output"
  test ! -s "$publication_docker_log" || fail "review template retained overlap reached Docker"
done
: >"$publication_docker_log"
expect_failure native-review-template-output-root env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" native-review-template --environment stage --dist "$publication_dist" \
  --baseline-dist "$publication_baseline_dist" --output /etc
grep -Fq 'review template output directory cannot be filesystem root' "$TMP_ROOT/native-review-template-output-root.err" || \
  fail "review template root output missed its policy gate"
test ! -s "$publication_docker_log" || fail "review template root output reached Docker"
: >"$publication_docker_log"
expect_failure acceptance-template-output-root env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" acceptance-template --dist "$publication_dist" \
  --output /etc
grep -Fq 'stage acceptance output directory cannot be filesystem root' "$TMP_ROOT/acceptance-template-output-root.err" || \
  fail "acceptance root output missed its policy gate"
test ! -s "$publication_docker_log" || fail "acceptance root output reached Docker"
: >"$publication_docker_log"
expect_failure acceptance-template-root env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" acceptance-template --dist / --output "$review_template_dir/root-acceptance.json"
grep -Fq 'retained output parent cannot be filesystem root' "$TMP_ROOT/acceptance-template-root.err" || \
  fail "acceptance root rejection missed its policy gate"
test ! -s "$publication_docker_log" || fail "acceptance root input reached Docker"
: >"$publication_docker_log"
expect_failure native-review-template-trailing-slash env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" native-review-template --environment stage --dist "$publication_dist" \
  --baseline-dist "$publication_baseline_dist" --output "$review_template_dir/"
test ! -s "$publication_docker_log" || fail "trailing-slash review output reached Docker"
: >"$publication_docker_log"
expect_failure acceptance-template-trailing-slash env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" acceptance-template --dist "$publication_dist" --output "$review_template_dir/"
test ! -s "$publication_docker_log" || fail "trailing-slash acceptance output reached Docker"
for acceptance_output in \
  "$publication_dist/native/stage-acceptance.json" \
  "$publication_dist//native/stage-acceptance-alias.json" \
  "$TMP_ROOT/stage-acceptance-ancestor.json"
do
  : >"$publication_docker_log"
  expect_failure acceptance-template-overlap env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
    "$fixture/scripts/release.sh" acceptance-template --dist "$publication_dist/" --output "$acceptance_output"
  grep -Fq 'stage acceptance output must be outside retained release tree' "$TMP_ROOT/acceptance-template-overlap.err" || \
    fail "acceptance overlap missed its policy gate"
  test ! -s "$publication_docker_log" || fail "overlapping acceptance output reached Docker"
  diff -r "$retained_current_snapshot" "$publication_dist" >/dev/null || fail "acceptance overlap changed retained tree"
done
acceptance_real_parent="$TMP_ROOT/acceptance-real-parent"
acceptance_link_parent="$TMP_ROOT/acceptance-link-parent"
mkdir "$acceptance_real_parent"
ln -s "$acceptance_real_parent" "$acceptance_link_parent"
: >"$publication_docker_log"
expect_failure acceptance-template-symlink-parent env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" acceptance-template --dist "$publication_dist" \
  --output "$acceptance_link_parent/stage-acceptance.json"
grep -Fq 'output path has an unsafe existing component' "$TMP_ROOT/acceptance-template-symlink-parent.err" || \
  fail "symlinked acceptance output missed its path-safety gate"
test ! -s "$publication_docker_log" || fail "symlinked acceptance output reached Docker"
diff -r "$retained_current_snapshot" "$publication_dist" >/dev/null || fail "symlinked acceptance output changed retained tree"
acceptance_output_dir="$TMP_ROOT/operator-records"
acceptance_output="$acceptance_output_dir/stage-acceptance.json"
acceptance_snapshot="$TMP_ROOT/acceptance-published-before"
mkdir "$acceptance_output_dir"
cp -R "$publication_published_dist" "$acceptance_snapshot"
: >"$publication_docker_log"
expect_success acceptance-template-wrapper env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  GITHUB_TOKEN=GITHUB_ACCEPTANCE_SENTINEL GH_TOKEN=GH_ACCEPTANCE_SENTINEL \
  HOOKSPOT_CLI_KEY=HOOKSPOT_ACCEPTANCE_SENTINEL HOOKSPOT_STAGE_CLI_KEY=HOOKSPOT_STAGE_ACCEPTANCE_SENTINEL \
  "$fixture/scripts/release.sh" acceptance-template --dist "$publication_published_dist" --output "$acceptance_output"
grep -Fqx "stage acceptance template: $acceptance_output" "$TMP_ROOT/acceptance-template-wrapper.out" || \
  fail "acceptance template wrapper did not print one host path"
[ "$(wc -l <"$TMP_ROOT/acceptance-template-wrapper.out" | tr -d ' ')" -eq 1 ] || \
  fail "acceptance template wrapper printed multiple success lines"
test -s "$acceptance_output" || fail "acceptance template wrapper did not create its output"
diff -r "$acceptance_snapshot" "$publication_published_dist" >/dev/null || fail "acceptance template changed retained stage tree"
acceptance_call="$TMP_ROOT/acceptance-template-docker-call"
grep -F -- 'publication acceptance-template --dist /out --output /destination/stage-acceptance.json' \
  "$publication_docker_log" >"$acceptance_call"
[ "$(wc -l <"$acceptance_call" | tr -d ' ')" -eq 1 ] || fail "acceptance template did not make one exact helper call"
grep -Fq -- '--network none --read-only' "$acceptance_call" || fail "acceptance template helper lacked network/read-only isolation"
grep -Fq -- "--entrypoint /out/tools/releasecheck-linux-${release_test_image_platform#linux/}" "$acceptance_call" || \
  fail "acceptance template did not use the retained helper entrypoint"
[ "$(awk '{count=0; for (field=1; field<=NF; field++) if ($field=="-v") count++; print count}' "$acceptance_call")" -eq 2 ] || \
  fail "acceptance template helper received an unexpected mount count"
grep -Fq -- '-v '"$publication_published_dist"':/out:ro' "$acceptance_call" || \
  fail "acceptance template retained parent was not read-only"
grep -Fq -- '-v '"$acceptance_output_dir"':/destination --entrypoint' "$acceptance_call" || \
  fail "acceptance template destination was not the one writable mount"
if grep -Eq -- '(GITHUB_TOKEN|GH_TOKEN|HOOKSPOT_)' "$acceptance_call"; then
  fail "acceptance template helper received a credential environment variable"
fi
if grep -Eq -- '(GITHUB_ACCEPTANCE_SENTINEL|GH_ACCEPTANCE_SENTINEL|HOOKSPOT_ACCEPTANCE_SENTINEL|HOOKSPOT_STAGE_ACCEPTANCE_SENTINEL)' \
  "$publication_docker_log" "$TMP_ROOT/acceptance-template-wrapper.out" \
  "$TMP_ROOT/acceptance-template-wrapper.err" "$acceptance_output"; then
  fail "acceptance template boundary disclosed a credential value"
fi
: >"$publication_docker_log"
expect_failure native-review-template-overlap env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" native-review-template --environment stage \
  --dist "$publication_dist" --baseline-dist "$publication_baseline_dist" \
  --output "$publication_dist/native/review-template.json"
test ! -s "$publication_docker_log" || fail "overlapping review template reached Docker"
diff -r "$retained_current_snapshot" "$publication_dist" >/dev/null || fail "overlap check changed current retained tree"
diff -r "$retained_baseline_snapshot" "$publication_baseline_dist" >/dev/null || fail "overlap check changed baseline retained tree"
for overlap_output in "$publication_baseline_dist/native/review-template.json" "$(dirname "$publication_dist")/review-template.json" "$publication_dist//native/alias-review.json"; do
  : >"$publication_docker_log"
  expect_failure native-review-template-overlap-variant env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
    "$fixture/scripts/release.sh" native-review-template --environment stage --dist "$publication_dist/" \
    --baseline-dist "$publication_baseline_dist" --output "$overlap_output"
  test ! -s "$publication_docker_log" || fail "overlap variant reached Docker"
done
symlink_review_parent="$TMP_ROOT/native-review-symlink-parent"
ln -s "$review_template_dir" "$symlink_review_parent"
: >"$publication_docker_log"
expect_failure native-review-template-symlink-parent env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" native-review-template --environment stage --dist "$publication_dist" \
  --baseline-dist "$publication_baseline_dist" --output "$symlink_review_parent/review.json"
grep -Fq 'output path has an unsafe existing component' "$TMP_ROOT/native-review-template-symlink-parent.err" || \
  fail "symlinked review output missed its path-safety gate"
test ! -s "$publication_docker_log" || fail "symlinked output parent reached Docker"
: >"$publication_docker_log"
expect_success native-review-template-wrapper env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  GITHUB_TOKEN=GITHUB_TEMPLATE_SENTINEL GH_TOKEN=GH_TEMPLATE_SENTINEL \
  HOOKSPOT_CLI_KEY=HOOKSPOT_TEMPLATE_SENTINEL HOOKSPOT_DEV_CLI_KEY=HOOKSPOT_DEV_TEMPLATE_SENTINEL \
  "$fixture/scripts/release.sh" native-review-template --environment stage \
  --dist "$publication_dist" --baseline-dist "$publication_baseline_dist" --output "$review_template_output"
test "$(wc -l <"$TMP_ROOT/native-review-template-wrapper.out" | tr -d ' ')" -eq 1 || fail "review template wrapper printed multiple success lines"
grep -Fqx "native review template created and remains incomplete until every blank field is reviewed: $review_template_output" \
  "$TMP_ROOT/native-review-template-wrapper.out" || fail "review template wrapper success message was unclear"
test -s "$review_template_output" || fail "native review template wrapper did not retain output"
diff -r "$retained_current_snapshot" "$publication_dist" >/dev/null || fail "review template changed current retained tree"
diff -r "$retained_baseline_snapshot" "$publication_baseline_dist" >/dev/null || fail "review template changed baseline retained tree"
template_call="$TMP_ROOT/native-review-template-docker-call"
grep -F -- 'native-review-template --dist /out --baseline-dist /baseline --output /destination/routine-review.json' \
  "$publication_docker_log" >"$template_call"
[ "$(wc -l <"$template_call" | tr -d ' ')" -eq 1 ] || fail "review template did not make one exact helper call"
grep -Fq -- '--network none --read-only' "$template_call" || fail "review template helper lacked network/read-only isolation"
grep -Fq -- "--entrypoint /out/tools/releasecheck-linux-${release_test_image_platform#linux/}" "$template_call" || \
  fail "review template did not use the retained helper entrypoint"
[ "$(awk '{count=0; for (field=1; field<=NF; field++) if ($field=="-v") count++; print count}' "$template_call")" -eq 3 ] || \
  fail "review template helper received an unexpected mount count"
grep -Fq -- '-v '"$publication_dist"':/out:ro' "$template_call" || fail "review template current parent was not read-only"
grep -Fq -- '-v '"$publication_baseline_dist"':/baseline:ro' "$template_call" || fail "review template baseline was not read-only"
grep -Fq -- '-v '"$review_template_dir"':/destination --entrypoint' "$template_call" || \
  fail "review template destination was not the one writable mount"
if grep -Eq -- '(GITHUB_TOKEN|GH_TOKEN|HOOKSPOT_)' "$template_call"; then
  fail "review template helper received a credential environment variable"
fi
if grep -Eq -- '(GITHUB_TEMPLATE_SENTINEL|GH_TEMPLATE_SENTINEL|HOOKSPOT_TEMPLATE_SENTINEL|HOOKSPOT_DEV_TEMPLATE_SENTINEL)' \
  "$publication_docker_log" "$TMP_ROOT/native-review-template-wrapper.out" \
  "$TMP_ROOT/native-review-template-wrapper.err" "$review_template_output"; then
  fail "review template boundary disclosed a credential value"
fi
: >"$publication_docker_log"
expect_failure native-review-template-existing-output env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" native-review-template --environment stage \
  --dist "$publication_dist" --baseline-dist "$publication_baseline_dist" --output "$review_template_output"
test ! -s "$publication_docker_log" || fail "existing review template reached Docker"

expect_failure native-evidence-raw-only env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$fixture/scripts/release.sh" native-evidence --environment stage --dist "$native_workflow_dist"
test ! -e "$native_workflow_dist/native/evidence.json" || fail "raw smoke reports alone created native evidence"

for manual_spec in \
  'linux/amd64:network' \
  'windows/amd64:config-private' 'windows/amd64:password-input' 'windows/amd64:signal-cancel' \
  'windows/arm64:config-private' 'windows/arm64:password-input' 'windows/arm64:signal-cancel'
do
  manual_target=${manual_spec%%:*}
  manual_check=${manual_spec#*:}
  manual_procedure=fixture-procedure-v1
  case "$manual_check" in
    network) manual_procedure=network_release_v1 ;;
    config-private) manual_procedure=windows_config_private_v1 ;;
    password-input) manual_procedure=windows_password_input_v1 ;;
    signal-cancel) manual_procedure=windows_signal_cancel_v1 ;;
  esac
  expect_success "native-manual-${manual_target//\//-}-$manual_check" env PATH="$publication_bin:$PATH" \
    PUBLICATION_DOCKER_LOG="$publication_docker_log" GITHUB_TOKEN=TOKEN_BOUNDARY_SENTINEL GH_TOKEN=TOKEN_BOUNDARY_SENTINEL \
    "$fixture/scripts/release.sh" native-manual --environment stage --dist "$native_workflow_dist" \
    --target "$manual_target" --check "$manual_check" --result pass --operator fixture-operator --procedure "$manual_procedure"
done
expect_success native-evidence-wrapper env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  GITHUB_TOKEN=TOKEN_BOUNDARY_SENTINEL GH_TOKEN=TOKEN_BOUNDARY_SENTINEL \
  "$fixture/scripts/release.sh" native-evidence --environment stage --dist "$native_workflow_dist"
test -s "$native_workflow_dist/native/evidence.json" || fail "native evidence wrapper did not retain complete evidence"
if grep -- '--entrypoint /out/tools/releasecheck-linux-' "$publication_docker_log" | grep -Eq -- '-e (GITHUB_TOKEN|GH_TOKEN)'; then
  fail "native evidence wrapper passed a publication token to the retained helper"
fi

eligibility_probe="$TMP_ROOT/publication-eligibility-probe.sh"
cat >"$eligibility_probe" <<'EOF'
#!/bin/bash
set -eu
production=$1
dist=$2
notes=$3
image_platform=$4
eval "$(sed '/^command=${1:-}/,$d' "$production")"
trap - EXIT HUP INT TERM
environment=stage
tag=v1.2.3-stage.1
resume_mode=false
stage_acceptance=
from_stage_tag=
IMAGE_ID=sha256:76ded7488668ae5e9e9bc2267cce23c21c1fb11c21e84347f7474c6f3d095c56
IMAGE_PLATFORM=$image_platform
mkdir -p "$dist"
verify_output() { :; }
allocate_publication_temp() { PUBLICATION_TEMP=$dist; }
read_candidate_identity() { repository=example/repo; tag_object=aaaa; tag_commit=bbbb; }
enforce_new_source_policy() { :; }
check_publisher_access() { :; }
container_run() {
  case "$*" in *'--entrypoint release-helper-check'*) return 0 ;; *) return 1 ;; esac
}
publish_retained false
EOF
chmod 755 "$eligibility_probe"
printf '%s\n' notes >"$TMP_ROOT/eligibility-notes.md"
expect_failure publication-eligibility-guidance "$eligibility_probe" "$SCRIPT" "$TMP_ROOT/missing-native-parent" \
  "$TMP_ROOT/eligibility-notes.md" "$release_test_image_platform"
grep -q "retained parent: $TMP_ROOT/missing-native-parent" "$TMP_ROOT/publication-eligibility-guidance.err" || fail "missing native evidence omitted retained parent"
grep -q 'docs/releases/RUNBOOK.md#native-evidence' "$TMP_ROOT/publication-eligibility-guidance.err" || fail "missing native evidence omitted the complete workflow section"
if grep -q 'scripts/smoke.sh' "$TMP_ROOT/publication-eligibility-guidance.err"; then
  fail "publication error implied one raw smoke report completes native evidence"
fi

"$REAL_GIT" -C "$fixture" config remote.origin.mirror true
expect_failure publication-mirror env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" resume --environment stage --tag v1.2.3-stage.1 --dist "$publication_dist"
"$REAL_GIT" -C "$fixture" config --unset remote.origin.mirror

expect_success publication-resume env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" PUBLICATION_STATE="$publication_state" \
  GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL "$fixture/scripts/release.sh" resume --environment stage \
  --tag v1.2.3-stage.1 --dist "$publication_dist"
grep -q 'release published: v1.2.3-stage.1' "$TMP_ROOT/publication-resume.out" || fail "stateful publication resume did not complete"
test ! -e "$publication_hook_marker" || fail "host Git received the publisher token"
grep -q -- '-c core.hooksPath=/dev/null' "$publication_git_log" || fail "publication Git did not disable repository hooks"
if grep -q 'https://github.com/example/hookspot-cli.git:refs/tags' "$publication_git_log"; then
  fail "publication re-injected an already resolved Git destination"
fi
grep -q -- '--method GET --paginate --slurp repos/example/hookspot-cli/releases?per_page=100' "$publication_docker_log" || fail "publication did not request complete release pages with GET"
grep -q " release upload .* /assets/$publication_first_asset" "$publication_docker_log" || fail "resume did not upload the missing allowlist"
if grep -Eq -- '--clobber|--force|--tags' "$publication_docker_log" "$publication_git_log"; then
  fail "publication used a destructive upload or push option"
fi
if grep -q 'PUBLICATION_TOKEN_SENTINEL' "$publication_docker_log" "$publication_git_log"; then
  fail "publication exposed a credential literal in child arguments"
fi

: >"$publication_write_log"
expect_failure publication-duplicate-fake env PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$publication_bin/docker" run --entrypoint gh synthetic-image release upload "$publication_tag" \
  --repo example/hookspot-cli "/assets/$publication_first_asset"
test ! -s "$publication_write_log" || fail "duplicate fake upload changed remote state"

: >"$publication_write_log"
: >"$publication_upload_attempts"
expect_failure publication-empty-upload-fake env PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  "$publication_bin/docker" run --entrypoint gh synthetic-image release upload "$publication_tag" \
  --repo example/hookspot-cli
test ! -s "$publication_write_log" && test ! -s "$publication_upload_attempts" || \
  fail "empty fake upload changed remote state"

cp "$publication_state/assets" "$publication_state/assets.before-duplicate-request"
awk -F '\t' -v first="$publication_first_asset" -v second="$publication_second_asset" '$2!=first && $2!=second' \
  "$publication_state/assets.before-duplicate-request" >"$publication_state/assets"
: >"$publication_write_log"
: >"$publication_upload_attempts"
set +e
env PUBLICATION_DOCKER_LOG="$publication_docker_log" "$publication_bin/docker" run --entrypoint gh synthetic-image \
  release upload "$publication_tag" --repo example/hookspot-cli "/assets/$publication_first_asset" \
  "/assets/$publication_second_asset" "/assets/$publication_first_asset"
duplicate_request_status=$?
set -e
mv "$publication_state/assets.before-duplicate-request" "$publication_state/assets"
[ "$duplicate_request_status" -ne 0 ] || fail "duplicate names in one fake upload were accepted"
test ! -s "$publication_write_log" && test ! -s "$publication_upload_attempts" || fail "duplicate request changed remote state"

printf '%s\n' 'different reviewed notes' >"$TMP_ROOT/different-notes.md"
: >"$publication_write_log"
expect_failure publication-notes-conflict env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" publish --environment stage --tag v1.2.3-stage.1 --dist "$publication_dist" \
  --notes "$TMP_ROOT/different-notes.md"
test ! -s "$publication_write_log" || fail "conflicting notes reached a remote write"

cp "$publication_dist/release-notes.md" "$TMP_ROOT/same-notes.md"
: >"$publication_write_log"
expect_success publication-same-notes env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" publish --environment stage --tag "$publication_tag" --dist "$publication_dist" \
  --notes "$TMP_ROOT/same-notes.md"
test ! -s "$publication_write_log" || fail "identical notes retry performed another remote write"

: >"$publication_write_log"
expect_failure publication-ref-conflict env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" publish --environment stage --tag "$publication_tag" --dist "$publication_dist" \
  --ref HEAD --notes "$publication_dist/release-notes.md"
test ! -s "$publication_write_log" || fail "conflicting explicit source ref reached a remote write"

for prod_conflict in stage-tag acceptance; do
  prod_acceptance="$publication_prod_dist/stage-acceptance.json"
  prod_stage_tag=v1.2.3-stage.1
  if [ "$prod_conflict" = stage-tag ]; then
    prod_stage_tag=v1.2.3-stage.2
  else
    prod_acceptance="$TMP_ROOT/changed-stage-acceptance.json"
    cp "$publication_prod_dist/stage-acceptance.json" "$prod_acceptance"
    printf '\n' >>"$prod_acceptance"
  fi
  : >"$publication_write_log"
  expect_failure "publication-prod-$prod_conflict" env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
    PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
    "$fixture/scripts/release.sh" publish --environment prod --tag v1.2.3 --dist "$publication_prod_dist" \
    --notes "$publication_prod_dist/release-notes.md" --from-stage-tag "$prod_stage_tag" --stage-acceptance "$prod_acceptance"
  test ! -s "$publication_write_log" || fail "conflicting production input reached a remote write"
done

reset_publication_remote() {
  remote_status=$1
  remote_tag=$2
  cp "$publication_initial" "$publication_dist/publication.json"
  printf '%s\n' "$remote_status" >"$publication_state/release"
  printf '%s\n' owned >"$publication_state/body"
  : >"$publication_state/assets"
  : >"$publication_write_log"
  if [ "$remote_tag" = present ]; then touch "$publication_state/tag"; else rm -f "$publication_state/tag"; fi
}

remove_publication_state() {
  rm -f "$publication_dist/publication.json"
}


: >"$publication_write_log"
expect_success publication-complete-again env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" PUBLICATION_STATE="$publication_state" \
  GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL "$fixture/scripts/release.sh" resume --environment stage \
  --tag "$publication_tag" --dist "$publication_dist"
test ! -s "$publication_write_log" || fail "already complete publication performed another write"

reset_publication_remote absent absent
: >"$publication_git_log"
expect_failure publication-push-loss env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" PUBLICATION_FAIL_AFTER=push GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" resume --environment stage --tag "$publication_tag" --dist "$publication_dist"
test -f "$publication_state/tag" || fail "push response loss did not preserve the applied remote tag"
grep -q "$publication_tag_object:refs/tags/$publication_tag" "$publication_git_log" || fail "publisher did not push the exact retained tag object"
: >"$publication_git_log"
expect_success publication-push-resume env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" PUBLICATION_STATE="$publication_state" \
  GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL "$fixture/scripts/release.sh" resume --environment stage \
  --tag "$publication_tag" --dist "$publication_dist"
if grep -q ' push ' "$publication_git_log"; then fail "resume pushed an already exact tag again"; fi

reset_publication_remote absent absent
expect_failure publication-create-loss env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" PUBLICATION_FAIL_AFTER=create GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" resume --environment stage --tag "$publication_tag" --dist "$publication_dist"
[ "$(cat "$publication_state/release")" = draft ] || fail "create response loss did not preserve the applied draft"
expect_success publication-create-resume env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" PUBLICATION_STATE="$publication_state" \
  GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL "$fixture/scripts/release.sh" resume --environment stage \
  --tag "$publication_tag" --dist "$publication_dist"

for upload_boundary in 1 2 3 4 5 6 7; do
  reset_publication_remote draft present
  : >"$publication_upload_attempts"
  expect_failure "publication-upload-$upload_boundary" env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
    PUBLICATION_STATE="$publication_state" PUBLICATION_FAIL_AFTER="upload:$upload_boundary" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
    "$fixture/scripts/release.sh" resume --environment stage --tag "$publication_tag" --dist "$publication_dist"
  [ "$(wc -l <"$publication_state/assets" | tr -d ' ')" -eq "$upload_boundary" ] || fail "upload response loss retained the wrong partial subset"
  tail -n "+$((upload_boundary + 1))" "$publication_state/asset-names" >"$publication_state/expected-resume-attempts"
  : >"$publication_upload_attempts"
  expect_success "publication-upload-$upload_boundary-resume" env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" PUBLICATION_STATE="$publication_state" \
    GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL "$fixture/scripts/release.sh" resume --environment stage \
    --tag "$publication_tag" --dist "$publication_dist"
  cmp -s "$publication_state/expected-resume-attempts" "$publication_upload_attempts" || \
    fail "partial resume did not attempt exactly the remaining asset names"
done

reset_publication_remote draft present
expect_success publication-edit-loss env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" PUBLICATION_STATE="$publication_state" \
  PUBLICATION_FAIL_AFTER=edit GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL "$fixture/scripts/release.sh" resume --environment stage \
  --tag "$publication_tag" --dist "$publication_dist"
[ "$(cat "$publication_state/release")" = published ] || fail "finalize response loss did not retain the published release"
: >"$publication_write_log"
expect_success publication-edit-resume env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" PUBLICATION_STATE="$publication_state" \
  GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL "$fixture/scripts/release.sh" resume --environment stage \
  --tag "$publication_tag" --dist "$publication_dist"
test ! -s "$publication_write_log" || fail "finalize response-loss recovery repeated a remote write"

reset_publication_remote draft present
head -3 "$publication_state/asset-names" | awk '{printf "%d\t%s\n", NR+99, $0}' >"$publication_state/assets"
remove_publication_state
: >"$publication_git_log"
printf '%s\n' advanced >>"$fixture/payload"
"$REAL_GIT" -C "$fixture" add payload
"$REAL_GIT" -C "$fixture" commit -qm 'advance branch after draft'
expect_success publication-lost-record env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" PUBLICATION_STATE="$publication_state" \
  GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL "$fixture/scripts/release.sh" resume --environment stage \
  --tag "$publication_tag" --dist "$publication_dist"
test -s "$publication_dist/publication.json" || fail "owned draft did not restore lost local publication state"
if grep -Eq '(^| )fetch( |$)' "$publication_git_log"; then fail "lost-record resume reapplied new-source policy"; fi

reset_publication_remote draft present
remove_publication_state
printf '%s\n' foreign >"$publication_state/body"
expect_failure publication-foreign-marker env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" resume --environment stage --tag "$publication_tag" --dist "$publication_dist"
test ! -e "$publication_dist/publication.json" || fail "foreign draft restored local publication state"

for changed_input in notes evidence manifest; do
  reset_publication_remote draft present
  remove_publication_state
  if [ "$changed_input" = notes ]; then
    changed_path=/out/release-notes.md
  elif [ "$changed_input" = evidence ]; then
    changed_path=/out/native/evidence.json
  else
    changed_path=/out/controls/release/environments.json
  fi
  printf '\n' >>"$publication_dist/${changed_path#/out/}"
  expect_failure "publication-changed-$changed_input" env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
    PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
    "$fixture/scripts/release.sh" resume --environment stage --tag "$publication_tag" --dist "$publication_dist"
  test ! -e "$publication_dist/publication.json" || fail "changed $changed_input restored local publication state"
  cp "$TMP_ROOT/release-notes.initial" "$publication_dist/release-notes.md"
  cp "$TMP_ROOT/native-evidence.initial" "$publication_dist/native/evidence.json"
  cp "$TMP_ROOT/environment-manifest.initial" "$publication_dist/controls/release/environments.json"
done

reset_publication_remote draft present
expect_failure publication-denied env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" PUBLICATION_DENY_ACCESS=1 GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" resume --environment stage --tag "$publication_tag" --dist "$publication_dist"
test ! -s "$publication_write_log" || fail "denied publisher access reached a remote write"

reset_publication_remote draft present
expect_success publication-id-baseline env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" PUBLICATION_STATE="$publication_state" \
  GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL "$fixture/scripts/release.sh" resume --environment stage \
  --tag "$publication_tag" --dist "$publication_dist"
sed '1s/^100/900/' "$publication_state/assets" >"$publication_state/assets.changed"
mv "$publication_state/assets.changed" "$publication_state/assets"
: >"$publication_write_log"
expect_failure publication-changed-id env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" resume --environment stage --tag "$publication_tag" --dist "$publication_dist"
test ! -s "$publication_write_log" || fail "changed remote asset ID reached a write"

sed '1s/^900/100/' "$publication_state/assets" >"$publication_state/assets.changed"
mv "$publication_state/assets.changed" "$publication_state/assets"
expect_failure publication-changed-bytes env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" PUBLICATION_CORRUPT_ASSET="$publication_first_asset" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" resume --environment stage --tag "$publication_tag" --dist "$publication_dist"

grep -q -- 'push.followTags=false' "$publication_git_log" || fail "publisher did not suppress ambient follow-tags"
if grep -q 'https://github.com/example/hookspot-cli.git:refs/tags' "$publication_git_log"; then
  fail "publisher re-injected the checked URL during tag push"
fi
test ! -e "$publication_hook_marker" || fail "tag push exposed the publisher token to Git hooks"

# Status uses a committed source identity matching the retained publication.
printf '%s\n' '{"schema_version":1,"repository":"example/hookspot-cli","stage":{"server_url":"https://stage.example.invalid","branch":"stage"},"prod":{"server_url":"https://prod.example.invalid","branch":"main"}}' >"$fixture/release/environments.json"
"$REAL_GIT" -C "$fixture" add release/environments.json
if ! "$REAL_GIT" -C "$fixture" diff --cached --quiet; then
  "$REAL_GIT" -C "$fixture" commit -qm status-fixture
fi
"$REAL_GIT" -C "$fixture" remote set-url origin chain:hookspot-cli.git
status_snapshot="$TMP_ROOT/status-dist-before"

assert_status_dist_mounts_read_only() {
  saw_status_dist_mount=
  while IFS= read -r docker_call; do
    dist_mount_count=0
    for docker_arg in $docker_call; do
      case "$docker_arg" in
        "$publication_dist":*)
          dist_mount_count=$((dist_mount_count + 1))
          [ "$docker_arg" = "$publication_dist:/out:ro" ] || fail "status used a writable or unexpected retained DIST mount"
          ;;
      esac
    done
    [ "$dist_mount_count" -le 1 ] || fail "status mounted retained DIST more than once in one container"
    [ "$dist_mount_count" -eq 0 ] || saw_status_dist_mount=1
  done <"$publication_docker_log"
  [ -n "$saw_status_dist_mount" ] || fail "status did not inspect retained DIST"
}

assert_no_remote_git_mutation() {
  if grep -Eq '(^| )push( |$)|(^| )tag( |$)|(^| )fetch .* origin' "$publication_git_log"; then
    fail "status attempted a remote Git mutation"
  fi
}

assert_status_git_read_only() {
  grep -q 'ls-remote origin' "$publication_git_log" || fail "status did not inspect the remote tag"
  assert_no_remote_git_mutation
}

assert_status_token_hidden() {
  if grep -q 'PUBLICATION_TOKEN_SENTINEL' "$@"; then
    fail "status disclosed its token"
  fi
}

: >"$publication_docker_log"
: >"$publication_git_log"
: >"$publication_write_log"
expect_failure status-wrong-channel-tag env -u GITHUB_TOKEN -u GH_TOKEN PATH="$publication_bin:$PATH" \
  PUBLICATION_DOCKER_LOG="$publication_docker_log" PUBLICATION_STATE="$publication_state" \
  "$fixture/scripts/release.sh" status --environment stage --tag v1.2.3
grep -Fq 'stage tag must use vMAJOR.MINOR.PATCH-stage.N' "$TMP_ROOT/status-wrong-channel-tag.err" || \
  fail "status wrong-channel tag missed argument validation"
test ! -s "$publication_docker_log" || fail "invalid status tag reached Docker"
test ! -s "$publication_git_log" || fail "invalid status tag reached Git"
test ! -s "$publication_write_log" || fail "invalid status tag reached a remote write"

: >"$publication_docker_log"
: >"$publication_git_log"
: >"$publication_write_log"
expect_failure status-root-dist env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" status --environment stage --tag "$publication_tag" --dist /
grep -Fq 'status --dist cannot be filesystem root' "$TMP_ROOT/status-root-dist.err" || fail "status root DIST diagnostic changed"
test ! -s "$publication_docker_log" || fail "status root DIST reached Docker"
test ! -s "$publication_git_log" || fail "status root DIST reached Git"
test ! -s "$publication_write_log" || fail "status root DIST reached a remote write"

rm -rf "$status_snapshot"
cp -R "$publication_dist" "$status_snapshot"
: >"$publication_docker_log"
: >"$publication_git_log"
: >"$publication_write_log"
expect_failure status-root-tmp env PATH="$publication_bin:$PATH" TMPDIR=/ PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" status --environment stage --tag "$publication_tag" --dist "$publication_dist"
grep -Fq 'status temporary directory cannot be filesystem root' "$TMP_ROOT/status-root-tmp.err" || fail "status root TMPDIR diagnostic changed"
diff -r "$status_snapshot" "$publication_dist" >/dev/null || fail "status root TMPDIR changed retained DIST"
test ! -s "$publication_docker_log" || fail "status root TMPDIR reached Docker"
test ! -s "$publication_git_log" || fail "status root TMPDIR reached Git"
test ! -s "$publication_write_log" || fail "status root TMPDIR reached a remote write"

rm -rf "$status_snapshot"
cp -R "$publication_dist" "$status_snapshot"
: >"$publication_docker_log"
: >"$publication_git_log"
: >"$publication_write_log"
expect_failure status-tmp-inside-dist env PATH="$publication_bin:$PATH" TMPDIR="$publication_dist" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" status --environment stage --tag "$publication_tag" --dist "$publication_dist"
grep -Fq 'status temporary directory must be outside retained DIST' "$TMP_ROOT/status-tmp-inside-dist.err" || fail "status TMPDIR diagnostic changed"
diff -r "$status_snapshot" "$publication_dist" >/dev/null || fail "unsafe status TMPDIR changed retained DIST"
test ! -s "$publication_docker_log" || fail "unsafe status TMPDIR reached Docker"
test ! -s "$publication_git_log" || fail "unsafe status TMPDIR reached Git"
test ! -s "$publication_write_log" || fail "unsafe status TMPDIR reached a remote write"
for status_tag_state in absent present; do
  reset_publication_remote absent "$status_tag_state"
  rm -rf "$status_snapshot"
  cp -R "$publication_dist" "$status_snapshot"
  : >"$publication_docker_log"
  : >"$publication_git_log"
  : >"$publication_write_log"
  rm -f "$publication_hook_marker"
  expect_success "status-$status_tag_state" env -u TMPDIR PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
    PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
    "$fixture/scripts/release.sh" status --environment stage --tag "$publication_tag" --dist "$publication_dist"
  expected_tag=$status_tag_state; [ "$status_tag_state" = present ] && expected_tag=exact
  grep -Fqx $'tag\t'"$expected_tag" "$TMP_ROOT/status-$status_tag_state.out" || fail "status tag result was not exact"
  diff -r "$status_snapshot" "$publication_dist" >/dev/null || fail "status changed retained DIST"
  test ! -s "$publication_write_log" || fail "status performed a remote write"
  assert_status_dist_mounts_read_only
  assert_status_git_read_only
  assert_status_token_hidden "$TMP_ROOT/status-$status_tag_state.out" "$TMP_ROOT/status-$status_tag_state.err" \
    "$publication_docker_log" "$publication_git_log" "$publication_write_log"
  test ! -e "$publication_hook_marker" || fail "status exposed token to Git hook"
done
status_tmp_real="$TMP_ROOT/status-real-temp"
status_tmp_alias="$TMP_ROOT/status-temp-alias"
mkdir "$status_tmp_real"
ln -s "$status_tmp_real" "$status_tmp_alias"
reset_publication_remote absent absent
rm -rf "$status_snapshot"
cp -R "$publication_dist" "$status_snapshot"
: >"$publication_docker_log"
: >"$publication_git_log"
: >"$publication_write_log"
rm -f "$publication_hook_marker"
expect_success status-symlink-tmp env PATH="$publication_bin:$PATH" TMPDIR="$status_tmp_alias" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" status --environment stage --tag "$publication_tag" --dist "$publication_dist"
grep -Fqx $'tag\tabsent' "$TMP_ROOT/status-symlink-tmp.out" || fail "canonical TMPDIR status result was not absent"
diff -r "$status_snapshot" "$publication_dist" >/dev/null || fail "canonical TMPDIR status changed retained DIST"
test ! -s "$publication_write_log" || fail "canonical TMPDIR status performed a write"
assert_status_dist_mounts_read_only
assert_status_git_read_only
assert_status_token_hidden "$TMP_ROOT/status-symlink-tmp.out" "$TMP_ROOT/status-symlink-tmp.err" \
  "$publication_docker_log" "$publication_git_log" "$publication_write_log"
grep -Fq "$status_tmp_real/" "$publication_docker_log" || fail "status did not use the canonical temporary root"
if grep -Fq "$status_tmp_alias" "$publication_docker_log" "$publication_git_log"; then
  fail "status reused the symlinked temporary-root spelling"
fi
test ! -e "$publication_hook_marker" || fail "canonical TMPDIR status exposed token to Git hook"
reset_publication_remote draft absent
rm -rf "$status_snapshot"
cp -R "$publication_dist" "$status_snapshot"
: >"$publication_docker_log"
: >"$publication_git_log"
: >"$publication_write_log"
rm -f "$publication_hook_marker"
expect_failure status-draft-missing-tag env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" status --environment stage --tag "$publication_tag" --dist "$publication_dist"
grep -Fq 'remote release exists without the retained tag identity' "$TMP_ROOT/status-draft-missing-tag.err" || fail "status missing-tag diagnostic changed"
diff -r "$status_snapshot" "$publication_dist" >/dev/null || fail "missing-tag status changed retained DIST"
test ! -s "$publication_write_log" || fail "failed status performed a write"
assert_status_dist_mounts_read_only
assert_status_git_read_only
assert_status_token_hidden "$TMP_ROOT/status-draft-missing-tag.out" "$TMP_ROOT/status-draft-missing-tag.err" \
  "$publication_docker_log" "$publication_git_log" "$publication_write_log"
test ! -e "$publication_hook_marker" || fail "failed status exposed token to Git hook"
reset_publication_remote absent present
: >"$publication_docker_log"
: >"$publication_git_log"
: >"$publication_write_log"
rm -rf "$status_snapshot"
cp -R "$publication_dist" "$status_snapshot"
rm -f "$publication_hook_marker"
expect_failure status-mismatched-tag env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" PUBLICATION_TAG_MISMATCH=1 GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" status --environment stage --tag "$publication_tag" --dist "$publication_dist"
grep -Fq 'remote tag identity conflicts with retained publication' "$TMP_ROOT/status-mismatched-tag.err" || fail "status mismatch diagnostic changed"
diff -r "$status_snapshot" "$publication_dist" >/dev/null || fail "mismatched-tag status changed retained DIST"
test ! -s "$publication_write_log" || fail "mismatched-tag status performed a write"
assert_status_dist_mounts_read_only
assert_status_git_read_only
assert_status_token_hidden "$TMP_ROOT/status-mismatched-tag.out" "$TMP_ROOT/status-mismatched-tag.err" \
  "$publication_docker_log" "$publication_git_log" "$publication_write_log"
test ! -e "$publication_hook_marker" || fail "mismatched status exposed token to Git hook"
reset_publication_remote published present
: >"$publication_docker_log"
: >"$publication_git_log"
: >"$publication_write_log"
rm -f "$publication_hook_marker"
expect_success status-no-dist env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" status --environment stage --tag "$publication_tag"
grep -Fqx '{"databaseId":42,"tagName":"v1.2.3-stage.1","isDraft":true,"isPrerelease":true}' "$TMP_ROOT/status-no-dist.out" || fail "status lookup output changed"
test ! -s "$publication_write_log" || fail "status lookup performed a write"
assert_no_remote_git_mutation
assert_status_token_hidden "$TMP_ROOT/status-no-dist.out" "$TMP_ROOT/status-no-dist.err" \
  "$publication_docker_log" "$publication_git_log" "$publication_write_log"
test ! -e "$publication_hook_marker" || fail "status lookup exposed token to Git hook"
: >"$publication_docker_log"
: >"$publication_git_log"
: >"$publication_write_log"
rm -f "$publication_hook_marker"
expect_failure status-no-dist-failure env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" PUBLICATION_STATUS_VIEW_FAIL=1 GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" status --environment stage --tag "$publication_tag"
grep -Fq 'release status is unavailable; no absence is inferred' "$TMP_ROOT/status-no-dist-failure.err" || fail "status lookup failure diagnostic changed"
if grep -q 'absent' "$TMP_ROOT/status-no-dist-failure.out"; then fail "failed status lookup reported absence"; fi
test ! -s "$publication_write_log" || fail "failed status lookup performed a write"
assert_no_remote_git_mutation
assert_status_token_hidden "$TMP_ROOT/status-no-dist-failure.out" "$TMP_ROOT/status-no-dist-failure.err" \
  "$publication_docker_log" "$publication_git_log" "$publication_write_log"
test ! -e "$publication_hook_marker" || fail "failed status lookup exposed token to Git hook"
"$REAL_GIT" -C "$fixture" remote set-url origin chain:repo.git
rm -rf "$status_snapshot"
cp -R "$publication_dist" "$status_snapshot"
: >"$publication_docker_log"
: >"$publication_git_log"
: >"$publication_write_log"
rm -f "$publication_hook_marker"
expect_failure status-repository-mismatch env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" PUBLICATION_SOURCE_REPOSITORY=example/repo GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" status --environment stage --tag "$publication_tag" --dist "$publication_dist"
grep -Fq 'retained publication repository differs from selected source' "$TMP_ROOT/status-repository-mismatch.err" || fail "status repository mismatch diagnostic changed"
diff -r "$status_snapshot" "$publication_dist" >/dev/null || fail "repository mismatch status changed retained DIST"
test ! -s "$publication_write_log" || fail "repository mismatch status performed a write"
if grep -q 'ls-remote' "$publication_git_log"; then fail "repository mismatch reached retained tag lookup"; fi
if grep -q 'releases?per_page=100' "$publication_docker_log"; then fail "repository mismatch reached retained release lookup"; fi
assert_status_dist_mounts_read_only
if grep -Eq '(^| )push( |$)|(^| )tag( |$)|(^| )fetch .* origin' "$publication_git_log"; then
  fail "repository mismatch status attempted a remote Git mutation"
fi
assert_status_token_hidden "$TMP_ROOT/status-repository-mismatch.out" "$TMP_ROOT/status-repository-mismatch.err" \
  "$publication_docker_log" "$publication_git_log" "$publication_write_log"
test ! -e "$publication_hook_marker" || fail "repository mismatch status exposed token to Git hook"
"$REAL_GIT" -C "$fixture" remote set-url origin chain:hookspot-cli.git

reset_publication_remote absent present
: >"$publication_docker_log"
: >"$publication_git_log"
: >"$publication_write_log"
expect_success resume-exact-tag-no-release env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" resume --environment stage --tag "$publication_tag" --dist "$publication_dist"
grep -Fq 'WRITE create' "$publication_write_log" || fail "exact-tag resume did not create the missing release"
if grep -Eq 'WRITE push|(^| )push( |$)' "$publication_write_log" "$publication_git_log"; then
  fail "exact-tag resume pushed the retained tag again"
fi

reset_publication_remote absent present
remove_publication_state
: >"$publication_docker_log"
: >"$publication_write_log"
: >"$publication_git_log"
rm -rf "$status_snapshot"
cp -R "$publication_dist" "$status_snapshot"
expect_failure resume-without-publication-state env PATH="$publication_bin:$PATH" PUBLICATION_DOCKER_LOG="$publication_docker_log" \
  PUBLICATION_STATE="$publication_state" GITHUB_TOKEN=PUBLICATION_TOKEN_SENTINEL \
  "$fixture/scripts/release.sh" resume --environment stage --tag "$publication_tag" --dist "$publication_dist"
grep -Fq 'lost publication state does not match an exact owned remote release' "$TMP_ROOT/resume-without-publication-state.err" || \
  fail "missing publication state did not reach the lost-state gate"
test ! -e "$publication_dist/publication.json" || fail "resume recreated missing publication state"
diff -r "$status_snapshot" "$publication_dist" >/dev/null || fail "resume without publication state changed retained DIST"
test ! -s "$publication_write_log" || fail "resume without publication state performed a write"
if grep -Eq 'WRITE push|(^| )push( |$)' "$publication_write_log" "$publication_git_log"; then
  fail "resume without publication state pushed a tag"
fi
assert_status_token_hidden "$TMP_ROOT/resume-without-publication-state.out" "$TMP_ROOT/resume-without-publication-state.err" \
  "$publication_docker_log" "$publication_git_log" "$publication_write_log"

echo "release shell tests passed"
