#!/bin/bash
set +x
set -eu
set -o pipefail
set -f

RELEASE_INTERFACE_VERSION=8
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd -P)
REPOSITORY_ROOT=$(cd "$SCRIPT_DIR/.." && pwd -P)
SOURCE_TEMP=
PUBLICATION_TEMP=
PUBLICATION_LOCK=
PUBLICATION_LOCK_OWNER=

cleanup() {
  if [ -n "$SOURCE_TEMP" ] && [ -d "$SOURCE_TEMP" ]; then
    rm -rf "$SOURCE_TEMP"
  fi
  if [ -n "$PUBLICATION_TEMP" ] && [ -d "$PUBLICATION_TEMP" ]; then
    rm -rf "$PUBLICATION_TEMP"
  fi
  if [ -n "$PUBLICATION_LOCK" ] && [ -d "$PUBLICATION_LOCK" ]; then
    if [ -n "$PUBLICATION_LOCK_OWNER" ] && [ "$(cat "$PUBLICATION_LOCK/owner" 2>/dev/null || true)" = "$PUBLICATION_LOCK_OWNER" ]; then
      rm -rf "$PUBLICATION_LOCK"
    fi
  fi
}

terminate() {
  signal=$1
  status=$2
  trap - "$signal"
  cleanup
  kill -"$signal" "$$"
  exit "$status"
}

trap cleanup EXIT
trap 'terminate HUP 129' HUP
trap 'terminate INT 130' INT
trap 'terminate TERM 143' TERM

die() {
  echo "release: $1" >&2
  exit 1
}

usage() {
  case "${command-}" in
    check) echo "usage: scripts/release.sh check --environment stage|prod" >&2 ;;
    snapshot) echo "usage: scripts/release.sh snapshot --environment stage|prod [--ref REF]" >&2 ;;
    build) echo "usage: scripts/release.sh build --environment stage|prod --tag TAG" >&2 ;;
    verify) echo "usage: scripts/release.sh verify --environment stage|prod --dist /absolute/PARENT" >&2 ;;
    publish) echo "usage: scripts/release.sh publish --environment stage|prod --tag TAG --ref REF --notes /absolute/FILE [--dist /absolute/PARENT] [--from-stage-tag TAG --stage-acceptance /absolute/FILE]" >&2 ;;
    resume) echo "usage: scripts/release.sh resume --environment stage|prod --tag TAG --dist /absolute/PARENT" >&2 ;;
    status) echo "usage: scripts/release.sh status --environment stage|prod --tag TAG [--dist /absolute/PARENT]" >&2 ;;
    acceptance-template) echo "usage: scripts/release.sh acceptance-template --dist /absolute/STAGE_PARENT --output /absolute/FILE" >&2 ;;
    native-requirements) echo "usage: scripts/release.sh native-requirements --environment stage|prod --dist /absolute/PARENT [--review /absolute/FILE --baseline-dist /absolute/PARENT]" >&2 ;;
    native-review-template) echo "usage: scripts/release.sh native-review-template --environment stage|prod --dist /absolute/CURRENT --baseline-dist /absolute/FIRST_SUPPORT --output /absolute/FILE" >&2 ;;
    native-manual) echo "usage: scripts/release.sh native-manual --environment stage|prod --dist /absolute/PARENT --target OS/ARCH --check NAME --result pass|fail --operator NAME --procedure ID" >&2 ;;
    native-evidence) echo "usage: scripts/release.sh native-evidence --environment stage|prod --dist /absolute/PARENT" >&2 ;;
    *) echo "usage: scripts/release.sh tools|check|snapshot|build|verify|native-requirements|native-review-template|native-manual|native-evidence|publish|status|resume|acceptance-template" >&2 ;;
  esac
  exit 2
}

valid_hex() {
  value=$1
  width=$2
  [ "${#value}" -eq "$width" ] || return 1
  case "$value" in *[!0-9a-f]*) return 1 ;; esac
}

valid_version() {
  printf '%s\n' "$1" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'
}

valid_tool_version() {
  printf '%s\n' "$1" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'
}

valid_image() {
  image=$1
  printf '%s\n' "$image" | grep -Eq '^[a-z0-9][a-z0-9./:_-]*@sha256:[0-9a-f]{64}$' || return 1
  digest=${image##*@sha256:}
  valid_hex "$digest" 64
}

load_toolchain() {
  lock=$1
  GO_VERSION= GO_IMAGE= GO_ALPINE_IMAGE= RUNTIME_IMAGE= AIR_VERSION=
  GORELEASER_VERSION= GORELEASER_IMAGE= GH_VERSION=
  GH_SHA256_AMD64= GH_SHA256_ARM64= STATICCHECK_VERSION= GOVULNCHECK_VERSION=
  seen_GO_VERSION= seen_GO_IMAGE= seen_GO_ALPINE_IMAGE= seen_RUNTIME_IMAGE= seen_AIR_VERSION=
  seen_GORELEASER_VERSION= seen_GORELEASER_IMAGE= seen_GH_VERSION=
  seen_GH_SHA256_AMD64= seen_GH_SHA256_ARM64= seen_STATICCHECK_VERSION= seen_GOVULNCHECK_VERSION=
  [ -f "$lock" ] && [ ! -L "$lock" ] || die "toolchain lock is missing or unsafe"
  [ "$(wc -c <"$lock" | tr -d ' ')" -le 65536 ] || die "toolchain lock is too large"
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      *=*) key=${line%%=*}; value=${line#*=} ;;
      *) die "toolchain lock contains malformed data" ;;
    esac
    [ -n "$value" ] || die "toolchain lock contains an empty value"
    case "$value" in *[[:space:]]*) die "toolchain lock contains whitespace" ;; esac
    case "$key" in
      GO_VERSION) [ -z "$seen_GO_VERSION" ] || die "toolchain lock repeats GO_VERSION"; seen_GO_VERSION=1; GO_VERSION=$value ;;
      GO_IMAGE) [ -z "$seen_GO_IMAGE" ] || die "toolchain lock repeats GO_IMAGE"; seen_GO_IMAGE=1; GO_IMAGE=$value ;;
      GO_ALPINE_IMAGE) [ -z "$seen_GO_ALPINE_IMAGE" ] || die "toolchain lock repeats GO_ALPINE_IMAGE"; seen_GO_ALPINE_IMAGE=1; GO_ALPINE_IMAGE=$value ;;
      RUNTIME_IMAGE) [ -z "$seen_RUNTIME_IMAGE" ] || die "toolchain lock repeats RUNTIME_IMAGE"; seen_RUNTIME_IMAGE=1; RUNTIME_IMAGE=$value ;;
      AIR_VERSION) [ -z "$seen_AIR_VERSION" ] || die "toolchain lock repeats AIR_VERSION"; seen_AIR_VERSION=1; AIR_VERSION=$value ;;
      GORELEASER_VERSION) [ -z "$seen_GORELEASER_VERSION" ] || die "toolchain lock repeats GORELEASER_VERSION"; seen_GORELEASER_VERSION=1; GORELEASER_VERSION=$value ;;
      GORELEASER_IMAGE) [ -z "$seen_GORELEASER_IMAGE" ] || die "toolchain lock repeats GORELEASER_IMAGE"; seen_GORELEASER_IMAGE=1; GORELEASER_IMAGE=$value ;;
      GH_VERSION) [ -z "$seen_GH_VERSION" ] || die "toolchain lock repeats GH_VERSION"; seen_GH_VERSION=1; GH_VERSION=$value ;;
      GH_SHA256_AMD64) [ -z "$seen_GH_SHA256_AMD64" ] || die "toolchain lock repeats GH_SHA256_AMD64"; seen_GH_SHA256_AMD64=1; GH_SHA256_AMD64=$value ;;
      GH_SHA256_ARM64) [ -z "$seen_GH_SHA256_ARM64" ] || die "toolchain lock repeats GH_SHA256_ARM64"; seen_GH_SHA256_ARM64=1; GH_SHA256_ARM64=$value ;;
      STATICCHECK_VERSION) [ -z "$seen_STATICCHECK_VERSION" ] || die "toolchain lock repeats STATICCHECK_VERSION"; seen_STATICCHECK_VERSION=1; STATICCHECK_VERSION=$value ;;
      GOVULNCHECK_VERSION) [ -z "$seen_GOVULNCHECK_VERSION" ] || die "toolchain lock repeats GOVULNCHECK_VERSION"; seen_GOVULNCHECK_VERSION=1; GOVULNCHECK_VERSION=$value ;;
      *) die "toolchain lock contains an unknown key" ;;
    esac
  done <"$lock"
  for required in "$seen_GO_VERSION" "$seen_GO_IMAGE" "$seen_GO_ALPINE_IMAGE" "$seen_RUNTIME_IMAGE" "$seen_AIR_VERSION" "$seen_GORELEASER_VERSION" "$seen_GORELEASER_IMAGE" "$seen_GH_VERSION" "$seen_GH_SHA256_AMD64" "$seen_GH_SHA256_ARM64" "$seen_STATICCHECK_VERSION" "$seen_GOVULNCHECK_VERSION"; do
    [ -n "$required" ] || die "toolchain lock is incomplete"
  done
  valid_version "$GO_VERSION" || die "toolchain lock has an invalid Go version"
  valid_version "$GH_VERSION" || die "toolchain lock has an invalid GitHub CLI version"
  valid_tool_version "$AIR_VERSION" || die "toolchain lock has an invalid Air version"
  valid_tool_version "$GORELEASER_VERSION" || die "toolchain lock has an invalid GoReleaser version"
  valid_tool_version "$STATICCHECK_VERSION" || die "toolchain lock has an invalid staticcheck version"
  valid_tool_version "$GOVULNCHECK_VERSION" || die "toolchain lock has an invalid govulncheck version"
  valid_image "$GO_IMAGE" || die "toolchain lock has an invalid Go image"
  valid_image "$GO_ALPINE_IMAGE" || die "toolchain lock has an invalid Alpine Go image"
  valid_image "$RUNTIME_IMAGE" || die "toolchain lock has an invalid runtime image"
  valid_image "$GORELEASER_IMAGE" || die "toolchain lock has an invalid GoReleaser image"
  valid_hex "$GH_SHA256_AMD64" 64 || die "toolchain lock has an invalid amd64 GitHub CLI hash"
  valid_hex "$GH_SHA256_ARM64" 64 || die "toolchain lock has an invalid arm64 GitHub CLI hash"
  RELEASE_IMAGE="hookspot-release:go${GO_VERSION}-gr${GORELEASER_VERSION#v}-gh${GH_VERSION}"
}

