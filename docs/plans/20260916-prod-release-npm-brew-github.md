# Prod release to npm, Homebrew and GitHub

## Overview

Publish every production release of the Hookspot CLI to three channels from one
tag push:

- **GitHub Releases**: six platform archives (`tar.gz`, `zip` for Windows) and a
  SHA-256 checksum file, exactly as today.
- **npm**: a single `hookspot` package bundling all six binaries with a Node
  launcher (the hookdeck-cli layout). `npm install -g hookspot`.
- **Homebrew**: a `hookspot` formula in the `bgr11n/homebrew-hookspot` tap.
  `brew install bgr11n/hookspot/hookspot`. Works on macOS and Linux and does not
  quarantine the unsigned binary (casks would).

Publication moves into a GitHub Actions workflow triggered by `v*` tags.
GoReleaser publishes the GitHub Release and the Homebrew formula; the same job
then publishes npm. Local machines only build unpublished snapshots.

The separate `stage` environment is removed entirely: no `hookspot-stage`
binary, no `-stage.N` tags, no stage acceptance gate, no stage server URL.
Local testing of unreleased features uses `make release-snapshot` (all six
prod-endpoint binaries, version `0.0.0-snapshot.<sha>`, nothing published) or
`make build SERVER_URL=...` (dev-endpoint build).

This replaces the bespoke, maintainer-run publisher in `scripts/release.sh`
(1367 lines plus a 2064-line test) and all of `tools/releasecheck` except the
`metadata` subcommand with the standard GoReleaser-in-Actions flow.

## Context (from discovery)

- **Project**: Go 1.26.8 cobra CLI, module `hookspot`, public repo
  `bgr11n/hookspot-cli`. No LICENSE file; `docs/releases/THIRD_PARTY_NOTICES.txt`
  exists.
- **Existing release flow**: `.goreleaser.yaml` (v2, `release: disable`) builds
  six targets inside a locked Docker image (`Dockerfile.release`,
  `release/toolchain.env`). `scripts/release.sh` handles tags, snapshots,
  verification, receipts, native evidence, stage acceptance, and GitHub upload
  via a vendored `gh`. `tools/releasecheck` (9.5k lines) validates
  environments, metadata, artifacts, receipts, smoke reports, and publication.
  `tools/releasebootstrap` verifies helper digests. `.github/` is empty.
- **Environment wiring**: `cmd/build_info.go` accepts `dev|stage|prod`;
  `internal/endpoint.Parse` and `internal/config.validEnvironment` gate the same
  set; `cmd/root.go:executableName` returns `hookspot-stage` for stage and is
  called from `cmd/root.go` (`rootCmd.Use`, `configRecoveryHint`),
  `cmd/errors.go`, `cmd/listen.go`, and `cmd/project.go`.
  `release/environments.json` holds stage/prod server URLs and branches;
  `tools/releasecheck/metadata.go` and `artifacts.go` read it through
  `environment.go`.
- **GoReleaser v2.17.1 facts that constrain the design** (verified in source
  during review): no top-level `after:` hooks in OSS; `goreleaser check` exits
  2 when a deprecated section such as `brews` is present; hooks run without a
  shell; `dist/artifacts.json` is written after publishing.
- **Bug**: `cmd/version.go` queries `repos/hookspot/hookspot-cli/releases/latest`;
  that repository does not exist. The real one is `bgr11n/hookspot-cli`.
- **External state**: npm name `hookspot` is unclaimed; `bgr11n/homebrew-hookspot`
  does not exist; the repo has no Actions secrets; old tags `v0.0.0-stage.1`,
  `stage_*` and one pre-release exist.
- **Dirty tree**: 17 modified files are uncommitted at planning time. Commit or
  review them before Task 1.
- **Reference**: hookdeck-cli (cloned during planning) uses one fat npm package
  with `binaries/<os>-<arch>/` and `bin/hookdeck.js`, publishes with
  `npm publish --provenance`, and disabled `homebrew_casks` because casks
  quarantine unsigned binaries.

## Development Approach

- **testing approach**: Regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
  - tasks that only touch Makefile, YAML, or Markdown have no unit tests;
    their check is the listed integration command
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change
- deleting code leaves no vestigial references (YAGNI)
- backward compatibility is explicitly not required for stage; the `hookspot`
  binary, its config, and `version --json` output keep working for prod users

