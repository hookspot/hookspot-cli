# Release runbook

A pushed `v*` tag publishes a release to GitHub Releases, Homebrew, Docker
Hub, and npm through `.github/workflows/release.yml`. Nothing is published from a local
machine.

## Choose a version

Versions are semantic: `vX.Y.Z`. Tag on `main` after CI is green for the
commit. A pre-release tag such as `v1.2.3-rc.1` takes a reduced path: the
GitHub Release is marked as a pre-release (so `releases/latest` and the CLI's
upgrade check ignore it), no Homebrew formula is pushed, the Docker image gets
its version tag but not `latest`, and npm publishes under the `next` dist-tag
instead of `latest`.

Check that the version is unused on every channel before tagging:

```sh
gh release list --repo hookspot/hookspot-cli
npm view @hookspot/cli versions
```

## Prerequisites

Before the first release, and worth re-checking when a release fails early:

- `hookspot/homebrew-hookspot` exists, is public, and has a `Formula/`
  directory; GoReleaser pushes `Formula/hookspot-cli.rb` into it.
- The Docker Hub repository `hookspot/cli` exists and is public.
- The `HOMEBREW_TAP_TOKEN`, `DOCKERHUB_USERNAME`, and `DOCKERHUB_TOKEN`
  Actions secrets are set (see Secrets below).
- The npm package `@hookspot/cli` exists and trusts `release.yml` (see Secrets below).

## Tag and push

```sh
git switch main && git pull
git tag -a v1.2.3 -m "v1.2.3"
git push origin v1.2.3
gh run watch
```

## What the workflow does

`release` (ubuntu, `contents: write`, `id-token: write`):

1. Checks that the Docker Hub secrets are set, then runs
   `make test`, so a missing secret or a failing test stops the job before
   anything is published.
2. `make release-tools` builds the locked GoReleaser-on-pinned-Go image from
   `Dockerfile.release` (digests in `release/toolchain.env`).
3. `make release-publish` runs `goreleaser release --clean` in that image. The
   before hooks clear `npm/binaries/`, run `go mod download`, and run
   `releasecheck metadata`, which writes `build-info.json`. GoReleaser builds
   the six binaries (`serverURL` from `.goreleaser.yaml`), copies each into
   `npm/binaries/<os>-<arch>/`, packs
   `hookspot_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) plus
   `hookspot_<version>_checksums.txt`, creates the GitHub Release with those
   seven assets, and pushes `Formula/hookspot-cli.rb` to
   `hookspot/homebrew-hookspot`.
4. `make release-image-publish` builds the `release` stage of `Dockerfile` for
   `linux/amd64` and `linux/arm64`, copying the Linux binaries from step 3, and
   pushes `hookspot/cli:<version>`, plus `latest` for a non-pre-release
   version. It runs before npm because image tags can be pushed again and npm
   versions cannot.
5. `npm version <version>` in `npm/`, then `npm publish --provenance` of the
   package bundling the binaries from step 3.

`smoke` (needs `release`; ubuntu, macOS, and Windows; `contents: read`):
`scripts/smoke.sh <version>` downloads the runner's archive from the release,
verifies its checksum, and asserts `hookspot version --json` reports the
version as a release build; then `npm install -g @hookspot/cli@<version>`
(retried for registry propagation) and the same assertion; on ubuntu also
`docker run --rm hookspot/cli:<version>` and the same assertion; on
macOS also `brew install hookspot/hookspot/hookspot-cli` and the same
assertion, skipped for pre-release tags. This matrix is the acceptance test for the release.

## Secrets

`HOMEBREW_TAP_TOKEN`, `DOCKERHUB_USERNAME`, and `DOCKERHUB_TOKEN` are
Actions secrets on `hookspot/hookspot-cli` (Settings, Secrets and variables,
Actions; or `gh secret set NAME --repo hookspot/hookspot-cli`); `GITHUB_TOKEN`
is the workflow's built-in token.

- `GITHUB_TOKEN`: creates the GitHub Release and uploads the assets.
- `HOMEBREW_TAP_TOKEN`: a fine-grained personal access token scoped to
  `hookspot/homebrew-hookspot` with Contents read/write; pushes the formula.
- `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN`: a Docker Hub user with push
  access to `hookspot/cli`, and a personal access token of that user
  with Read & Write scope; pushes the image.

npm takes no secret: the workflow publishes through npm trusted publishing
(GitHub OIDC), since npm stops accepting direct publishes from tokens in
January 2027. npm trusts only an existing package, so the first version is
published by hand; the package's trusted publisher (npmjs.com, `@hookspot/cli`,
Settings) is GitHub Actions, `hookspot/hookspot-cli`, workflow `release.yml`.

The workflow checks the Docker Hub secrets first, and `make release-publish` refuses to
start without `GITHUB_TOKEN` and `HOMEBREW_TAP_TOKEN`, so a missing secret
stops the job before anything is published.

## Re-run a failed job

Use "Re-run failed jobs" on the workflow run (`gh run rerun <id> --failed`).
The workflow runs from the tag's commit, so a fix that needs a code or
workflow change means a new patch version; never move a tag.

- `release` failed before or during GoReleaser: re-running is safe. The
  release is created if missing, existing assets are replaced
  (`replace_existing_artifacts: true`, `mode: keep-existing`), and the formula
  is written again with the same content.
- `release` failed at the Docker or npm step: re-running repeats the
  GoReleaser step as above, pushes the same image tags again, then publishes,
  skipping npm when the version already reached the registry, so `smoke` runs
  afterwards.
- `smoke` failed: re-running repeats only the smoke matrix. A transient
  failure (registry propagation, a runner outage) passes on re-run. A
  reproducible failure means the release is broken; yank it.

## Yank a broken release

A red smoke job, or a bug found after release, is handled per channel. Keep
the tag; publish a fixed patch version afterwards.

npm cannot unpublish a version most users can already have, so deprecate it;
`npm install` then warns and `latest` moves back to the previous version:

```sh
npm deprecate @hookspot/cli@1.2.3 "Broken release, use 1.2.4"
npm dist-tag add @hookspot/cli@1.2.2 latest
```

Homebrew: revert the formula commit in `hookspot/homebrew-hookspot` so
`brew install` and `brew upgrade` resolve to the previous version again:

```sh
git -C homebrew-hookspot revert HEAD && git -C homebrew-hookspot push
```

Docker Hub: point `latest` back at the previous version. The broken version's
own tag stays, like the npm version:

```sh
docker buildx imagetools create -t hookspot/cli:latest hookspot/cli:1.2.2
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
working tree because the build embeds VCS metadata; a before hook clears
`npm/binaries/` inside the container, so the run is repeatable on Linux hosts
where the bind mount leaves those files root-owned. The snapshot formula in
`dist/homebrew/` names the newest reachable tag in its download URLs
(`v0.0.0-stage.1` today); a real `v*` tag push fills in the right one.

`make release-image` then builds the image from those binaries for both
platforms without pushing. It needs a Buildx builder that supports
multi-platform builds: a `docker-container` builder, or Docker's containerd
image store.

`make release-check` validates `.goreleaser.yaml`; exit code 2 is GoReleaser's
deprecation notice for the `brews` section, which is used deliberately (a
formula does not quarantine the unsigned binary and works on Linux), and is
treated as success. `brews` is past GoReleaser's soft-deprecation phase, so a
GoReleaser bump in `release/toolchain.env` may land on a version that has
removed it; switching to `homebrew_casks` then means signing the binary or
accepting quarantine.