toolchain_value() {
  [ "$#" -eq 1 ] || usage
  load_toolchain "$REPOSITORY_ROOT/release/toolchain.env"
  case "$1" in
    GO_VERSION) echo "$GO_VERSION" ;;
    GO_IMAGE) echo "$GO_IMAGE" ;;
    AIR_VERSION) echo "$AIR_VERSION" ;;
    STATICCHECK_VERSION) echo "$STATICCHECK_VERSION" ;;
    GOVULNCHECK_VERSION) echo "$GOVULNCHECK_VERSION" ;;
    *) die "unsupported toolchain value" ;;
  esac
}

require_environment() {
  case "$1" in stage|prod) ;; *) die "--environment must be stage or prod" ;; esac
}

safe_repository_view() {
  cd "$REPOSITORY_ROOT"
  [ "$(git rev-parse --is-inside-work-tree 2>/dev/null)" = true ] || die "run this command from a Git worktree"
  [ "$(GIT_NO_REPLACE_OBJECTS=1 git rev-parse --is-shallow-repository 2>/dev/null)" = false ] || die "release source must be a full, non-shallow checkout"
  replacement=$(git for-each-ref --format='%(refname)' refs/replace 2>/dev/null) || die "cannot inspect Git replacement refs"
  [ -z "$replacement" ] || die "remove Git replacement refs before selecting release source"
  set +e
  partial=$(git config --get-regexp '^(extensions\.partialclone|remote\..*\.(promisor|partialclonefilter))$' 2>/dev/null)
  config_status=$?
  set -e
  [ "$config_status" -eq 0 ] || [ "$config_status" -eq 1 ] || die "cannot inspect effective Git configuration"
  [ -z "$partial" ] || die "release source must not be a partial or promisor checkout"
  common=$(GIT_NO_REPLACE_OBJECTS=1 git rev-parse --path-format=absolute --git-common-dir 2>/dev/null) || die "cannot resolve Git metadata"
  if [ -s "$common/info/grafts" ]; then
    die "remove legacy Git grafts before selecting release source"
  fi
}

require_clean_tree() {
  cd "$REPOSITORY_ROOT"
  tree_status=$(publication_git status --porcelain --untracked-files=normal) || die "cannot inspect worktree cleanliness"
  [ -z "$tree_status" ] || die "working tree has uncommitted changes; commit or stash staged, unstaged, and untracked changes, then retry"
}

valid_ref_input() {
  [ -n "$1" ] && [ "${#1}" -le 256 ] || return 1
  case "$1" in -*|*$'\n'*|*$'\r'*) return 1 ;; esac
}

resolve_commit() {
  ref=$1
  valid_ref_input "$ref" || die "source ref is invalid"
  commit=$(GIT_NO_REPLACE_OBJECTS=1 git rev-parse --verify --end-of-options "$ref^{commit}" 2>/dev/null) || die "source ref does not identify a commit"
  valid_hex "$commit" 40 || die "source commit identity is unsupported"
  echo "$commit"
}

validate_tag() {
  environment=$1
  tag=$2
  validate_tag_name "$environment" "$tag"
  tag_object=$(publication_git rev-parse --verify --end-of-options "refs/tags/$tag^{object}" 2>/dev/null) || die "release tag must already exist locally"
  tag_commit=$(publication_git rev-parse --verify --end-of-options "refs/tags/$tag^{commit}" 2>/dev/null) || die "release tag does not identify a commit"
  valid_hex "$tag_object" 40 && valid_hex "$tag_commit" 40 || die "release tag identity is unsupported"
}