## Testing Strategy

- **unit tests**: `make test` (Go, in the locked Docker image) and
  `make npm-test` (`node --test npm/test/`). Required for every code task.
- **integration tests**: `make release-snapshot` runs the full GoReleaser
  pipeline unpublished. Run it after every task that touches
  `.goreleaser.yaml`, the Makefile, `Dockerfile.release`, or
  `tools/releasecheck`.
- **e2e tests**: the release workflow's smoke job installs from each channel on
  ubuntu, macOS, and Windows runners and asserts `hookspot version --json`.
  It runs only after a real tag. `ci.yml` is the dry run for the build path.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

```
git tag v1.2.3 && git push origin v1.2.3
        │
        ▼  .github/workflows/release.yml
┌──────────────────────────────────────────────────────────────┐
│ release (ubuntu)                                             │
│   make release-tools      # build locked goreleaser image    │
│   make release-publish    # goreleaser release --clean       │
│     ├─ before hook: releasecheck metadata → build-info.json  │
│     ├─ builds 6 binaries; post-hooks copy each into          │
│     │  npm/binaries/<os>-<arch>/                             │
│     ├─ 6 archives + checksums                                │
│     ├─ creates GitHub Release, uploads 7 assets              │
│     └─ pushes Formula/hookspot.rb to bgr11n/homebrew-hookspot│
│   setup-node; npm version ${TAG#v}; npm publish --provenance │
├──────────────────────────────────────────────────────────────┤
│ smoke (needs release; matrix ubuntu/macos/windows)           │
│   gh release download + checksum → hookspot version --json   │
│   npm install -g hookspot@X → hookspot version --json        │
│   macOS: brew install bgr11n/hookspot/hookspot → version     │
└──────────────────────────────────────────────────────────────┘
```

Key decisions:

- **One source of truth**: GoReleaser builds once; npm and Homebrew consume
  those exact bytes. No rebuild per channel.
- **Locked toolchain in CI and locally**: both run the same
  `Dockerfile.release` image (pinned Go + GoReleaser digests from
  `release/toolchain.env`), so `make release-snapshot` on a laptop is
  byte-for-byte the CI build minus publication.
- **Prod server URL lives once, in `.goreleaser.yaml`** (`env:
PROD_SERVER_URL=...`), templated into both the ldflags and the
  `releasecheck metadata --server-url` hook. The JSON manifest and
  `releasecheck env`/`env-url` are deleted.
- **Build environments collapse to `dev` and `prod`**. `prod` still requires
  release/snapshot build metadata; `dev` still requires an explicit
  `SERVER_URL`.
- **`brews` formula, not `homebrew_casks`**: soft-deprecated in GoReleaser but
  supported in the pinned v2.17.1; avoids Gatekeeper quarantine on the unsigned
  binary and works on Linux. `goreleaser check` therefore exits 2, which
  `make release-check` treats as success with a comment saying why. Revisit if
  the binary is ever signed.
- **Verification is post-publish**: the smoke matrix is the acceptance test.
  `releasecheck artifacts` and its archive/executable/receipt helpers are
  deleted; they duplicated GoReleaser's checksum step and were coupled to its
  internal `artifacts.json` layout. A red smoke job means yank, documented in
  the runbook.
- **npm publishes from the release job**, not a downstream job:
  `actions/upload-artifact` drops the executable bit, and a hand-off buys
  nothing here.
- **npm auth is a granular `NPM_TOKEN` secret** for now. Trusted Publishing
  can only be attached to a package that already exists; migrate after the
  first publish (Post-Completion).
- **Pre-release tags** (`v1.2.3-rc.1`) are supported minimally: GoReleaser
  marks the GitHub Release as pre-release, the formula upload is skipped, and
  npm publishes under the `next` dist-tag. No other channel logic.
- **Clean tree is required for `release-snapshot` and `release-publish`**:
  the build uses `-buildvcs=true` and the Makefile mounts the live checkout,
  so a dirty tree would change the embedded VCS metadata. A short
  `git status --porcelain` guard replaces the old `_require-clean-tree`.

## Technical Details

**Version and metadata**

- Tag `vX.Y.Z` → GoReleaser `{{ .Version }}` = `X.Y.Z`; snapshot version
  `0.0.0-snapshot.<shortsha>` (unchanged).
