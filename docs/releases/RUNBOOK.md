# Release runbook

Releases are built by GoReleaser inside the locked Docker image from
`Dockerfile.release` (tool digests in `release/toolchain.env`). Publication
runs in GitHub Actions on `v*` tags; local machines only build unpublished
snapshots:

```sh
make release-tools
make release-check
make release-snapshot
```

`release-snapshot` refuses a dirty checkout because the build embeds VCS
metadata, and clears `build-info.json` and `npm/binaries/` before each run.