validate_tag_name() {
  environment=$1
  tag=$2
  valid_ref_input "$tag" || die "release tag is invalid"
  case "$tag" in */*|*' '*|*~*|*^*|*:*|*'?'*|*'['*|*'\\'*) die "release tag is invalid" ;; esac
  if [ "$environment" = stage ]; then
    echo "$tag" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-stage\.[1-9][0-9]*$' || die "stage tag must use vMAJOR.MINOR.PATCH-stage.N"
  else
    echo "$tag" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || die "prod tag must use vMAJOR.MINOR.PATCH"
  fi
}

prepare_source() {
  selected_commit=$1
  selected_tag_object=${2:-}
  SOURCE_TEMP=$(mktemp -d "${TMPDIR:-/tmp}/hookspot-release-source.XXXXXX") || die "cannot allocate disposable source"
  SOURCE_TEMP=$(cd "$SOURCE_TEMP" && pwd -P) || die "cannot resolve disposable source workspace"
  source_checkout="$SOURCE_TEMP/source"
  mkdir "$SOURCE_TEMP/home"
  isolated_git clone --quiet --no-local --no-checkout "$REPOSITORY_ROOT" "$source_checkout" || die "cannot create independent source copy"
  alternates=$(isolated_git -C "$source_checkout" rev-parse --path-format=absolute --git-path objects/info/alternates 2>/dev/null) || die "cannot inspect disposable source"
  [ ! -s "$alternates" ] || die "disposable source unexpectedly uses shared objects"
  copied_commit=$(isolated_git -C "$source_checkout" rev-parse --verify "$selected_commit^{commit}" 2>/dev/null) || die "selected commit is missing from disposable source"
  [ "$copied_commit" = "$selected_commit" ] || die "selected commit changed while copying source"
  if [ -n "$selected_tag_object" ]; then
    copied_tag=$(isolated_git -C "$source_checkout" rev-parse --verify "refs/tags/$tag^{object}" 2>/dev/null) || die "selected tag is missing from disposable source"
    [ "$copied_tag" = "$selected_tag_object" ] || die "selected tag changed while copying source"
  fi
  isolated_git -C "$source_checkout" remote remove origin
  isolated_git -c core.hooksPath=/dev/null -C "$source_checkout" checkout --quiet --detach "$selected_commit" || die "cannot check out selected source"
  [ "$(isolated_git -C "$source_checkout" rev-parse HEAD)" = "$selected_commit" ] || die "disposable checkout has the wrong commit"
  [ -f "$source_checkout/scripts/release.sh" ] || die "selected source predates the supported release interface"
  grep -q '^RELEASE_INTERFACE_VERSION=8$' "$source_checkout/scripts/release.sh" || die "selected source has an incompatible release interface"
  [ -f "$source_checkout/tools/releasebootstrap/main.go" ] && [ ! -L "$source_checkout/tools/releasebootstrap/main.go" ] || \
    die "selected source lacks the current release helper checker; select current release tooling or use that source's matching historical workflow"
  load_toolchain "$source_checkout/release/toolchain.env"
  CONTROL_ROOT=$source_checkout
}

isolated_git() {
  env -i PATH="$PATH" HOME="$SOURCE_TEMP/home" GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_COUNT=0 GIT_NO_REPLACE_OBJECTS=1 git "$@"
}

build_tools() {
  load_toolchain "$REPOSITORY_ROOT/release/toolchain.env"
  CONTROL_ROOT=$REPOSITORY_ROOT
  lock_hash=$(hash_control_file "$GO_IMAGE" "$CONTROL_ROOT/release/toolchain.env")
  recipe_hash=$(hash_control_file "$GO_IMAGE" "$CONTROL_ROOT/Dockerfile.release")
  bootstrap_hash=$(hash_control_file "$GO_IMAGE" "$CONTROL_ROOT/tools/releasebootstrap/main.go")
  docker buildx build --load --file "$REPOSITORY_ROOT/Dockerfile.release" --tag "$RELEASE_IMAGE" \
    --label "org.hookspot.release.lock-sha256=$lock_hash" \
    --label "org.hookspot.release.recipe-sha256=$recipe_hash" \
    --label "org.hookspot.release.bootstrap-sha256=$bootstrap_hash" \
    --build-arg "GO_IMAGE=$GO_IMAGE" --build-arg "GORELEASER_IMAGE=$GORELEASER_IMAGE" \
    --build-arg "GH_VERSION=$GH_VERSION" --build-arg "GH_SHA256_AMD64=$GH_SHA256_AMD64" \
    --build-arg "GH_SHA256_ARM64=$GH_SHA256_ARM64" \
    --build-arg HTTP_PROXY= --build-arg http_proxy= --build-arg HTTPS_PROXY= --build-arg https_proxy= \
    --build-arg NO_PROXY= --build-arg no_proxy= --build-arg ALL_PROXY= --build-arg all_proxy= \
    --build-arg FTP_PROXY= --build-arg ftp_proxy= "$REPOSITORY_ROOT"
  inspect_release_image
  echo "release tools are ready"
}

inspect_release_image() {
  details=$(docker image inspect "$RELEASE_IMAGE" --format '{{.Id}} {{.Os}}/{{.Architecture}}' 2>/dev/null) || die "release tools are missing; run scripts/release.sh tools"
  set -- $details
  [ "$#" -eq 2 ] || die "release tool image identity is invalid"
  IMAGE_ID=$1
  IMAGE_PLATFORM=$2
  case "$IMAGE_ID" in sha256:*) valid_hex "${IMAGE_ID#sha256:}" 64 || die "release tool image ID is invalid" ;; *) die "release tool image ID is invalid" ;; esac
  case "$IMAGE_PLATFORM" in linux/amd64|linux/arm64) ;; *) die "release tool image platform is unsupported" ;; esac
  daemon_platform=$(docker version --format '{{.Server.Os}}/{{.Server.Arch}}' 2>/dev/null) || die "cannot inspect the Docker daemon platform"
  case "$daemon_platform" in linux/aarch64) daemon_platform=linux/arm64 ;; linux/x86_64) daemon_platform=linux/amd64 ;; esac
  [ "$daemon_platform" = "$IMAGE_PLATFORM" ] || die "release tool image does not match the native Docker daemon platform"
  lock_label=$(docker image inspect "$IMAGE_ID" --format '{{index .Config.Labels "org.hookspot.release.lock-sha256"}}' 2>/dev/null) || die "release tool image lock identity is missing"
  recipe_label=$(docker image inspect "$IMAGE_ID" --format '{{index .Config.Labels "org.hookspot.release.recipe-sha256"}}' 2>/dev/null) || die "release tool image recipe identity is missing"
  bootstrap_label=$(docker image inspect "$IMAGE_ID" --format '{{index .Config.Labels "org.hookspot.release.bootstrap-sha256"}}' 2>/dev/null) || die "release tool image helper checker identity is missing"
  [ "$lock_label" = "$(hash_control_file "$IMAGE_ID" "$CONTROL_ROOT/release/toolchain.env")" ] || die "release tool image was built from a different toolchain lock; rerun tools"
  [ "$recipe_label" = "$(hash_control_file "$IMAGE_ID" "$CONTROL_ROOT/Dockerfile.release")" ] || die "release tool image was built from a different Docker recipe; rerun tools"
  [ "$bootstrap_label" = "$(hash_control_file "$IMAGE_ID" "$CONTROL_ROOT/tools/releasebootstrap/main.go")" ] || die "release tool image was built from a different helper checker; rerun tools"
  identity=$(container_run --rm --network none --entrypoint sh "$IMAGE_ID" -c 'set -e; go version; goreleaser --version; gh --version' 2>/dev/null) || die "release tool image cannot run"
  printf '%s\n' "$identity" | grep -Fxq "go version go${GO_VERSION} ${IMAGE_PLATFORM}" || die "release tool image has the wrong Go version"
  goreleaser_identity=$(printf '%s\n' "$identity" | sed -n -E 's/^(GitVersion:|goreleaser version)[[:space:]]+([^[:space:]]+)$/\2/p')
  [ "$goreleaser_identity" = "${GORELEASER_VERSION#v}" ] || die "release tool image has the wrong GoReleaser version"
  printf '%s\n' "$identity" | grep -Eq "^gh version ${GH_VERSION//./\\.}( |$)" || die "release tool image has the wrong GitHub CLI version"
}

hash_control_file() {
  hash_image=$1
  hash_path=$2
  [ -f "$hash_path" ] && [ ! -L "$hash_path" ] || die "release control file is missing or unsafe"
  hash_output=$(container_run --rm --network none -v "$hash_path:/input:ro" --entrypoint sha256sum "$hash_image" /input 2>/dev/null) || die "cannot hash release controls in the pinned container"
  hash_value=${hash_output%% *}
  valid_hex "$hash_value" 64 || die "container returned an invalid control digest"
  echo "$hash_value"
}

container_run() {
  docker run -e HTTP_PROXY= -e http_proxy= -e HTTPS_PROXY= -e https_proxy= \
    -e NO_PROXY= -e no_proxy= -e ALL_PROXY= -e all_proxy= -e FTP_PROXY= -e ftp_proxy= "$@"
}

resolve_server_url() {
  source_checkout=$1
  environment=$2
  server_url=$(container_run --rm --entrypoint sh \
    -v "$source_checkout:/src:ro" -w /src \
    -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build \
    "$IMAGE_ID" -c 'go run ./tools/releasecheck env-url --environment "$1"' sh "$environment" 2>"$SOURCE_TEMP/environment.log") || environment_failure
  [ -n "$server_url" ] || die "selected environment URL is empty"
  case "$server_url" in *[[:space:]]*) die "selected environment URL is invalid" ;; esac
  if [ -n "${SERVER_URL-}" ]; then
    if ! RELEASE_ASSERTED_SERVER_URL=$SERVER_URL container_run --rm --entrypoint sh \
      -v "$source_checkout:/src:ro" -w /src \
      -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build \
      -e RELEASE_ASSERTED_SERVER_URL "$IMAGE_ID" \
      -c 'go run ./tools/releasecheck env --environment "$1" --server-url "$RELEASE_ASSERTED_SERVER_URL"' sh "$environment" \
      >>"$SOURCE_TEMP/environment.log" 2>&1; then
      environment_failure
    fi
  fi
  source_repository=$(container_run --rm --entrypoint sh \
    -v "$source_checkout:/src:ro" -w /src \
    -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build \
    "$IMAGE_ID" -c 'go run ./tools/releasecheck repository' 2>>"$SOURCE_TEMP/environment.log") || environment_failure
  printf '%s\n' "$source_repository" | grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$' || die "selected release repository identity is invalid"
  repository_owner=${source_repository%%/*}
  repository_name=${source_repository#*/}
  case "$repository_owner/$repository_name" in './'*|'../'*|*'/.'|*'/..') die "selected release repository identity is invalid" ;; esac
  isolated_git -C "$source_checkout" remote add origin "https://github.com/$source_repository.git" || die "cannot set the disposable source origin"
}

environment_failure() {
  diagnostic=$(retain_diagnostic "$SOURCE_TEMP/environment.log" environment)
  die "selected environment configuration is incomplete or invalid; diagnostic retained at $diagnostic"
}

retain_diagnostic() {
  source_log=$1
  label=$2
  diagnostic_root="$REPOSITORY_ROOT/dist/diagnostics"
  validate_path_components "$diagnostic_root"
  mkdir -p "$diagnostic_root"
  validate_path_components "$diagnostic_root"
  diagnostic=$(mktemp "$diagnostic_root/${label}.XXXXXX") || die "cannot allocate release diagnostic"
  cp "$source_log" "$diagnostic" || die "cannot retain release diagnostic"
  echo "$diagnostic"
}

run_check() {
  source_checkout=$1
  environment=$2
  resolve_server_url "$source_checkout" "$environment"
  if ! container_run --rm --entrypoint sh \
    -v "$source_checkout:/src:ro" -w /src \
    -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build \
    -e "RELEASE_ENV=$environment" -e "SERVER_URL=$server_url" \
    "$IMAGE_ID" -c 'go run ./tools/releasecheck env --environment "$RELEASE_ENV" --server-url "$SERVER_URL" && goreleaser check .goreleaser.yaml' \
    >"$SOURCE_TEMP/check.log" 2>&1; then
    diagnostic=$(retain_diagnostic "$SOURCE_TEMP/check.log" check)
    die "release checks failed; diagnostic retained at $diagnostic"
  fi
  echo "release configuration is valid for $environment"
}