- ldflags keep `version`, `serverURL`, `buildEnvironment=prod`, `commit`,
  `sourceDate`, `buildKind=release|snapshot`.
- Archive name: `hookspot_{{ .Version }}_{{ .Os }}_{{ .Arch }}.tar.gz`
  (`.zip` on Windows). Checksums: `hookspot_{{ .Version }}_checksums.txt`.
  The `_{{ .Env.RELEASE_ENV }}_` segment is gone.
- `build-info.json` is written to the repo root by the before hook and
  bundled into every archive as today; the Makefile removes it before each
  run so the exclusive-create in `metadata.go` does not trip on reruns.

**`releasecheck metadata` after the change**

```
go run ./tools/releasecheck metadata \
  --version {{ .Version }} --commit {{ .FullCommit }} \
  --source-date {{ .CommitDate }} \
  --kind {{ if .IsSnapshot }}snapshot{{ else }}release{{ end }} \
  --server-url {{ .Env.PROD_SERVER_URL }} --output build-info.json
```

`--output` may be relative (resolved against the working directory).
`environment` in the JSON is the constant `prod`. Version must match the prod
or snapshot pattern; the stage pattern is deleted. The GoReleaser version is
still read from `release/toolchain.env`, whose line format stays strict
(`KEY=value`, no comments or blank lines).

**npm package (`npm/`)**

```
npm/
  package.json        name hookspot, version 0.0.0 (set in CI), bin, files
  bin/hookspot.js     #!/usr/bin/env node launcher; exports resolveBinary()
  test/launcher.test.js   node --test
  binaries/           generated by GoReleaser post-hooks, gitignored
    darwin-amd64/hookspot  darwin-arm64/hookspot
    linux-amd64/hookspot   linux-arm64/hookspot
    windows-amd64/hookspot.exe  windows-arm64/hookspot.exe
  README.md           short install/usage pointer
```

Launcher maps Node `process.platform` `win32→windows` and `process.arch`
`x64→amd64`, `arm64→arm64`; anything else exits 1 with an "unsupported
platform" message. It runs the binary with `spawnSync(..., {stdio:
'inherit'})` and exits with the child's status (or 1 on spawn error).
`files: ["bin/", "binaries/"]` excludes `test/` without an `.npmignore`.

**GoReleaser build post-hook** (no shell, so wrap in `sh -c`)

```yaml
hooks:
  post:
    - sh -c 'mkdir -p npm/binaries/{{ .Os }}-{{ .Arch }} && cp "{{ .Path }}" npm/binaries/{{ .Os }}-{{ .Arch }}/hookspot{{ .Ext }}'
```

The Makefile clears `npm/binaries/` before every GoReleaser run because
`--clean` only empties `dist/`.

**Homebrew formula** (GoReleaser `brews`)

```yaml
brews:
  - name: hookspot
    ids: [cli]
    repository:
      owner: bgr11n
      name: homebrew-hookspot
      token: "{{ .Env.HOMEBREW_TAP_TOKEN }}"
    directory: Formula
    homepage: https://hookspot.dev
    description: Receive and forward Hookspot webhooks to a local server
    skip_upload: auto # no formula for pre-release tags
    install: bin.install "hookspot"
    test: system "#{bin}/hookspot", "version", "--json"
