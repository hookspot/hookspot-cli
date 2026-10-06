## Development

`mise trust && mise install` once per checkout or worktree puts `mise.toml`'s Go and golangci-lint on the host. Where the shell's mise doesn't follow `cd`, run `mise exec -- make …`. `DOCKER=1` runs the Go targets in the pinned images instead.

- `make check` is the gate: everything CI's `ci` job runs. Commit first; its release snapshot needs a clean tree, and its image a multi-platform Buildx builder (`BUILDX_BUILDER=<a docker-container builder>`).
- `make test ARGS='-run ^TestX$ ./internal/tui'`; `make lint` (gofmt, golangci-lint for darwin, linux and windows); `make fmt`.
- Goldens (`testdata/*.golden`): `make golden`, or `make golden ARGS='-run TestX ./internal/tui'`, then review the diff.
- E2E build: `make e2e-build VERSION=1.2.0 SERVER_URL=https://hookspot.localhost:4443 UPDATE_API_URL=http://127.0.0.1:8080` writes `tmp/e2e/hookspot`. Its update check asks `$UPDATE_API_URL/repos/hookspot/hookspot-cli/releases/latest`.

## Agent skills

### Issue tracker

GitHub Issues on `hookspot/hookspot-cli`, via `gh`. See `docs/agents/issue-tracker.md`.

### Domain docs

Single-context: root `CONTEXT.md` and `docs/adr/`. See `docs/agents/domain.md`.