validate_path_components() {
  path=$1
  case "$path" in /*) ;; *) die "output path must be absolute" ;; esac
  old_ifs=$IFS
  IFS=/
  set -- ${path#/}
  IFS=$old_ifs
  current=
  for component in "$@"; do
    case "$component" in ''|.) continue ;; ..) die "output path must not contain parent traversal" ;; esac
    current="$current/$component"
    if [ -e "$current" ] || [ -L "$current" ]; then
      [ -d "$current" ] && [ ! -L "$current" ] || die "output path has an unsafe existing component"
    fi
  done
}

path_is_within() {
  local parent=$1 child=$2
  [ "$parent" != / ] || return 0
  [ "$child" = "$parent" ] && return 0
  case "$child" in "$parent"/*) return 0 ;; esac
  return 1
}

paths_overlap() {
  path_is_within "$1" "$2" || path_is_within "$2" "$1"
}

allocate_output() {
  environment=$1
  label=$2
  short_commit=$(printf '%s' "$selected_commit" | cut -c1-7)
  base="$REPOSITORY_ROOT/dist/$environment/$label/$short_commit"
  validate_path_components "$base"
  mkdir -p "$base" || die "cannot create release output directory"
  validate_path_components "$base"
  run_id="$(date -u '+%Y%m%dT%H%M%SZ')-$$"
  output_parent="$base/$run_id"
  mkdir "$output_parent" || die "cannot allocate an exclusive release output parent"
  output_parent=$(cd "$output_parent" && pwd -P)
}

run_build() {
  mode=$1
  environment=$2
  build_tag=$3
  resolve_server_url "$source_checkout" "$environment"
  label=snapshot
  current_tag=v0.0.0
  if [ "$mode" = release ]; then
    label=$build_tag
    current_tag=$build_tag
  fi
  allocate_output "$environment" "$label"
  invocation_started=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
  if ! container_run --rm --entrypoint sh \
    -v "$source_checkout:/src" -w /src -v "$output_parent:/out" \
    -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build \
    -e "RELEASE_ENV=$environment" -e "SERVER_URL=$server_url" \
    -e "GORELEASER_CURRENT_TAG=$current_tag" -e "RELEASE_BUILD_MODE=$mode" \
    -e "RELEASE_TAG=$build_tag" -e "RELEASE_TAG_OBJECT=${tag_object:-}" \
    -e "RELEASE_BUILDER_IMAGE_ID=$IMAGE_ID" -e "RELEASE_BUILDER_PLATFORM=$IMAGE_PLATFORM" \
    -e "RELEASE_INVOCATION_STARTED_AT=$invocation_started" \
    "$IMAGE_ID" -c '
      set -eu
      umask 022
      mkdir /out/tools
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /out/tools/releasecheck-linux-amd64 ./tools/releasecheck
      CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o /out/tools/releasecheck-linux-arm64 ./tools/releasecheck
      go run ./tools/releasecheck env --environment "$RELEASE_ENV" --server-url "$SERVER_URL"
      goreleaser check .goreleaser.yaml
      if [ "$RELEASE_BUILD_MODE" = snapshot ]; then
        goreleaser release --config .goreleaser.yaml --snapshot --skip=publish
        go run ./tools/releasecheck artifacts --environment "$RELEASE_ENV" --dist /out --snapshot
      else
        goreleaser release --config .goreleaser.yaml --skip=publish
        go run ./tools/releasecheck artifacts --environment "$RELEASE_ENV" --dist /out --tag "$RELEASE_TAG"
      fi
    ' >"$output_parent/build.log" 2>&1; then
    die "release build failed; diagnostic log retained at $output_parent/build.log"
  fi
  echo "release output: $output_parent"
}

verify_output() {
  environment=$1
  dist=$2
  case "$dist" in /*) ;; *) die "--dist must be an absolute retained output parent" ;; esac
  validate_path_components "$dist"
  [ -d "$dist" ] && [ ! -L "$dist" ] || die "retained output parent is missing or unsafe"
  load_toolchain "$REPOSITORY_ROOT/release/toolchain.env"
  CONTROL_ROOT=$REPOSITORY_ROOT
  inspect_release_image
  current_lock_hash=$(hash_control_file "$IMAGE_ID" "$REPOSITORY_ROOT/release/toolchain.env")
  retained_lock_hash=$(hash_control_file "$IMAGE_ID" "$dist/controls/release/toolchain.env")
  [ "$retained_lock_hash" = "$current_lock_hash" ] || die "retained output uses an unsupported toolchain lock"
  helper_arch=${IMAGE_PLATFORM#linux/}
  if ! container_run --rm --network none --read-only \
    -v "$dist:/out:ro" --entrypoint release-helper-check \
    "$IMAGE_ID" --receipt /out/receipt.json --tools /out/tools --arch "$helper_arch"; then
    die "retained release helper failed its trusted digest check"
  fi
  if ! container_run --rm --network none --read-only \
    -v "$dist:/out:ro" --entrypoint "/out/tools/releasecheck-linux-$helper_arch" \
    "$IMAGE_ID" receipt verify --environment "$environment" --dist /out; then
    die "retained release verification failed"
  fi
  echo "release output is intact for $environment"
}

publication_git() {
  env -u GITHUB_TOKEN -u GH_TOKEN GIT_TERMINAL_PROMPT=0 GIT_NO_REPLACE_OBJECTS=1 \
    git -c core.hooksPath=/dev/null -c core.fsmonitor=false -c push.followTags=false \
    -c remote.origin.mirror=false -c remote.origin.prune=false -c fetch.prune=false "$@"
}

publication_git_config() {
  env -u GITHUB_TOKEN -u GH_TOKEN GIT_TERMINAL_PROMPT=0 GIT_NO_REPLACE_OBJECTS=1 \
    git -c core.hooksPath=/dev/null -c core.fsmonitor=false config "$@"
}

acquire_publication_lock() {
  common=$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null) || die "cannot resolve publication lock directory"
  PUBLICATION_LOCK="$common/hookspot-publication.lock"
  umask 077
  if ! mkdir "$PUBLICATION_LOCK" 2>/dev/null; then
    PUBLICATION_LOCK=
    die "another release coordinator owns the local publication lock; inspect it before retrying"
  fi
  PUBLICATION_LOCK_OWNER="pid=$$ started=$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  printf '%s\n' "$PUBLICATION_LOCK_OWNER" >"$PUBLICATION_LOCK/owner"
}

canonical_repository_url() {
  url=$1
  expected=$2
  case "$url" in
    "https://github.com/$expected"|"https://github.com/$expected.git"|"git@github.com:$expected"|"git@github.com:$expected.git"|"ssh://git@github.com/$expected"|"ssh://git@github.com/$expected.git") ;;
    *) return 1 ;;
  esac
}

validate_remote_identity() {
  repository=$1
  set +e
  mirror=$(publication_git_config --bool --get remote.origin.mirror 2>/dev/null)
  mirror_status=$?
  set -e
  [ "$mirror_status" -eq 0 ] || [ "$mirror_status" -eq 1 ] || die "cannot inspect origin mirror configuration"
  [ "$mirror" != true ] || die "origin must not be a mirror for publication"
  fetch_urls=$(publication_git remote get-url --all origin) || die "cannot resolve origin fetch URL"
  push_urls=$(publication_git remote get-url --push --all origin) || die "cannot resolve origin push URL"
  [ "$(printf '%s\n' "$fetch_urls" | wc -l | tr -d ' ')" -eq 1 ] || die "origin must have exactly one effective fetch URL"
  [ "$(printf '%s\n' "$push_urls" | wc -l | tr -d ' ')" -eq 1 ] || die "origin must have exactly one effective push URL"
  canonical_repository_url "$fetch_urls" "$repository" || die "origin fetch URL does not match the release repository"
  canonical_repository_url "$push_urls" "$repository" || die "origin push URL does not match the release repository"
}

publication_helper() {
  helper_arch=${IMAGE_PLATFORM#linux/}
  if ! container_run --rm --network none --read-only -v "$dist:/out:ro" \
    --entrypoint release-helper-check "$IMAGE_ID" --receipt /out/receipt.json --tools /out/tools --arch "$helper_arch" >/dev/null; then
    die "retained release helper failed its trusted digest check"
  fi
  publication_out_mount="$dist:/out"
  [ "${PUBLICATION_HELPER_READ_ONLY-}" = 1 ] && publication_out_mount="$dist:/out:ro"
  container_run --rm --network none -v "$publication_out_mount" -v "$PUBLICATION_TEMP:/state" \
    --entrypoint "/out/tools/releasecheck-linux-$helper_arch" "$IMAGE_ID" "$@"
}

PUBLICATION_HELPER_READ_ONLY=

read_publication_identity() {
  identity_file="$PUBLICATION_TEMP/identity"
  publication_helper publication describe --environment "$environment" --dist /out >"$identity_file" || die "retained publication state is invalid"
  repository= tag_read= tag_object= tag_commit= phase= release_id=
  stage_tag= stage_tag_object= stage_tag_commit=
  assets=
  while IFS=$'\t' read -r field value extra; do
    [ -z "$extra" ] || die "publication helper returned malformed identity"
    case "$field" in
      repository) repository=$value ;;
      tag) tag_read=$value ;;
      tag_object) tag_object=$value ;;
      tag_commit) tag_commit=$value ;;
      phase) phase=$value ;;
      release_id) release_id=$value ;;
      marker) marker=$value ;;
      asset) assets="${assets}${assets:+
}$value" ;;
      stage_tag) stage_tag=$value ;;
      stage_tag_object) stage_tag_object=$value ;;
      stage_tag_commit) stage_tag_commit=$value ;;
      *) die "publication helper returned an unknown identity field" ;;
    esac
  done <"$identity_file"
  [ "$tag_read" = "$tag" ] || die "requested tag does not match retained publication"
  [ -n "$repository" ] && [ -n "$tag_object" ] && [ -n "$tag_commit" ] || die "retained publication identity is incomplete"
}

read_candidate_identity() {
  candidate_file="$PUBLICATION_TEMP/candidate"
  publication_helper publication candidate --environment "$environment" --dist /out >"$candidate_file" || die "retained publication candidate is invalid"
  repository= tag_read= tag_object= tag_commit=
  while IFS=$'\t' read -r field value extra; do
    [ -z "$extra" ] || die "publication helper returned malformed candidate identity"
    case "$field" in
      repository) repository=$value ;;
      tag) tag_read=$value ;;
      tag_object) tag_object=$value ;;
      tag_commit) tag_commit=$value ;;
      *) die "publication helper returned an unknown candidate field" ;;
    esac
  done <"$candidate_file"
  [ "$tag_read" = "$tag" ] || die "requested tag does not match retained release"
  [ -n "$repository" ] && [ -n "$tag_object" ] && [ -n "$tag_commit" ] || die "retained candidate identity is incomplete"
}

validate_explicit_publication_inputs() {
  [ "$resume_mode" = false ] || return 0
  cmp -s "$notes" "$dist/release-notes.md" || die "reviewed release notes conflict with the retained publication"
  if [ "${ref_supplied-false}" = true ]; then
    explicit_commit=$(publication_git rev-parse --verify --end-of-options "$ref^{commit}" 2>/dev/null) || die "source ref does not identify a commit"
    [ "$explicit_commit" = "$tag_commit" ] || die "source ref conflicts with the retained publication"
  fi
  if [ "$environment" = prod ]; then
    [ "$from_stage_tag" = "$stage_tag" ] || die "stage tag conflicts with the retained publication"
    cmp -s "$stage_acceptance" "$dist/stage-acceptance.json" || die "stage acceptance conflicts with the retained publication"
  fi
}

enforce_new_source_policy() {
  validate_remote_identity "$repository"
  branch=stage
  [ "$environment" = prod ] && branch=main
  publication_git fetch --quiet --no-tags origin "+refs/heads/$branch:refs/remotes/origin/$branch" || die "cannot fetch the release branch"
  if [ "$environment" = stage ]; then
    [ "$(publication_git rev-parse "refs/remotes/origin/$branch")" = "$tag_commit" ] || die "new stage publication must use the fetched stage tip"
  else
    publication_git merge-base --is-ancestor "$tag_commit" "refs/remotes/origin/$branch" || die "new production publication must be reachable from fetched main"
  fi
}

publisher_gh() {
  set -- --rm -e GITHUB_TOKEN -v "$PUBLICATION_TEMP:/state" -w /state
  if [ -n "${dist-}" ] && [ -f "$dist/release-body.md" ]; then
    set -- "$@" -v "$dist/release-body.md:/public/release-body.md:ro"
  fi
  while IFS= read -r asset_name; do
    [ -n "$asset_name" ] || continue
    set -- "$@" -v "$dist/artifacts/$asset_name:/assets/$asset_name:ro"
  done <<EOF
$assets
EOF
  set -- "$@" --entrypoint gh "$IMAGE_ID" "${publisher_gh_args[@]}"
  container_run "$@"
}

run_gh() {
  publisher_gh_args=("$@")
  publisher_gh
}

allocate_publication_temp() {
  [ -z "$PUBLICATION_TEMP" ] || return 0
  PUBLICATION_TEMP=$(mktemp -d "${TMPDIR:-/tmp}/hookspot-publication.XXXXXX") || die "cannot allocate publication workspace"
  PUBLICATION_TEMP=$(cd "$PUBLICATION_TEMP" && pwd -P) || die "cannot resolve publication workspace"
  chmod 700 "$PUBLICATION_TEMP"
}

check_publisher_access() {
  allocate_publication_temp
  run_gh api --method GET "repos/$repository" >"$PUBLICATION_TEMP/repository.json" || \
    die "publisher cannot read the selected GitHub repository; no mutation was attempted"
}

refresh_remote_state() {
  fetch_remote_state
  publication_helper publication remote-plan --environment "$environment" --dist /out \
    --remote-json /state/releases.json >"$PUBLICATION_TEMP/plan" || die "remote release conflicts with retained publication"
}

require_status_tag_identity() {
  status_remote_lines=$(publication_git ls-remote origin "refs/tags/$tag" "refs/tags/$tag^{}") || \
    die "cannot read remote tag identity for status"
  status_remote_object=$(printf '%s\n' "$status_remote_lines" | awk -v ref="refs/tags/$tag" '$2==ref {print $1}')
  status_remote_commit=$(printf '%s\n' "$status_remote_lines" | awk -v ref="refs/tags/$tag^{}" '$2==ref {print $1}')
  if [ -z "$status_remote_lines" ]; then
    [ "$plan_status" = absent ] || die "remote release exists without the retained tag identity"
    printf 'tag\tabsent\n' >>"$PUBLICATION_TEMP/plan"
    return 0
  fi
  [ -n "$status_remote_commit" ] || status_remote_commit=$status_remote_object
  [ "$status_remote_object" = "$tag_object" ] && [ "$status_remote_commit" = "$tag_commit" ] || \
    die "remote tag identity conflicts with retained publication"
  printf 'tag\texact\n' >>"$PUBLICATION_TEMP/plan"
}

fetch_remote_state() {
  releases_temporary=$(mktemp "$PUBLICATION_TEMP/releases.XXXXXX") || die "cannot allocate GitHub release response"
  if ! run_gh api --method GET --paginate --slurp "repos/$repository/releases?per_page=100" >"$releases_temporary"; then
    rm -f "$releases_temporary"
    die "cannot read complete GitHub release state; no absence or mutation is inferred"
  fi
  mv "$releases_temporary" "$PUBLICATION_TEMP/releases.json"
}

download_remote_assets() {
  rm -rf "$PUBLICATION_TEMP/downloads"
  mkdir -m 700 "$PUBLICATION_TEMP/downloads"
  while IFS=$'\t' read -r field asset_id asset_name extra; do
    [ "$field" = remote_asset ] || continue
    [ -z "$extra" ] || die "remote asset plan is malformed"
    case "$asset_id" in ''|*[!0-9]*) die "remote asset ID is invalid" ;; esac
    run_gh api --method GET -H 'Accept: application/octet-stream' "repos/$repository/releases/assets/$asset_id" >"$PUBLICATION_TEMP/downloads/$asset_name" || \
      die "cannot download an existing release asset for verification"
  done <"$PUBLICATION_TEMP/plan"
}

reconcile_remote() {
  allow_missing=$1
  download_remote_assets
  set -- publication reconcile --environment "$environment" --dist /out \
    --remote-json /state/releases.json --downloads /state/downloads
  [ "$allow_missing" = true ] && set -- "$@" --allow-missing
  publication_helper "$@" >"$PUBLICATION_TEMP/reconciled" || die "remote release bytes conflict with retained publication"
}

recover_lost_publication() {
  read_candidate_identity
  assets=
  validate_remote_identity "$repository"
  check_publisher_access
  fetch_remote_state
  publication_helper publication recover-plan --environment "$environment" --dist /out \
    --publisher-image-id "$IMAGE_ID" --publisher-platform "$IMAGE_PLATFORM" \
    --remote-json /state/releases.json >"$PUBLICATION_TEMP/plan" || \
    die "lost publication state does not match an exact owned remote release"
  require_exact_remote_tag
  download_remote_assets
  publication_helper publication recover --environment "$environment" --dist /out \
    --publisher-image-id "$IMAGE_ID" --publisher-platform "$IMAGE_PLATFORM" \
    --remote-json /state/releases.json --downloads /state/downloads || \
    die "lost publication state could not be restored from exact retained and remote bytes"
}

verify_remote_stage_acceptance() {
  [ "$environment" = prod ] || return 0
  require_exact_named_remote_tag "$stage_tag" "$stage_tag_object" "$stage_tag_commit" "accepted stage"
  cp "$PUBLICATION_TEMP/plan" "$PUBLICATION_TEMP/publication-plan"
  publication_helper publication acceptance-plan --dist /out --remote-json /state/releases.json >"$PUBLICATION_TEMP/plan" || \
    die "accepted completed stage release no longer matches remote state"
  download_remote_assets
  mv "$PUBLICATION_TEMP/downloads" "$PUBLICATION_TEMP/stage-downloads"
  publication_helper publication acceptance-verify --dist /out --remote-json /state/releases.json \
    --downloads /state/stage-downloads || die "accepted stage assets do not match downloaded remote bytes"
  mv "$PUBLICATION_TEMP/publication-plan" "$PUBLICATION_TEMP/plan"
}

require_exact_named_remote_tag() {
  checked_tag=$1
  checked_object=$2
  checked_commit=$3
  label=$4
  remote_lines=$(publication_git ls-remote origin "refs/tags/$checked_tag" "refs/tags/$checked_tag^{}") || die "cannot read $label tag identity"
  remote_object=$(printf '%s\n' "$remote_lines" | awk -v ref="refs/tags/$checked_tag" '$2==ref {print $1}')
  remote_commit=$(printf '%s\n' "$remote_lines" | awk -v ref="refs/tags/$checked_tag^{}" '$2==ref {print $1}')
  if [ -z "$remote_commit" ]; then remote_commit=$remote_object; fi
  [ "$remote_object" = "$checked_object" ] && [ "$remote_commit" = "$checked_commit" ] || die "$label tag identity conflicts with retained publication"
}

push_exact_tag() {
  validate_remote_identity "$repository"
  remote_lines=$(publication_git ls-remote origin "refs/tags/$tag" "refs/tags/$tag^{}") || die "cannot read remote tag identity"
  if [ -n "$remote_lines" ]; then
    remote_object=$(printf '%s\n' "$remote_lines" | awk -v ref="refs/tags/$tag" '$2==ref {print $1}')
    remote_commit=$(printf '%s\n' "$remote_lines" | awk -v ref="refs/tags/$tag^{}" '$2==ref {print $1}')
    if [ "$remote_object" = "$remote_commit" ] || [ -z "$remote_commit" ]; then remote_commit=$remote_object; fi
    [ "$remote_object" = "$tag_object" ] && [ "$remote_commit" = "$tag_commit" ] || die "remote tag identity conflicts with retained publication"
    return
  fi
  validate_remote_identity "$repository"
  publication_git push --porcelain origin "$tag_object:refs/tags/$tag" >/dev/null || die "exact tag push failed; retain the bundle and resume after reconciling remote state"
}

require_exact_remote_tag() {
  validate_remote_identity "$repository"
  remote_lines=$(publication_git ls-remote origin "refs/tags/$tag" "refs/tags/$tag^{}") || die "cannot read remote tag identity"
  [ -n "$remote_lines" ] || die "remote release exists without the retained tag identity"
  remote_object=$(printf '%s\n' "$remote_lines" | awk -v ref="refs/tags/$tag" '$2==ref {print $1}')
  remote_commit=$(printf '%s\n' "$remote_lines" | awk -v ref="refs/tags/$tag^{}" '$2==ref {print $1}')
  if [ -z "$remote_commit" ]; then remote_commit=$remote_object; fi
  [ "$remote_object" = "$tag_object" ] && [ "$remote_commit" = "$tag_commit" ] || die "remote tag identity conflicts with retained publication"
}

prepare_local_publication_tag() {
  validate_tag_name "$environment" "$tag"
  remote_lines=$(publication_git ls-remote origin "refs/tags/$tag" "refs/tags/$tag^{}") || die "cannot inspect the proposed remote tag"
  if [ -n "$remote_lines" ]; then
    remote_object=$(printf '%s\n' "$remote_lines" | awk -v ref="refs/tags/$tag" '$2==ref {print $1}')
    remote_commit=$(printf '%s\n' "$remote_lines" | awk -v ref="refs/tags/$tag^{}" '$2==ref {print $1}')
    if [ -z "$remote_commit" ]; then remote_commit=$remote_object; fi
    [ "$remote_commit" = "$selected_commit" ] || die "remote tag points to another commit"
    if ! publication_git show-ref --verify --quiet "refs/tags/$tag"; then
      publication_git fetch --quiet --no-tags origin "refs/tags/$tag:refs/tags/$tag" || die "cannot retain the existing remote tag object locally"
    fi
    validate_tag "$environment" "$tag"
    [ "$tag_object" = "$remote_object" ] && [ "$tag_commit" = "$remote_commit" ] || die "local and remote tag objects differ"
  elif publication_git show-ref --verify --quiet "refs/tags/$tag"; then
    validate_tag "$environment" "$tag"
    [ "$tag_commit" = "$selected_commit" ] || die "existing local tag points to another commit"
  else
    publication_git tag -a "$tag" "$selected_commit" -m "$tag" || die "cannot create local annotated release tag"
    validate_tag "$environment" "$tag"
  fi
}

create_draft() {
  prerelease=false
  [ "$environment" = stage ] && prerelease=true
  set +e
  run_gh release create "$tag" --repo "$repository" --verify-tag --draft \
    "--prerelease=$prerelease" --latest=false --title "$tag" --notes-file /public/release-body.md >/dev/null
  create_status=$?
  set -e
  refresh_remote_state
  plan_status=$(awk -F '\t' '$1=="status" {print $2}' "$PUBLICATION_TEMP/plan")
  [ "$plan_status" = draft ] || die "draft creation did not reconcile to this exact retained publication"
  if [ "$create_status" -ne 0 ]; then
    reconcile_remote true
    die "draft creation response was lost or another publisher won; state was retained, use explicit resume"
  fi
}

upload_missing_assets() {
  set -- release upload "$tag" --repo "$repository"
  missing_count=0
  while IFS=$'\t' read -r field name extra; do
    [ "$field" = missing ] || continue
    [ -z "$extra" ] || die "missing asset plan is malformed"
    set -- "$@" "/assets/$name"
    missing_count=$((missing_count + 1))
  done <"$PUBLICATION_TEMP/plan"
  [ "$missing_count" -gt 0 ] || return 0
  run_gh "$@" >/dev/null || die "asset upload was interrupted; reconcile with explicit resume"
}

check_production_policy() {
  [ "$environment" = prod ] || return 0
  publication_helper publication stable-history --dist /out --remote-json /state/releases.json >"$PUBLICATION_TEMP/stable-policy" || \
    die "production stable release history rejected this version"
  latest_required=$(awk -F '\t' '$1=="latest_required" {print $2}' "$PUBLICATION_TEMP/stable-policy")
  case "$latest_required" in
    true)
      run_gh api --method GET "repos/$repository/releases/latest" >"$PUBLICATION_TEMP/latest.json" || \
        die "cannot establish the current stable latest release"
      publication_helper publication latest --environment prod --dist /out --remote-json /state/latest.json || \
        die "production latest policy rejected this release"
      ;;
    false) ;;
    *) die "production latest policy returned an invalid result" ;;
  esac
}

finalize_release() {
  refresh_remote_state
  check_production_policy
  if [ "$environment" = prod ]; then
    run_gh release edit "$tag" --repo "$repository" --draft=false --prerelease=false --latest=true >/dev/null || true
  else
    run_gh release edit "$tag" --repo "$repository" --draft=false --prerelease --latest=false >/dev/null || true
  fi
  refresh_remote_state
  reconcile_remote false
  final_status=$(awk -F '\t' '$1=="status" {print $2}' "$PUBLICATION_TEMP/reconciled")
  [ "$final_status" = published ] || die "finalization response was inconclusive; retained publication remains resumable"
}

publish_retained() {
  resume_mode=$1
  verify_output "$environment" "$dist"
  allocate_publication_temp
  if [ ! -f "$dist/publication.json" ]; then
    if [ "$resume_mode" = true ]; then
      recover_lost_publication
    else
      read_candidate_identity
      enforce_new_source_policy
      assets=
      check_publisher_access
      publication_args=(publication prepare --environment "$environment" --dist /out --notes /notes \
        --publisher-image-id "$IMAGE_ID" --publisher-platform "$IMAGE_PLATFORM")
      [ -z "$stage_acceptance" ] || publication_args+=(--stage-acceptance /acceptance --from-stage-tag "$from_stage_tag")
      set -- --rm --network none -v "$dist:/out" -v "$notes:/notes:ro"
      [ -z "$stage_acceptance" ] || set -- "$@" -v "$stage_acceptance:/acceptance:ro"
      helper_arch=${IMAGE_PLATFORM#linux/}
      container_run --rm --network none --read-only -v "$dist:/out:ro" --entrypoint release-helper-check \
        "$IMAGE_ID" --receipt /out/receipt.json --tools /out/tools --arch "$helper_arch" >/dev/null || \
        die "retained release helper failed its trusted digest check"
      if ! container_run "$@" --entrypoint "/out/tools/releasecheck-linux-$helper_arch" "$IMAGE_ID" "${publication_args[@]}"; then
        die "publication eligibility is incomplete; retained parent: $dist; complete docs/releases/RUNBOOK.md#native-evidence before retrying"
      fi
    fi
  fi
  read_publication_identity
  validate_explicit_publication_inputs
  validate_remote_identity "$repository"
  check_publisher_access
  refresh_remote_state
  verify_remote_stage_acceptance
  plan_status=$(awk -F '\t' '$1=="status" {print $2}' "$PUBLICATION_TEMP/plan")
  if [ "$plan_status" = published ]; then
    require_exact_remote_tag
    reconcile_remote false
    echo "release already complete: $tag"
    return
  fi
  check_production_policy
  if [ "$plan_status" = absent ]; then
    push_exact_tag
    create_draft
  elif [ "$resume_mode" != true ]; then
    die "a matching draft already exists; use explicit resume with this retained parent"
  else
    require_exact_remote_tag
  fi
  reconcile_remote true
  cp "$PUBLICATION_TEMP/reconciled" "$PUBLICATION_TEMP/plan"
  upload_missing_assets
  refresh_remote_state
  reconcile_remote false
  finalize_release
  echo "release published: $tag"
}

parse_environment_only() {
  environment=
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --environment) [ "$#" -ge 2 ] || usage; environment=$2; shift 2 ;;
      *) usage ;;
    esac
  done
  require_environment "$environment"
}

command=${1:-}
[ -n "$command" ] || usage
shift || true

if [ "$command" = _make ]; then
  [ "$#" -eq 1 ] || usage
  make_action=$1
  case "$make_action" in
    check)
      command=check
      set -- --environment "${RELEASE_MAKE_ENV-}"
      ;;
    snapshot)
      command=snapshot
      set -- --environment "${RELEASE_MAKE_ENV-}"
      if [ -n "${RELEASE_MAKE_REF-}" ]; then set -- "$@" --ref "$RELEASE_MAKE_REF"; fi
      ;;
    build)
      command=build
      set -- --environment "${RELEASE_MAKE_ENV-}" --tag "${RELEASE_MAKE_TAG-}"
      ;;
    verify)
      command=verify
      set -- --environment "${RELEASE_MAKE_ENV-}" --dist "${RELEASE_MAKE_DIST-}"
      ;;
    status)
      command=status
      set -- --environment "${RELEASE_MAKE_ENV-}" --tag "${RELEASE_MAKE_TAG-}"
      if [ -n "${RELEASE_MAKE_DIST-}" ]; then set -- "$@" --dist "$RELEASE_MAKE_DIST"; fi
      ;;
    resume)
      command=resume
      set -- --environment "${RELEASE_MAKE_ENV-}" --tag "${RELEASE_MAKE_TAG-}" --dist "${RELEASE_MAKE_DIST-}"
      ;;
    stage-release)
      [ -z "${RELEASE_MAKE_ENV-}" ] || [ "$RELEASE_MAKE_ENV" = stage ] || die "stage-release fixes ENV=stage"
      command=publish
      set -- --environment stage --tag "${RELEASE_MAKE_TAG-}" --notes "${RELEASE_MAKE_NOTES_FILE-}"
      if [ -n "${RELEASE_MAKE_REF-}" ]; then set -- "$@" --ref "$RELEASE_MAKE_REF"; fi
      if [ -n "${RELEASE_MAKE_DIST-}" ]; then set -- "$@" --dist "$RELEASE_MAKE_DIST"; fi
      ;;
    prod-release)
      [ -z "${RELEASE_MAKE_ENV-}" ] || [ "$RELEASE_MAKE_ENV" = prod ] || die "prod-release fixes ENV=prod"
      command=publish
      set -- --environment prod --tag "${RELEASE_MAKE_TAG-}" --notes "${RELEASE_MAKE_NOTES_FILE-}" \
        --from-stage-tag "${RELEASE_MAKE_FROM_STAGE_TAG-}" --stage-acceptance "${RELEASE_MAKE_STAGE_ACCEPTANCE-}"
      if [ -n "${RELEASE_MAKE_REF-}" ]; then set -- "$@" --ref "$RELEASE_MAKE_REF"; fi
      if [ -n "${RELEASE_MAKE_DIST-}" ]; then set -- "$@" --dist "$RELEASE_MAKE_DIST"; fi
      ;;
    *) usage ;;
  esac
fi

# Source commands check cleanliness after rejecting unsafe Git source views.
# These other mutating commands do not select release source.
case "$command" in
  tools|acceptance-template|native-requirements|native-review-template|native-manual|native-evidence)
    require_clean_tree
    ;;
esac

case "$command" in
  _require-clean-tree)
    [ "$#" -eq 0 ] || usage
    require_clean_tree
    ;;
  _toolchain-value)
    toolchain_value "$@"
    ;;
  _tools-identity)
    [ "$#" -eq 0 ] || usage
    load_toolchain "$REPOSITORY_ROOT/release/toolchain.env"
    CONTROL_ROOT=$REPOSITORY_ROOT
    inspect_release_image
    printf '%s\t%s\n' "$IMAGE_ID" "$IMAGE_PLATFORM"
    ;;
  tools)
    [ "$#" -eq 0 ] || usage
    build_tools
    ;;
  check)
    parse_environment_only "$@"
    safe_repository_view
    selected_commit=$(resolve_commit HEAD)
    prepare_source "$selected_commit"
    inspect_release_image
    run_check "$source_checkout" "$environment"
    ;;
  snapshot)
    environment= ref=HEAD
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --environment) [ "$#" -ge 2 ] || usage; environment=$2; shift 2 ;;
        --ref) [ "$#" -ge 2 ] || usage; ref=$2; shift 2 ;;
        *) usage ;;
      esac
    done
    require_environment "$environment"
    safe_repository_view
    require_clean_tree
    selected_commit=$(resolve_commit "$ref")
    tag_object=
    prepare_source "$selected_commit"
    inspect_release_image
    run_build snapshot "$environment" ""
    ;;
  build)
    environment= tag=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --environment) [ "$#" -ge 2 ] || usage; environment=$2; shift 2 ;;
        --tag) [ "$#" -ge 2 ] || usage; tag=$2; shift 2 ;;
        *) usage ;;
      esac
    done
    require_environment "$environment"
    [ -n "$tag" ] || die "build requires --tag naming the release version"
    safe_repository_view
    require_clean_tree
    validate_tag_name "$environment" "$tag"
    if ! publication_git show-ref --verify --quiet "refs/tags/$tag"; then
      selected_commit=$(resolve_commit HEAD)
      prepare_source "$selected_commit"
      inspect_release_image
      run_check "$source_checkout" "$environment"
      require_clean_tree
      publication_git tag -a "$tag" "$selected_commit" -m "$tag" || die "cannot create local annotated release tag"
      rm -rf "$SOURCE_TEMP"
      SOURCE_TEMP=
    fi
    validate_tag "$environment" "$tag"
    selected_commit=$tag_commit
    echo "release tag: $tag ($selected_commit)"
    prepare_source "$selected_commit" "$tag_object"
    inspect_release_image
    run_build release "$environment" "$tag"
    ;;
  verify)
    environment= dist=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --environment) [ "$#" -ge 2 ] || usage; environment=$2; shift 2 ;;
        --dist) [ "$#" -ge 2 ] || usage; dist=$2; shift 2 ;;
        *) usage ;;
      esac
    done
    require_environment "$environment"
    [ -n "$dist" ] || die "verify requires --dist"
    verify_output "$environment" "$dist"
    ;;
  publish)
    environment= tag= ref=HEAD ref_supplied=false notes= dist= from_stage_tag= stage_acceptance=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --environment) [ "$#" -ge 2 ] || usage; environment=$2; shift 2 ;;
        --tag) [ "$#" -ge 2 ] || usage; tag=$2; shift 2 ;;
        --ref) [ "$#" -ge 2 ] || usage; ref=$2; ref_supplied=true; shift 2 ;;
        --notes) [ "$#" -ge 2 ] || usage; notes=$2; shift 2 ;;
        --dist) [ "$#" -ge 2 ] || usage; dist=$2; shift 2 ;;
        --from-stage-tag) [ "$#" -ge 2 ] || usage; from_stage_tag=$2; shift 2 ;;
        --stage-acceptance) [ "$#" -ge 2 ] || usage; stage_acceptance=$2; shift 2 ;;
        *) usage ;;
      esac
    done
    require_environment "$environment"
    [ -n "$tag" ] && [ -n "$notes" ] || usage
    case "$notes" in /*) ;; *) die "--notes must be an absolute reviewed file" ;; esac
    [ -f "$notes" ] && [ ! -L "$notes" ] || die "reviewed release notes are missing or unsafe"
    [ -n "${GITHUB_TOKEN-}" ] || die "publish requires GITHUB_TOKEN before release work begins"
    if [ "$environment" = prod ]; then
      [ -n "$from_stage_tag" ] && [ -n "$stage_acceptance" ] || die "production requires --from-stage-tag and --stage-acceptance"
      case "$stage_acceptance" in /*) ;; *) die "--stage-acceptance must be absolute" ;; esac
      [ -f "$stage_acceptance" ] && [ ! -L "$stage_acceptance" ] || die "stage acceptance is missing or unsafe"
    elif [ -n "$from_stage_tag$stage_acceptance" ]; then
      die "stage publication does not accept production promotion inputs"
    fi
    safe_repository_view
    require_clean_tree
    acquire_publication_lock
    if [ -z "$dist" ]; then
      selected_commit=$(resolve_commit "$ref")
      tag_object=
      prepare_source "$selected_commit"
      inspect_release_image
      run_check "$source_checkout" "$environment"
      validate_remote_identity "$source_repository"
      repository=$source_repository
      assets=
      check_publisher_access
      branch=$environment
      [ "$environment" = prod ] && branch=main
      publication_git fetch --quiet --no-tags origin "+refs/heads/$branch:refs/remotes/origin/$branch" || die "cannot fetch the release branch"
      if [ "$environment" = stage ]; then
        [ "$(publication_git rev-parse "refs/remotes/origin/$branch")" = "$selected_commit" ] || die "new stage publication must use the fetched stage tip"
      else
        publication_git merge-base --is-ancestor "$selected_commit" "refs/remotes/origin/$branch" || die "new production publication must be reachable from fetched main"
      fi
      prepare_local_publication_tag
      rm -rf "$SOURCE_TEMP"
      SOURCE_TEMP=
      prepare_source "$selected_commit" "$tag_object"
      run_build release "$environment" "$tag"
      dist=$output_parent
    else
      case "$dist" in /*) ;; *) die "--dist must be absolute" ;; esac
      load_toolchain "$REPOSITORY_ROOT/release/toolchain.env"
      CONTROL_ROOT=$REPOSITORY_ROOT
      inspect_release_image
    fi
    publish_retained false
    ;;
  resume)
    environment= tag= dist= notes= stage_acceptance= from_stage_tag=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --environment) [ "$#" -ge 2 ] || usage; environment=$2; shift 2 ;;
        --tag) [ "$#" -ge 2 ] || usage; tag=$2; shift 2 ;;
        --dist) [ "$#" -ge 2 ] || usage; dist=$2; shift 2 ;;
        *) usage ;;
      esac
    done
    require_environment "$environment"
    [ -n "$tag" ] && [ -n "$dist" ] || usage
    [ -n "${GITHUB_TOKEN-}" ] || die "resume requires GITHUB_TOKEN before release work begins"
    safe_repository_view
    require_clean_tree
    acquire_publication_lock
    load_toolchain "$REPOSITORY_ROOT/release/toolchain.env"
    CONTROL_ROOT=$REPOSITORY_ROOT
    inspect_release_image
    publish_retained true
    ;;
  status)
    environment= tag= dist=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --environment) [ "$#" -ge 2 ] || usage; environment=$2; shift 2 ;;
        --tag) [ "$#" -ge 2 ] || usage; tag=$2; shift 2 ;;
        --dist) [ "$#" -ge 2 ] || usage; dist=$2; shift 2 ;;
        *) usage ;;
      esac
    done
    require_environment "$environment"
    [ -n "$tag" ] || usage
    validate_tag_name "$environment" "$tag"
    if [ -n "$dist" ]; then
      case "$dist" in /*) ;; *) die "status --dist must be an absolute retained output parent" ;; esac
      validate_path_components "$dist"
      [ -d "$dist" ] && [ ! -L "$dist" ] || die "status --dist is missing or unsafe"
      dist=$(cd "$dist" && pwd -P) || die "cannot canonicalize status retained output"
      [ "$dist" != / ] || die "status --dist cannot be filesystem root"
      status_tmp=${TMPDIR:-/tmp}
      [ -d "$status_tmp" ] || die "status temporary directory is missing or unsafe"
      status_tmp=$(cd "$status_tmp" && pwd -P) || die "cannot canonicalize status temporary directory"
      validate_path_components "$status_tmp"
      [ "$status_tmp" != / ] || die "status temporary directory cannot be filesystem root"
      path_is_within "$dist" "$status_tmp" && die "status temporary directory must be outside retained DIST"
      TMPDIR=$status_tmp
    fi
    [ -n "${GITHUB_TOKEN-}" ] || die "status requires a read-capable GITHUB_TOKEN to distinguish drafts from absence"
    safe_repository_view
    selected_commit=$(resolve_commit HEAD)
    prepare_source "$selected_commit"
    inspect_release_image
    resolve_server_url "$source_checkout" "$environment"
    repository=$source_repository
    validate_remote_identity "$repository"
    PUBLICATION_TEMP=$(mktemp -d "${TMPDIR:-/tmp}/hookspot-publication-status.XXXXXX") || die "cannot allocate status workspace"
    chmod 700 "$PUBLICATION_TEMP"
    assets=
    if [ -n "$dist" ]; then
      PUBLICATION_HELPER_READ_ONLY=1
      verify_output "$environment" "$dist"
      read_publication_identity
      [ "$repository" = "$source_repository" ] || die "retained publication repository differs from selected source"
      refresh_remote_state
      plan_status=$(awk -F '\t' '$1=="status" {print $2}' "$PUBLICATION_TEMP/plan")
      require_status_tag_identity
      cat "$PUBLICATION_TEMP/plan"
      PUBLICATION_HELPER_READ_ONLY=
    else
      run_gh release view "$tag" --repo "$repository" --json databaseId,tagName,isDraft,isPrerelease || \
        die "release status is unavailable; no absence is inferred"
    fi
    ;;
  acceptance-template)
    dist= output=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --dist) [ "$#" -ge 2 ] || usage; dist=$2; shift 2 ;;
        --output) [ "$#" -ge 2 ] || usage; output=$2; shift 2 ;;
        *) usage ;;
      esac
    done
    case "$dist:$output" in /*:/*) ;; *) usage ;; esac
    case "$output" in */) die "stage acceptance output must name a file" ;; esac
    environment=stage
    output_dir=$(dirname "$output")
    output_name=$(basename "$output")
    [ "$output_name" != . ] && [ "$output_name" != .. ] && [ -n "$output_name" ] || die "stage acceptance output name is invalid"
    validate_path_components "$dist"
    validate_path_components "$output_dir"
    [ -d "$dist" ] && [ ! -L "$dist" ] || die "retained output parent is missing or unsafe"
    [ -d "$output_dir" ] && [ ! -L "$output_dir" ] || die "stage acceptance output directory is missing or unsafe"
    dist=$(cd "$dist" && pwd -P) || die "cannot canonicalize retained output parent"
    output_dir=$(cd "$output_dir" && pwd -P) || die "cannot canonicalize stage acceptance output directory"
    [ "$dist" != / ] || die "retained output parent cannot be filesystem root"
    [ "$output_dir" != / ] || die "stage acceptance output directory cannot be filesystem root"
    paths_overlap "$dist" "$output_dir" && die "stage acceptance output must be outside retained release tree"
    [ ! -e "$output" ] && [ ! -L "$output" ] || die "stage acceptance output already exists"
    unset GITHUB_TOKEN GH_TOKEN
    verify_output stage "$dist" >/dev/null
    helper_arch=${IMAGE_PLATFORM#linux/}
    container_run --rm --network none --read-only -v "$dist:/out:ro" -v "$output_dir:/destination" \
      --entrypoint "/out/tools/releasecheck-linux-$helper_arch" "$IMAGE_ID" publication acceptance-template \
      --dist /out --output "/destination/$output_name" >/dev/null || \
      die "cannot create stage acceptance template from an incomplete publication"
    echo "stage acceptance template: $output"
    ;;
  native-requirements)
    environment= dist= review= baseline_dist=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --environment) [ "$#" -ge 2 ] || usage; environment=$2; shift 2 ;;
        --dist) [ "$#" -ge 2 ] || usage; dist=$2; shift 2 ;;
        --review) [ "$#" -ge 2 ] || usage; review=$2; shift 2 ;;
        --baseline-dist) [ "$#" -ge 2 ] || usage; baseline_dist=$2; shift 2 ;;
        *) usage ;;
      esac
    done
    require_environment "$environment"
    [ -n "$dist" ] || usage
    if [ -n "$review$baseline_dist" ]; then
      case "$review:$baseline_dist" in /*:/*) ;; *) die "routine requirements need absolute --review and --baseline-dist together" ;; esac
      validate_path_components "$dist"
      validate_path_components "$(dirname "$review")"
      [ -f "$review" ] && [ ! -L "$review" ] || die "routine review is missing or unsafe"
      validate_path_components "$baseline_dist"
      [ -d "$baseline_dist" ] && [ ! -L "$baseline_dist" ] || die "routine baseline is missing or unsafe"
      dist=$(cd "$dist" && pwd -P) || die "cannot canonicalize retained output parent"
      baseline_dist=$(cd "$baseline_dist" && pwd -P) || die "cannot canonicalize routine baseline"
      review_name=$(basename "$review")
      review_dir=$(dirname "$review")
      review_dir=$(cd "$review_dir" && pwd -P) || die "cannot canonicalize routine review directory"
      review="$review_dir/$review_name"
      [ "$dist" != / ] && [ "$baseline_dist" != / ] || die "routine retained parents cannot be filesystem root"
      paths_overlap "$dist" "$baseline_dist" && die "routine baseline and current retained tree must be disjoint"
      path_is_within "$dist" "$review" && die "routine review must be outside current retained tree"
      for destination in "$dist/native/baseline" "$dist/native/review.json" "$dist/native/requirements.json"; do
        [ ! -e "$destination" ] && [ ! -L "$destination" ] || die "routine native destination already exists"
      done
    fi
    unset GITHUB_TOKEN GH_TOKEN
    verify_output "$environment" "$dist"
    helper_arch=${IMAGE_PLATFORM#linux/}
    set -- --rm --network none --read-only -v "$dist:/out"
    native_args=(native-requirements --dist /out)
    if [ -n "$review" ]; then
      set -- "$@" -v "$review:/review:ro" -v "$baseline_dist:/baseline:ro"
      native_args+=(--review /review --baseline-dist /baseline)
    fi
    container_run "$@" --entrypoint "/out/tools/releasecheck-linux-$helper_arch" "$IMAGE_ID" "${native_args[@]}" || \
      die "cannot create native requirements"
    echo "native requirements retained: $dist/native/requirements.json"
    ;;
  native-review-template)
    environment= dist= baseline_dist= output=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --environment) [ "$#" -ge 2 ] || usage; environment=$2; shift 2 ;;
        --dist) [ "$#" -ge 2 ] || usage; dist=$2; shift 2 ;;
        --baseline-dist) [ "$#" -ge 2 ] || usage; baseline_dist=$2; shift 2 ;;
        --output) [ "$#" -ge 2 ] || usage; output=$2; shift 2 ;;
        *) usage ;;
      esac
    done
    require_environment "$environment"
    [ -n "$dist" ] && [ -n "$baseline_dist" ] && [ -n "$output" ] || usage
    case "$dist:$baseline_dist:$output" in /*:/*:/*) ;; *) die "native review template paths must be absolute" ;; esac
    case "$output" in */) die "native review template output must name a file" ;; esac
    output_dir=$(dirname "$output")
    output_name=$(basename "$output")
    validate_path_components "$dist"
    validate_path_components "$baseline_dist"
    validate_path_components "$output_dir"
    [ -d "$dist" ] && [ ! -L "$dist" ] || die "current retained output parent is missing or unsafe"
    [ -d "$baseline_dist" ] && [ ! -L "$baseline_dist" ] || die "first-support baseline is missing or unsafe"
    [ -d "$output_dir" ] && [ ! -L "$output_dir" ] || die "review template output directory is missing or unsafe"
    dist=$(cd "$dist" && pwd -P) || die "cannot canonicalize retained output parent"
    baseline_dist=$(cd "$baseline_dist" && pwd -P) || die "cannot canonicalize first-support baseline"
    output_dir=$(cd "$output_dir" && pwd -P) || die "cannot canonicalize review template output directory"
    [ "$dist" != / ] && [ "$baseline_dist" != / ] || die "review template retained parents cannot be filesystem root"
    paths_overlap "$dist" "$baseline_dist" && die "review template current and baseline retained trees must be disjoint"
    [ "$output_dir" != / ] || die "review template output directory cannot be filesystem root"
    paths_overlap "$output_dir" "$dist" && die "review template output must be outside retained release trees"
    paths_overlap "$output_dir" "$baseline_dist" && die "review template output must be outside retained release trees"
    [ "$output_name" != . ] && [ "$output_name" != .. ] && [ -n "$output_name" ] || die "review template output name is invalid"
    [ ! -e "$output" ] && [ ! -L "$output" ] || die "review template output already exists"
    unset GITHUB_TOKEN GH_TOKEN
    verify_output "$environment" "$dist" >/dev/null
    helper_arch=${IMAGE_PLATFORM#linux/}
    container_run --rm --network none --read-only \
      -v "$dist:/out:ro" -v "$baseline_dist:/baseline:ro" -v "$output_dir:/destination" \
      --entrypoint "/out/tools/releasecheck-linux-$helper_arch" "$IMAGE_ID" native-review-template \
      --dist /out --baseline-dist /baseline --output "/destination/$output_name" >/dev/null || \
      die "cannot create native review template"
    echo "native review template created and remains incomplete until every blank field is reviewed: $output"
    ;;
  native-manual)
    environment= dist= target= check= result= operator= procedure=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --environment) [ "$#" -ge 2 ] || usage; environment=$2; shift 2 ;;
        --dist) [ "$#" -ge 2 ] || usage; dist=$2; shift 2 ;;
        --target) [ "$#" -ge 2 ] || usage; target=$2; shift 2 ;;
        --check) [ "$#" -ge 2 ] || usage; check=$2; shift 2 ;;
        --result) [ "$#" -ge 2 ] || usage; result=$2; shift 2 ;;
        --operator) [ "$#" -ge 2 ] || usage; operator=$2; shift 2 ;;
        --procedure) [ "$#" -ge 2 ] || usage; procedure=$2; shift 2 ;;
        *) usage ;;
      esac
    done
    require_environment "$environment"
    [ -n "$dist" ] && [ -n "$target" ] && [ -n "$check" ] && [ -n "$result" ] && [ -n "$operator" ] && [ -n "$procedure" ] || usage
    unset GITHUB_TOKEN GH_TOKEN
    verify_output "$environment" "$dist"
    helper_arch=${IMAGE_PLATFORM#linux/}
    container_run --rm --network none --read-only -v "$dist:/out" \
      --entrypoint "/out/tools/releasecheck-linux-$helper_arch" "$IMAGE_ID" native-manual --dist /out \
      --target "$target" --check "$check" --result "$result" --operator "$operator" --procedure "$procedure" || \
      die "cannot retain native manual check"
    ;;
  native-evidence)
    environment= dist=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --environment) [ "$#" -ge 2 ] || usage; environment=$2; shift 2 ;;
        --dist) [ "$#" -ge 2 ] || usage; dist=$2; shift 2 ;;
        *) usage ;;
      esac
    done
    require_environment "$environment"
    [ -n "$dist" ] || usage
    unset GITHUB_TOKEN GH_TOKEN
    verify_output "$environment" "$dist"
    helper_arch=${IMAGE_PLATFORM#linux/}
    container_run --rm --network none --read-only -v "$dist:/out" \
      --entrypoint "/out/tools/releasecheck-linux-$helper_arch" "$IMAGE_ID" native-evidence --dist /out || \
      die "native evidence remains incomplete; retain the required host reports and manual checks before retrying"
    echo "native evidence retained: $dist/native/evidence.json"
    ;;
  *) usage ;;
esac