```

**Workflow secrets / permissions**

| Job     | Permission / secret                                                                     |
| ------- | --------------------------------------------------------------------------------------- |
| release | `contents: write`, `id-token: write`; `GITHUB_TOKEN`, `HOMEBREW_TAP_TOKEN`, `NPM_TOKEN` |
| smoke   | `contents: read`                                                                        |

Tokens are passed into the Docker container with `-e` only for
`release-publish`; `release-snapshot` passes none.

**Makefile targets after the change**

`tidy build test vet run get dev npm-test release-tools release-check
release-snapshot release-publish`. `release-publish` is for CI; it refuses to
run without `GITHUB_TOKEN` and `HOMEBREW_TAP_TOKEN` in the environment.
Toolchain values come from `include release/toolchain.env` instead of
`scripts/release.sh`. `Dockerfile.release` adds `git config --global --add
safe.directory /src` so GoReleaser's git calls work on runner-owned checkouts.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, config, workflow, docs, and
  tests inside this repository.
- **Post-Completion** (no checkboxes): tap repository creation, secrets, npm
  setup, license decision, first tagged release.

## Implementation Steps

### Task 1: Delete the bespoke publisher and rewrite the Makefile

**Files:**

- Modify: `Makefile`, `Dockerfile.release`, `release/toolchain.env`, `.gitignore`
- Delete: `scripts/release.sh`, `scripts/release_test.sh`, `scripts/smoke.sh`,
  `scripts/smoke.ps1`, `tools/releasebootstrap/` (all files),
  `docs/releases/NATIVE_CHECKS.md`

- [x] `Makefile`: `include release/toolchain.env`; drop `TOOLCHAIN_VALUE`,
      `_require-clean-tree`, `ENV/TAG/REF/DIST/NOTES_FILE/FROM_STAGE_TAG/
  STAGE_ACCEPTANCE`, `release-build`, `release-verify`, `release-status`,
      `release-resume`, `stage-release`, `prod-release`
- [x] `Makefile`: `release-tools` builds `Dockerfile.release` as
      `hookspot-release:local`; `release-check` runs `goreleaser check` and
      accepts exit 0 or 2 (comment: 2 = deprecation notice for `brews`);
      `release-snapshot` requires a clean tree, removes `build-info.json` and
      `npm/binaries/`, then runs `goreleaser release --snapshot --clean
  --skip=publish` with the repo mounted at `/src`, output to `./dist`, no
      tokens; `release-publish` does the same preparation, requires
      `GITHUB_TOKEN` and `HOMEBREW_TAP_TOKEN` (fail with a clear message
      otherwise), and runs `goreleaser release --clean`
- [x] `Dockerfile.release`: keep only the GoReleaser-onto-pinned-Go stage;
      remove the `gh` download and the `releasebootstrap` stage; add
      `git config --global --add safe.directory /src`;
      `release/toolchain.env`: remove `GH_VERSION` and `GH_SHA256_*`
- [x] delete the listed scripts, tool, and doc; grep the repo for
      `release.sh`, `releasebootstrap`, `smoke.sh`, `smoke.ps1`,
      `NATIVE_CHECKS`, `release-helper-check`, `_require-clean-tree` and remove
      every reference (README's clean-checkout paragraph for `make build/tidy/
  get` included)
- [x] `.gitignore`: keep `/dist/` and `/build-info.json`; add `/npm/binaries/`
- [x] verify: `make test`, `make vet`, `make release-tools` pass;
      `make release-check` and `make release-snapshot` become runnable in
      task 3 - must pass before task 2

### Task 2: Trim `tools/releasecheck` to the `metadata` subcommand

**Files:**

- Modify: `tools/releasecheck/main.go`, `tools/releasecheck/main_test.go`
- Modify: `tools/releasecheck/metadata.go`, `tools/releasecheck/metadata_test.go`
- Modify: `tools/releasecheck/cli_integration_test.go`
- Delete: `tools/releasecheck/environment.go`, `environment_test.go`,
  `artifacts.go`, `artifacts_test.go`, `artifact_manifest.go`, `archive.go`,
  `executable.go`, `path_safety.go`, `publication.go`, `publication_test.go`,
  `receipt.go`, `receipt_test.go`, `smoke.go`, `smoke_test.go`,
  `native_evidence.go`, `native_requirements.go`
- Delete: `release/environments.json`

- [x] `main.go`: only the `metadata` subcommand remains; usage string
      `releasecheck metadata`
- [x] `metadata.go`: remove the `RELEASE_ENV`/`SERVER_URL` env reads and the
      `loadEnvironmentManifest`/`checkEnvironment` calls; add a required
      `--server-url` flag validated with `endpoint.Parse(url, "prod")`;
      `Environment` is the constant `prod`; allow a relative `--output`;
      delete `stageVersionPattern`; keep `writeExclusiveFile`, moving it here
      if it lived in a deleted file
- [x] delete the listed files and `release/environments.json`; `go vet` and
      `go build ./...` confirm nothing dangles
- [x] `metadata_test.go`: success cases for release and snapshot versions with
      a relative and an absolute `--output`; error cases for a stage-style
      version, an `http://` server URL, an empty `--server-url`, and an
      existing output file
- [x] `cli_integration_test.go` and `main_test.go`: keep only the `metadata`
      paths and an unknown-subcommand error case
- [x] run `make test` and `make vet` - must pass before task 3

### Task 3: Rewrite `.goreleaser.yaml` for prod-only multi-channel publishing

