# Release runbook

A pushed `v*` tag publishes a release to GitHub Releases, Homebrew, and npm
through `.github/workflows/release.yml`. Nothing is published from a local
machine.

## Choose a version

Versions are semantic: `vX.Y.Z`. Tag on `main` after CI is green for the
commit. A pre-release tag such as `v1.2.3-rc.1` takes a reduced path: the
GitHub Release is marked as a pre-release (so `releases/latest` and the CLI's
upgrade check ignore it), no Homebrew formula is pushed, and npm publishes
under the `next` dist-tag instead of `latest`.

Check that the version is unused on every channel before tagging:

```sh
gh release list --repo hookspot/hookspot-cli
npm view hookspot versions
```

## Tag and push

```sh
git switch main && git pull
git tag -a v1.2.3 -m "v1.2.3"
git push origin v1.2.3
gh run watch
```

## What the workflow does

`release` (ubuntu, `contents: write`, `id-token: write`):

1. `make release-tools` builds the locked GoReleaser-on-pinned-Go image from
   `Dockerfile.release` (digests in `release/toolchain.env`).
2. `make release-publish` runs `goreleaser release --clean` in that image. The
   before hooks run `go mod download`, `go test ./...`, and
   `releasecheck metadata`, which writes `build-info.json`. GoReleaser builds
   the six binaries (`serverURL` and `buildEnvironment=prod` from
   `.goreleaser.yaml`), copies each into `npm/binaries/<os>-<arch>/`, packs
   `hookspot_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) plus
   `hookspot_<version>_checksums.txt`, creates the GitHub Release with those
   seven assets, and pushes `Formula/hookspot.rb` to
   `hookspot/homebrew-hookspot`.
3. `npm version <version>` in `npm/`, then `npm publish --provenance` of the
   package bundling the binaries from step 2.

`smoke` (needs `release`; ubuntu, macOS, and Windows; `contents: read`):
`scripts/smoke.sh <version>` downloads the runner's archive from the release,
verifies its checksum, and asserts `hookspot version --json` reports the
version as a prod release build; then `npm install -g hookspot@<version>`
(retried for registry propagation) and the same assertion; on macOS also
`brew install hookspot/hookspot/hookspot` and the same assertion, skipped for
pre-release tags. This matrix is the acceptance test for the release.

## Secrets

All three are Actions secrets on `hookspot/hookspot-cli` (Settings, Secrets and
variables, Actions; or `gh secret set NAME --repo hookspot/hookspot-cli`):

- `GITHUB_TOKEN`: provided by Actions; creates the GitHub Release and uploads
  the assets.
- `HOMEBREW_TAP_TOKEN`: a fine-grained personal access token scoped to
  `hookspot/homebrew-hookspot` with Contents read/write; pushes the formula.
- `NPM_TOKEN`: an npm granular access token with publish permission for
  `hookspot` and 2FA bypass for CI; used as `NODE_AUTH_TOKEN`.

`make release-publish` refuses to start without `GITHUB_TOKEN` and
`HOMEBREW_TAP_TOKEN`; a missing `NPM_TOKEN` fails only at the npm step, after
the GitHub Release and formula are already published.

## Re-run a failed job

Use "Re-run failed jobs" on the workflow run (`gh run rerun <id> --failed`).
The workflow runs from the tag's commit, so a fix that needs a code or
workflow change means a new patch version; never move a tag.

- `release` failed before or during GoReleaser: re-running is safe. The
  release is created if missing, existing assets are replaced
  (`replace_existing_artifacts: true`, `mode: keep-existing`), and the formula
  is written again with the same content.
- `release` failed at the npm step: re-running repeats the GoReleaser step as
  above, then publishes. If the version had already reached the registry, npm
  refuses to publish it again (403); confirm with
  `npm view hookspot@<version>` and treat the release as published.
- `smoke` failed: re-running repeats only the smoke matrix. A transient
  failure (registry propagation, a runner outage) passes on re-run. A
  reproducible failure means the release is broken; yank it.

## Yank a broken release

A red smoke job, or a bug found after release, is handled per channel. Keep
the tag; publish a fixed patch version afterwards.

npm cannot unpublish a version most users can already have, so deprecate it;
`npm install` then warns and `latest` moves back to the previous version:

```sh
npm deprecate hookspot@1.2.3 "Broken release, use 1.2.4"
npm dist-tag add hookspot@1.2.2 latest
```

Homebrew: revert the formula commit in `hookspot/homebrew-hookspot` so
`brew install` and `brew upgrade` resolve to the previous version again:

```sh
git -C homebrew-hookspot revert HEAD && git -C homebrew-hookspot push
```

GitHub Release: mark it as a pre-release so `releases/latest` and the CLI's
upgrade check skip it, or delete it outright when the assets must not be
downloadable at all:

```sh
gh release edit v1.2.3 --repo hookspot/hookspot-cli --prerelease
# or
gh release delete v1.2.3 --repo hookspot/hookspot-cli --yes
```

## Local snapshot

`make release-snapshot` runs the same GoReleaser pipeline unpublished: six
archives and the checksum file in `dist/`, the binaries in `npm/binaries/`,
version `0.0.0-snapshot.<sha>`, `build_kind=snapshot`. It requires a clean
working tree because the build embeds VCS metadata, and it clears
`build-info.json` and `npm/binaries/` before each run. `make release-check`
validates `.goreleaser.yaml`; exit code 2 is GoReleaser's deprecation notice
for the `brews` section, which is used deliberately (a formula does not
quarantine the unsigned binary and works on Linux), and is treated as success.