**Files:**

- Modify: `.goreleaser.yaml`

- [x] `env`: replace `RELEASE_ENV`/`SERVER_URL` with
      `PROD_SERVER_URL=https://app.hookspot.dev/`; keep `GOTOOLCHAIN=local`,
      `GOFLAGS=-mod=readonly`; remove `dist: /out/artifacts`
- [x] `before.hooks`: `go mod download`, `go test ./...`, the
      `releasecheck metadata` invocation from Technical Details
- [x] build `hookspot`: binary `hookspot`, six targets as today, ldflags with
      `serverURL={{ .Env.PROD_SERVER_URL }}` and `buildEnvironment=prod`;
      `hooks.post` as in Technical Details (`sh -c`)
- [x] archive `cli`: name `hookspot_{{ .Version }}_{{ .Os }}_{{ .Arch }}`,
      same bundled files; checksum `hookspot_{{ .Version }}_checksums.txt`
- [x] `release`: enabled, `github.owner/name` `bgr11n/hookspot-cli`,
      `prerelease: auto`, `replace_existing_artifacts: true`,
      `mode: keep-existing`; `changelog.disable: true` stays
- [x] add the `brews` section from Technical Details
- [x] verify: `make release-check` passes (exit 2 tolerated);
      `make release-snapshot` produces `dist/` with 6 archives + checksums and
      `npm/binaries/` with 6 binaries (mode 0755 on Unix); `tar -tzf` one
      archive shows `hookspot`, `README.md`, `INSTALL.md`,
      `THIRD_PARTY_NOTICES.txt`, `build-info.json`; the extracted darwin/linux
      binary's `version --json` reports `environment=prod`,
      `build_kind=snapshot`, `server_url=https://app.hookspot.dev`; a second
      `make release-snapshot` succeeds (rerun safety); a dirty tree is refused
- [x] run `make test` - must pass before task 4

### Task 4: Collapse build environments to `dev` and `prod` and fix the upgrade check

**Files:**

- Modify: `cmd/build_info.go`, `cmd/root.go`, `cmd/errors.go`, `cmd/listen.go`,
  `cmd/project.go`, `cmd/version.go`
- Modify: `internal/endpoint/endpoint.go`, `internal/config/store.go`
- Modify: `cmd/config_test.go`, `cmd/version_test.go`, `cmd/errors_test.go`,
  `internal/endpoint/endpoint_test.go`, `internal/config/store_test.go`

- [ ] `cmd/build_info.go`: `networkEndpoint` handles `dev` and `prod` only;
      the default branch keeps rejecting unknown environments
- [ ] `cmd/root.go`: delete `executableName` and the `rootCmd.Use =
  executableName()` assignment (`Use` is already `hookspot`); replace the
      remaining callers in `cmd/root.go`, `cmd/errors.go`, `cmd/listen.go`,
      `cmd/project.go` with the literal `hookspot`
- [ ] `internal/endpoint/endpoint.go` and `internal/config/store.go`: accept
      `dev|prod` only
- [ ] `cmd/version.go`: point `latestVersion` at `repos/bgr11n/hookspot-cli`
- [ ] tests: remove stage cases; add cases asserting `stage` is rejected by
      `endpoint.Parse`, `validEnvironment`, and `networkEndpoint`; keep
      dev/prod success cases; assert the upgrade-check request path; rename
      the `-stage.N` semver fixtures in `cmd/version_test.go` to `-rc.N`
- [ ] run `make test`, `make vet`, and `make release-snapshot` - must pass
      before task 5

### Task 5: Create the npm package skeleton and launcher

**Files:**

- Create: `npm/package.json`, `npm/bin/hookspot.js`,
  `npm/test/launcher.test.js`, `npm/README.md`
- Modify: `Makefile`

- [ ] `npm/package.json`: name `hookspot`, version `0.0.0`, `bin`
      `{hookspot: bin/hookspot.js}`, `files` `[bin/, binaries/]`, `engines.node
  > =18`, repository/bugs/homepage pointing at `bgr11n/hookspot-cli`,
    `license`set per Post-Completion decision (default`UNLICENSED`until a
    LICENSE file exists), no`scripts`, no dependencies
- [ ] `npm/bin/hookspot.js`: shebang; `resolveBinary(platform, arch, root)`
      returns the path or `null`; when run as main, `spawnSync` the binary
      with inherited stdio and exit with its status, or print the
      unsupported-platform message and exit 1
- [ ] `npm/test/launcher.test.js` (`node --test`): success cases for all six
      supported platform/arch pairs including `win32/x64 → windows-amd64/
  hookspot.exe`; error cases for `linux/ia32` and `freebsd/x64`; a spawn
      test that runs the launcher against a stub executable placed in a temp
      `binaries/` tree and checks exit-code passthrough for 0 and 3
- [ ] `Makefile`: add `npm-test` running `node --test npm/test/` (host Node;
      not part of the Docker toolchain)
- [ ] verify: `make release-snapshot && cd npm && npm pack --dry-run` lists
      `bin/hookspot.js` and six binaries and no tests
- [ ] run `make test` and `make npm-test` - must pass before task 6

### Task 6: Add the CI workflow for branches and pull requests

**Files:**

- Create: `.github/workflows/ci.yml`

- [ ] trigger on `push` to `main` and `pull_request`; `permissions: contents:
  read`; single ubuntu job; pin `actions/*` to major versions
- [ ] steps: checkout with `fetch-depth: 0`; `make test`; `make vet`;
      `make release-tools`; `make release-check`; `make release-snapshot`;
      `actions/setup-node@v4` Node 22 then `make npm-test`; `cd npm && npm
  pack --dry-run`
- [ ] no dependency caching: the `hookspot-gomod` and `hookspot-gocache`
      Docker volumes do not survive between runners; add a comment saying so
- [ ] verify: push the branch and confirm the workflow is green via `gh run
  view`; fix and re-push until green
- [ ] run `make test` - must pass before task 7

### Task 7: Add the release workflow (GitHub Release, Homebrew, npm)

**Files:**

- Create: `.github/workflows/release.yml`

- [ ] trigger on `push.tags: ['v*']`; top-level `permissions: contents: read`
- [ ] job `release` (ubuntu, `permissions: contents: write, id-token: write`):
      checkout `fetch-depth: 0`; `make release-tools`; `make release-publish`
      with `GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}` and `HOMEBREW_TAP_TOKEN:
  ${{ secrets.HOMEBREW_TAP_TOKEN }}`
- [ ] same job, after the build: `actions/setup-node@v4` Node 22 with
      `registry-url: https://registry.npmjs.org`; in `npm/`: `npm version
  "${GITHUB_REF_NAME#v}" --no-git-tag-version`; dist-tag `latest`, or
      `next` when the version contains `-`; `npm publish --provenance
  --access public --tag "$NPM_TAG"` with `NODE_AUTH_TOKEN: ${{
  secrets.NPM_TOKEN }}`
- [ ] verify: `actionlint` (or `gh workflow view` after push) reports no
      errors; the workflow cannot be exercised without a tag, so the
      end-to-end check is the first release (Post-Completion)
- [ ] run `make test` - must pass before task 8

### Task 8: Add the post-release smoke matrix

**Files:**

- Modify: `.github/workflows/release.yml`
- Create: `scripts/smoke.sh`

- [ ] `scripts/smoke.sh VERSION`: detect OS/arch, `gh release download
  "v$VERSION" --pattern` for the matching archive and the checksum file,
      verify SHA-256 with `sha256sum -c` (`shasum -a 256` on macOS), extract
      with `tar -xf` (handles `zip` too), run `hookspot version --json`, assert
      `version`, `build_kind=release`, `environment=prod`. One script for all
      three runners via `shell: bash`
- [ ] job `smoke` (needs `release`; matrix `ubuntu-latest`, `macos-latest`,
      `windows-latest`; `permissions: contents: read`): run `scripts/smoke.sh`;
      `npm install -g hookspot@$VERSION` with up to 5 retries 30s apart for
      registry propagation, then `hookspot version --json`; on macOS
      additionally `brew install bgr11n/hookspot/hookspot` then `hookspot
  version --json`, skipped for pre-release versions
- [ ] tests: `shellcheck scripts/smoke.sh` clean; a negative run with a
      deliberately corrupted checksum file (via a `CHECKSUMS_FILE` override
      used only by the test) exits non-zero; both recorded in the task notes
- [ ] run `make test` - must pass before task 9

### Task 9: Rewrite user and operator documentation

**Files:**

- Modify: `README.md`, `docs/releases/INSTALL.md`, `docs/releases/RUNBOOK.md`

- [ ] `README.md`: remove every stage/`hookspot-stage`/`environments.json`
      mention; Install section lists npm, Homebrew, and GitHub archive in that
      order; replace "Release operators" with a short "Releasing" section
      (tag, push, watch the workflow, local snapshot for testing)
- [ ] `docs/releases/INSTALL.md`: remove stage; new archive names; add npm and
      Homebrew sections; keep the checksum verification block for archives
- [ ] `docs/releases/RUNBOOK.md`: rewrite to the new flow: version choice,
      `git tag -a vX.Y.Z -m ... && git push origin vX.Y.Z`, what each job does,
      required secrets and where they live, how to re-run a failed job (assets
      are replaced, formula push and npm publish are idempotent per version),
      what a red smoke job means and how to yank (npm `deprecate`, revert the
      tap formula commit, mark the GitHub release as pre-release or delete it)
- [ ] grep docs for `stage`, `RELEASE_ENV`, `release.sh`, `receipt`,
      `native`, `clean checkout` and remove leftovers
- [ ] run `make test` - must pass before task 10

### Task 10: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented: GitHub archives,
      npm fat package, Homebrew formula, tag-triggered workflow, stage fully
      removed, local unpublished snapshot works
- [ ] `grep -ri stage` across the repo (excluding `dist/`, `tmp/`,
      `.worktrees/`, `docs/superpowers/`) returns nothing release-related
- [ ] verify edge cases: pre-release tag path (`prerelease: auto`,
      `skip_upload: auto`, npm `next`), unsupported platform message in the
      launcher, missing tokens in `make release-publish`, dirty-tree refusal,
      rerun of `make release-snapshot`
- [ ] run full test suite: `make test && make vet && make npm-test`
- [ ] run integration: `make release-check && make release-snapshot`
- [ ] `ci.yml` green on the branch

### Task 11: [Final] Update documentation

- [ ] update README.md if anything changed during implementation
- [ ] no CLAUDE.md exists in this repo; skip unless one is added
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

_Items requiring manual intervention or external systems - no checkboxes, informational only_

**Prerequisites for the first real release**

- The repository is already public, which Homebrew asset downloads and the
  `version` upgrade check both require. Keep it that way.
- Create the public repository `bgr11n/homebrew-hookspot` with an empty
  `Formula/` directory and a README.
- Create a fine-grained PAT scoped to `homebrew-hookspot` with Contents:
  read/write; store it as the `HOMEBREW_TAP_TOKEN` Actions secret on
  `hookspot-cli`.
- Create an npm granular access token (publish-only, 2FA bypass for CI) and
  store it as the `NPM_TOKEN` Actions secret. Confirm the name `hookspot` is
  still free right before tagging.
- Decide the license. npm and Homebrew both display it. Add a `LICENSE` file
  and set `license` in `npm/package.json` and `brews.license` accordingly, or
  keep `UNLICENSED`.
- Delete or leave the old `stage_*` and `v0.0.0-stage.1` tags and the
  existing pre-release. They do not affect `releases/latest`, but they clutter
  the release list that the docs now link to.

**First release**

- Tag `v0.1.0` (or the chosen first version) on `main` and push it.
- Watch `gh run watch`; the smoke matrix is the acceptance test for all three
  channels. If it fails, follow the yank section of the runbook.
- Manually confirm on a clean macOS machine: `brew install
bgr11n/hookspot/hookspot`, `npm install -g hookspot`, and the INSTALL.md
  archive flow all yield the same `version --json` output. Confirm Gatekeeper
  does not block the brew-installed binary.

**After the first release**

- On npmjs.com, enable Trusted Publishing for `hookspot` pointing at
  `bgr11n/hookspot-cli` / `release.yml`, then delete the `NPM_TOKEN` secret and
  the `NODE_AUTH_TOKEN` line from the workflow.

**Security considerations**

- The release job holds `contents: write`; keep `release.yml` free of
  third-party actions beyond `actions/*` and pin action versions.
- The Docker image digests in `release/toolchain.env` are the toolchain lock;
  bump GoReleaser and Go there deliberately, never through floating tags.
- If GoReleaser removes `brews` in a future major, switch to `homebrew_casks`
  only after the binary is signed and notarized, or add the quarantine-removal
  hook consciously.
