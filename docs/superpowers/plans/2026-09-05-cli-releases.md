# Reliable stage and production CLI releases — Implementation Plan

> Implementation was authorized on 2026-09-06. The named Superset Codex worker implements supervised batches with independent review checkpoints. The historical review below remains evidence, not a claim that implementation checks have passed. Publication is not authorized by implementation approval.

**Date:** 2026-09-05; implementation corrections 2026-09-06. **Status:** implementation in progress on `feat/cli-releases`.

**Goal:** Build, verify, package, and eventually publish environment-specific CLI releases from a Mac or Linux workstation using Docker, with the same entry points reusable in CI.

**Architecture:** One GoReleaser OSS configuration builds six targets in a dedicated container. A shared script validates environment/source/tag policy and publishes the exact verified archives through a draft GitHub release using containerized GitHub CLI. Stage and prod have different embedded endpoints, executable names, and configuration namespaces.

**Tech stack:** Go 1.26.8 proposed; GoReleaser OSS v2.17.1; Docker; Git; portable Bash/Make; GitHub CLI v2.100.0; existing Cobra/Viper and Gorilla WebSocket.

**Spec:** The user's release-engineering request in this conversation, dated 2026-09-05. This document captures its implementation contract; older repository designs are contextual references, not the governing release specification.

**Constraints:** All Go compilation, tests, and Go analysis tools run in Docker. Native execution of an already-built binary is allowed. Do not add a runtime backend selector or embed authentication credentials. Initial delivery requires no CI, global tool installation, registry publication, Homebrew, npm, or paid GoReleaser feature. This document is the only repository change made during the review.

## Global constraints

- The original planning-only restriction is superseded by the user's 2026-09-06 implementation request. Local implementation, tests, and review commits are authorized; publication remains a separate action.
- “Build must happen in its own docker container not on the local machine.”
- “Do not embed authentication credentials in binaries, archives, or build logs.”
- “A prerelease tag alone does not configure a staging backend.”
- “The initial implementation must work locally without CI configuration.”
- Release environments are stage/prod; each covers darwin/linux/windows × amd64/arm64, with Linux ARM64 explicitly assumed.

## 1. Current-state assessment

### Implementation decisions (2026-09-06)

- User experience is the primary constraint: fast startup, simple commands, minimal settings, useful errors, and no routine debug output. Keep detailed build diagnostics in `version --json` and operator documentation; human version output should use short readable labels.
- No-echo login uses synchronous caller-owned terminal setup/restoration and a bounded, guarded byte-only reader when an interruptible input handle is unavailable. Isolated native macOS/Linux probes reproduced that closing a duplicated descriptor does not reliably cancel the pinned `term.ReadPassword`, and starting it in a goroutine can disable echo after cancellation cleanup. Do not use that pattern. A blocked fallback reader is explicitly process-lifetime, never spawned repeatedly, and restoration errors must remain visible even when cancellation would normally exit successfully.
- Preserve the requested existing Superset worker as the sole implementation owner. Review each batch for requirements and readability before continuing. Fix concrete adjacent defects when discovered; avoid speculative frameworks or unused future APIs.
- Start from local `main` commit `f8e35de06da945515c2f6ce9373b32bf9f1107c9`. Superset initially selected an older remote branch; the isolated feature branch was fast-forwarded before implementation.
- Empty approved endpoint values block real environment release operations, not implementation or fixture builds. Fixture repositories may commit clearly fictional manifests; production scripts get no test bypass.
- Build-time version variables must be verified against official tools/images. Do not treat the review's proposed versions or hashes as proven merely because they appear below.
- Bootstrap tool versions, multi-architecture image references with digests, and both GitHub CLI archive hashes live together in `release/toolchain.env`. Treat it as strictly parsed key/value data, never source/eval untrusted values or interpret them as Make syntax. Task 7 must remove the unchecked Make include from release entry paths before a script parser can protect them. Omit the proposed `toolchain.lock.json`: parsing it before building the tool image would introduce an undocumented host parser or brittle JSON parsing. Generated receipts record the resolved lock data.
- Move restore of the CLI image's full metadata and config-volume contract across Tasks 1, 3, and 4 as needed so every commit compiles. Task 1 may add linker flags for symbols introduced in Task 3 only together with those symbols.
- Implement `Store` loading and creation as distinct command intents: an explicitly missing path is an error for ordinary commands, while login/migration may create it. Migration must also inspect a destination without consuming its environment overrides.
- Resolve only command-relevant settings and the winning precedence tier. Login/project-list/logout do not fail on an unrelated incomplete slug pair; an explicit project UID suppresses slug pairs. An unused legacy assertion does not override scoped values or explicit flags. Migration validates its exact source and publishes one complete record with atomic no-overwrite, including if the destination appears during validation.
- Private config writes create restrictive modes/DACLs and verify access before writing secrets, never repairing an exposed inode then adding credentials. Native Darwin probes showed inherited read ACLs despite mode `0600`; inspect existing files, directory mutation ACLs and the opened empty temp with a small pure-Go platform helper. Reject/delete unsafe empty temps without ever writing secrets; deny-only ACLs remain supported. Validate effective ancestry/ownership before reads and directory creation, not only the immediate parent; a Linux probe demonstrated ancestor-owner displacement. Preserve ordinary `0755` user parents, trusted sticky-temp paths and safe resolved aliases without changing user permissions. Windows creates protected-DACL temp files and uses replace/no-replace moves according to intent. Native Windows behavior remains a separate gate.
- Local backend source at revision `d87b8b1e9516d6bbf7aac09be0202c1d0db00713` was subsequently found. It emits unpadded Base64 delivery bodies, which the current Go `[]byte` JSON decoder rejects for ordinary body lengths; Task 5 must accept padded and unpadded Base64 with backend-shaped fixtures. It also confirms that project list/detail serialize the same fields: Task 2 removes the redundant detail request when selecting by slugs. This is source evidence, not a claim about deployed backend versions or infrastructure payload limits.
- No public payload-limit settings are added. Confirm protocol limits against available backend source; if unavailable, record bounded defensive defaults as compatibility assumptions with tests and release notes, not as a verified backend contract.
- Maintain immutable build receipts and append separate publication/native-evidence records. This resolves the contradictory requirements to preserve the receipt while adding mutable release IDs and notes state.
- Keep first-support native evidence separate from per-release evidence. Record unavailable hosts honestly; implementation can finish with explicit platform-validation limitations, while publication gates remain enforced according to the documented policy.


- Publication verification runs retained helpers without credentials. Credentialed containers execute only the pinned image's trusted GitHub CLI/hash tools and fixed validated shell, with seven assets mounted read-only; they never run build-produced helper executables. Validate the actual image ID before use.
- Verify the selected retained helper's fixed name and digest against the receipt using trusted current tooling before executing it. A real mutation probe replaced only that helper with an executable returning success and bypassed all self-verification. Keep this as a bounded integrity check, not a claim of authenticity for an unsigned, wholly replaced bundle; no host JSON parser or general attestation system is needed.
- A tiny standard-library checker baked into the approved release-tools image performs that pre-execution check without compiling current project source or needing module caches during offline verification. Bind its fixed source digest as an image input. For retained verification, approve the invoking installation's current verifier recipe/checker separately from the original bundle's builder recipe/image, requiring supported lock compatibility. Preserve existing schema-v2 receipts/five build controls. Selected-source snapshot/build controls remain authoritative.
- Record both annotated/lightweight tag object ID and peeled commit. A tag object mismatch stops publication/recovery even if the peeled commits agree. Build the selected source using its own release-control files and verified toolchain identity; do not silently substitute the invoking checkout's configuration.
- Implement native-evidence schema/validation and smoke scripts from Task 9 before Task 8 consumes them; final Task 9 completes documentation and wider integration. Native unavailability is recorded but never counted as a passing publication gate.
- Native evidence distinguishes tested target, collector process and host architecture, with `native|emulated|unknown` classification and provenance. In-container `uname` can describe the emulated target; the Docker launcher records daemon architecture and selected platform. Unknown evidence never silently becomes native. Record exact checks performed: version/help alone cannot certify credential permissions or terminal/signal behavior.
- Retained failed smoke attempts do not prevent a later valid successful retry from satisfying unchanged requirements; incomplete collection remains diagnostic-only, and unsuccessful observations never count as passes. Reject malformed or mixed identity inputs and bind the exact chosen successful evidence for publication. Routine baselines must match the current repository and environment, retain their own identities, and never substitute for current-binary observations.
- First support requires version/help on all six targets, plus config-private/password-input/signal-cancel on both Windows targets, whose native behavior remains unverified. The real `network_release_v1` procedure is required on the receipt's original native Linux builder target for first support and remains mandatory for routine releases. The Task 9 isolated `.invalid` TLS/Phoenix fixture exercises exact binaries from synthetic retained bundles and labels only test-local `local-tls-phoenix-v1` records; those records are never publication inputs and never satisfy `network_release_v1`. This is not a six-host network-test requirement and does not replace real staged-backend acceptance.
- Retain `-trimpath`: pinned Go 1.26.8 intentionally omits linker flags from build information when it is enabled. Static inspection verifies the available Go/VCS/target fields, not custom `-X` globals. Public metadata is checked against approved build inputs; exact-binary `version --json` observes custom runtime fields on compatible hosts. Record unobserved targets explicitly. This does not add a new six-host execution requirement to every routine release or turn a sidecar/receipt into a runtime pass.
- Production requires an explicit stage-acceptance record tying a successful operator staging smoke to repository, tag object/commit, base version, release ID and asset digest set. A completed upload alone cannot prove the backend was tested. Unknown stable/latest version formats stop for operator review rather than guessing ordering.
- Bind draft ownership to a deterministic bundle ID from immutable receipt, notes hash, tag object and eligibility_inputs_sha256. Compute that eligibility fingerprint from a fixed, canonically ordered list of relative path/digest records covering exact native requirements, selected evidence, required routine-baseline inputs and production stage-acceptance bytes; reuse the same list in initial publication state and resume validation. Exclude failed/unselected diagnostics and mutable publication status/timestamps/IDs/publisher architecture. Place the bundle ID in a hidden release-body marker when creating the draft; explicit resume requires a matching marker and any already-recorded release ID. This permits lost-response/local-record recovery without accepting changed eligibility evidence. Changed inputs require restoring the original evidence, never rewriting the marker. Existing assets are downloaded and hashed before being skipped.

### Inspection baseline

Reviewed commit `f8e35de06da945515c2f6ce9373b32bf9f1107c9`, branch `main`, initially clean working tree, full Git history. The observed origin is `git@github.com:bgr11n/hookspot-cli.git`. No applicable `AGENTS.md` was found in the repository or its parent directories. Repository design documents were read as historical context and checked against implementation.

The inspection covered root configuration/build files, all production Go packages and existing tests, `go.mod`, `go.sum`, README, and the tracked design/implementation documents. No tracked CI workflows or standalone release scripts currently exist. No root `LICENSE` exists. Ignored, pre-existing `dist/` contents were not used as proof of the current source and were left untouched.

Local tags observed were `stage_065aa29`, `stage_33`, `stage_35_35`, and `stage_36_80898dc`. No current-format SemVer tag was present locally. Remote releases, repository rules, and backend contracts were not authenticated or queried. Do not infer that the local tag list is the complete published-release history.

### Verified implementation

| Area | Current evidence | Assessment and reuse |
| --- | --- | --- |
| GoReleaser | `.goreleaser.yaml:1` uses configuration version 2; `:5` runs module download and tests; `:10` defines one build. | Reuse the existing build/archive structure and test gate. Schema validation and a fresh six-target snapshot succeeded. |
| Tool versions | `Makefile:1` pins Go 1.26.5, `:3` Air v1.67.3, `:4` GoReleaser v2.17.1. `go.mod:3`, `Dockerfile:3`, and `docker-compose.yml:17` also use Go 1.26.5. | The versions are aligned today but duplicated. The inspected GoReleaser image also contains Go 1.26.5; pinning the GoReleaser executable alone does not select a patched build toolchain. |
| Compilation | `.goreleaser.yaml:15` disables CGO; `:17` enables trimpath; `:21` injects `hookspot/cmd.version`; `:22` injects `hookspot/cmd.serverURL`. | Keep pure-Go cross-compilation, trimpath, and both existing linker symbols. They are intentional externally populated variables, not unused globals. |
| Targets | `.goreleaser.yaml:23` lists darwin/linux/windows; `:27` lists amd64/arm64. | All six combinations compile with CGO disabled. Keep Windows and Linux ARM64 as requested/assumed below. |
| Packages | `.goreleaser.yaml:31` creates tarballs with Windows ZIP overrides; `:47` creates checksums. | Current fresh archives contain only README and the binary. Add explicit install instructions, notices, metadata, and environment in names. |
| Stage publication | `Makefile:51` checks URL/token, stage branch, clean tree, fetched remote tip, config, and tag collision; `:67` creates a lightweight commit-count tag; `:69` pushes it; `:70` publishes. | Branch/source and collision checks are useful. Build/test execution occurs after the remote tag is pushed. There is no prod workflow or defined recovery protocol. |
| Release channel | `.goreleaser.yaml:50` sets `prerelease: auto`. | The prerelease suffix controls GitHub classification; it does not select a staging backend. There is no environment model or explicit latest policy. |
| Backend contract | `cmd/root.go:18` documents linker injection; `:28` rejects only an empty value. README `:67` explicitly rejects runtime override. | Preserve the build-time contract, add environment/URL validation and metadata. Do not reinterpret the legacy generic `SERVER_URL` as an environment selector. |
| API/WS | `internal/api/client.go:49` trims trailing slashes and sets a 10-second HTTP timeout. `cmd/listen.go:120` derives WS using string replacement and concatenation. | Reuse API timeout and typed errors. Replace inconsistent URL construction with one parsed base-URL implementation. |
| Configuration | `internal/config/config.go:23` binds shared `HOOKSPOT_*` variables; `:38` defaults to `~/.config/hookspot/config.toml`; `:55` resolves values; `:67` saves. | Stage/prod currently share credentials and selected project. Viper combines resolution and persistence, which is unsafe for environment isolation. |
| Error/retry behavior | `main.go:9`, `cmd/errors.go:66`, `cmd/listen.go:145`, and `internal/ws/client.go:24` provide one error presenter, typed session errors, bounded initial connection retries, and reconnects after a successful join. | Reuse these. Fix cancellation during join, write deadlines, and heartbeat failure propagation rather than replacing the supervisor. |
| Output/forwarding | `internal/printer/printer.go:240` and `:415` redact specified sensitive headers; printer tests cover formatting, truncation, color, and escaping. `cmd/listen.go:424` protects/deep-copies replay state. | These are substantial tested behaviors. Preserve them while fixing output errors and adding bounds to network payload handling. |
| Development Docker | `Dockerfile:16` comments out ENTRYPOINT. README `:119` uses Compose service `hookspot`, whereas `docker-compose.yml:2` defines `cli`. | Advertised runtime commands do not match the actual image/service contract. Air's separate entrypoint in `.air.toml:10` does not fix the runtime image. |

### Historical design documents

Do not implement old documents wholesale. Add concise superseded/status notes when updating documentation; keep history.

- `docs/superpowers/specs/2026-06-12-hookspot-cli-skeleton-design.md:16`, `:57`, and `:74` describe older token, runtime URL flag, config path, and environment-variable behavior. Current CLI-key authentication and build-time URL behavior supersede those descriptions. The matching implementation plan still names Go 1.23 at line 9.
- `docs/superpowers/specs/2026-06-18-human-readable-project-design.md:21` describes an older UID/query arrangement; current `cmd/listen.go:72` resolves slug pairs and joins the project's Phoenix topic.
- `docs/superpowers/specs/2026-06-19-phoenix-channels-ws-design.md:24` names `delivery_attempt.created`; current `internal/ws/client.go:20` uses `delivery`, decodes a structured payload, and sends correlated responses. Its older concurrency description also predates `connWriter`.
- `docs/superpowers/specs/2026-07-12-listen-print-mode-design.md:5` explicitly supersedes the port-flag design. Its `--print-body`/older printer API details at `:22` and `:41` also differ from the current print-limit and inspect/forward implementation. Neither document establishes an obligation to restore obsolete flags.

### Hookdeck reference: useful patterns and limits

Inspected upstream at immutable commit `a5293f167386c525b38926bc6acdcb5f4dc2602c`, including all four `.goreleaser/` configurations, release workflow, version implementation, and distribution documentation. Raw GitHub retrieval succeeded after some browser fetches failed.

Hookdeck's Linux build uses CGO-disabled amd64/arm64 binaries, while its macOS build covers both Mac architectures. Its macOS archives explicitly include documentation and notices. Those patterns fit Hookspot's existing pure-Go matrix. Separate OS configurations are useful for Hookdeck's different package managers and signing concerns; Hookspot does not yet have those requirements. [Linux configuration](https://github.com/hookdeck/hookdeck-cli/blob/a5293f167386c525b38926bc6acdcb5f4dc2602c/.goreleaser/linux.yml#L11-L38), [macOS configuration](https://github.com/hookdeck/hookdeck-cli/blob/a5293f167386c525b38926bc6acdcb5f4dc2602c/.goreleaser/mac.yml#L11-L48).

Its workflow gates release builds on acceptance tests and checks out full history. Reuse those principles. Do not copy its multiple concurrent OS publishers, older GoReleaser pin, or npm job that switches from the tag to a branch and makes a version commit before rebuilding. Hookspot should publish one source identity and one verified artifact set. [Release workflow](https://github.com/hookdeck/hookdeck-cli/blob/a5293f167386c525b38926bc6acdcb5f4dc2602c/.github/workflows/release.yml#L9-L175).

Hookdeck documents stable and prerelease installation separately and avoids updating container `latest` for beta releases. Adopt explicit channel documentation and stable-only latest selection. Its two Homebrew channels install the same binary name and can conflict; distinct Hookspot executable names avoid that. Homebrew/npm/Scoop/container publication would add independent maintenance and credentials, so leave them optional. [Distribution and release documentation](https://github.com/hookdeck/hookdeck-cli/blob/a5293f167386c525b38926bc6acdcb5f4dc2602c/README.md#L64-L140), [binary-name conflict](https://github.com/hookdeck/hookdeck-cli/blob/a5293f167386c525b38926bc6acdcb5f4dc2602c/README.md#L1279-L1288).

Hookdeck injects a version variable but its version command also performs an update check. Reuse build metadata injection, not that network dependency: Hookspot version/help must work offline and without a valid config. Its native Windows configuration also has CGO/MinGW and 386 choices that do not match this repository's tested CGO-disabled Windows ARM64 matrix. [Version implementation](https://github.com/hookdeck/hookdeck-cli/blob/a5293f167386c525b38926bc6acdcb5f4dc2602c/pkg/version/version.go#L22-L29), [version command](https://github.com/hookdeck/hookdeck-cli/blob/a5293f167386c525b38926bc6acdcb5f4dc2602c/pkg/cmd/version.go#L10-L21), [Windows configuration](https://github.com/hookdeck/hookdeck-cli/blob/a5293f167386c525b38926bc6acdcb5f4dc2602c/.goreleaser/windows.yml#L11-L23).

## 2. Assumptions and unresolved decisions

1. **Two release environments:** exactly `stage` and `prod`. `dev` is a local-development identity, never a publishable release environment. There is no silent default to prod.
2. **Required external values:** the owner supplies the canonical stage and prod server URLs, including any supported deployment prefix. No endpoint in current examples or Compose is assumed to be the production endpoint. Release commands fail until both reviewed values are configured.
3. **Linux ARM64 is an assumption.** Keep linux/amd64 and linux/arm64. Windows amd64 and arm64 remain supported because the user explicitly included Windows; no 386 target is added.
4. **Repository/branches:** initially use the observed GitHub origin, `stage` for staging and `main` for production eligibility. Confirm the intended release repository before the first publication; a different repository only changes configuration, not architecture.
5. **Promotion:** production must rebuild the same source commit as a tested staging candidate with the same base version. That commit must be reachable from `origin/main`; it need not remain the tip. Prefer a merge retaining the tested commit. Squashing/rebasing changes identity and requires staging the resulting commit again.
6. **Versions:** use manually selected SemVer versions, not commit counts. Examples using `0.1.0` below demonstrate syntax only. Inventory existing remote tags/releases before choosing the first real version.
7. **Initial macOS distribution:** separate archives without Developer ID signing are acceptable for controlled/internal use. More precisely, the tested ARM64 Go binary is ad-hoc linker-signed, without an Apple team identity or notarization. Public friction-free browser downloads may require the optional signing work before a public launch.
8. **Configuration migration:** fail closed rather than guessing which environment owns the legacy file. An explicit migration command or fresh login is acceptable. Generic legacy credential environment variables require an explicit matching environment assertion after migration.
9. **Local release coordinators:** Mac/Linux maintainers have Docker and existing Git access. One coordinated publisher operates at a time; future CI serializes publication. Building Windows binaries locally does not imply Windows is a supported host for the shell release tooling.

No architectural question needs to block this plan. Before real publication, the concrete inputs are the two approved URLs, release repository confirmation, intended first version, and disposition of the committed credential literal. Decide whether Developer ID/notarization is required for the intended audience before calling the Mac download experience production-ready.

## 3. Recommended architecture and alternatives

### One build configuration, one policy script

Create `scripts/release.sh` as the sole release-policy entry point. Make targets only delegate to it. Add a small standard-library Go helper at `tools/releasecheck/` for URL/manifest validation, metadata, artifact inspection, and verification receipts; compile/run it only in the release container. Reuse `internal/endpoint` in that helper and the CLI so endpoint semantics do not diverge.

Use GoReleaser OSS v2.17.1 to build/package. Disable its SCM publisher in the shared configuration and publish its completed archives with GitHub CLI v2.100.0, inside a separate publisher invocation. This deliberate division lets the uploader use the exact verified local bytes, without supplying a publishing token to build hooks or rebuilding after approval/verification. It also gives a small, explicit missing-assets-only recovery path.

The uploader is a shell coordinator around `gh release` and `gh api`, not a new upload client or a general release framework. Six archives and one checksum file are its entire publishable asset list. Build metadata and receipts prevent accidental mixing.

| Alternative | Fit |
| --- | --- |
| One parameterized config plus exact-artifact uploader — recommended | One matrix, one naming scheme, one set of build flags; handles safe reruns using saved files; entirely OSS. Requires a small upload/recovery script and a pinned containerized GitHub CLI. |
| One parameterized config with native GoReleaser publishing | Slightly fewer tools. A preflight snapshot is possible, followed by a real release build, but the real invocation builds again. Do not claim it publishes the already-verified snapshot. Handling partial uploads without replacing completed assets still requires policy. Reasonable if exact saved-artifact publication is deliberately relaxed later. |
| Two small stage/prod configs | Environment values can be literal, but the matrix, archive members, checksums, and hooks drift unless maintained together. Useful if future signing or distribution channels diverge materially. Not needed now. |

Do not assume OSS has a prepare/continue pipeline, shared-config include mechanism, or paid split/merge build feature. The inspected v2.17.1 executable's `release --help` has `--snapshot`, `--skip=publish`, `--config`, and `--clean`; it has no `--prepare` or continuation command for this design.

### Toolchain and container boundary

Pin the following proposed versions in `release/toolchain.env`:

```dotenv
GO_VERSION=1.26.8
GORELEASER_VERSION=v2.17.1
GH_VERSION=2.100.0
AIR_VERSION=v1.67.3
STATICCHECK_VERSION=v0.8.1
GOVULNCHECK_VERSION=v1.7.0
```

Go 1.26.5 currently has reachable standard-library vulnerability reports; all four reports observed are fixed by 1.26.6. Choose the newer available 1.26 patch, **1.26.8**, and rerun the scanner. Avoid an unrelated upgrade to Go 1.27 because it changes the minimum macOS requirement. The official release index was checked on the review date. [Go downloads](https://go.dev/dl/?mode=json&include=all), [minimum requirements](https://go.dev/wiki/MinimumRequirements).

Create `docker/release.Dockerfile`: copy `/usr/bin/goreleaser` from the pinned v2.17.1 image into the `golang:1.26.8` build environment, install the pinned Linux GitHub CLI archive appropriate to the container's amd64/arm64 architecture, and verify its published SHA-256 before extraction. Both source image references should be locked to their multi-architecture index digests after registry verification during implementation; no digest is invented here. The executable location was verified in the current ARM64 image. Inspect `go version`, `goreleaser --version`, and `gh --version` on both architectures in the resulting image.

GitHub CLI v2.100.0 published tarball hashes observed for the implementation lock file were:

```text
linux_amd64: e4d4bb4498e8d007abe545b6568926793ace1b6447da598294a610018cb164be
linux_arm64: ea4e7a581a32ccad6cc7923cb1576ac5859ba4b9a16ab22eb8f8a96e78e2e961
```

Re-verify them against the official assets when implementing. This installs tools only inside an image, never globally on the workstation. [GitHub CLI v2.100.0 release](https://github.com/cli/cli/releases/tag/v2.100.0).

Set `GOTOOLCHAIN=local` and `GOFLAGS=-mod=readonly` for release/test containers. Align `go.mod`, development image, runtime-image builder, and release-image Go version. Load the tracked version file from Make/scripts; pass its values explicitly as Docker build args and Compose interpolation inputs. Add a compatibility check for duplicated Docker ARG defaults and `go.mod`, rather than allowing automatic Go toolchain download to conceal a mismatch.

Run the container natively on Apple Silicon, Intel Macs, and Linux amd64/arm64. Pure-Go cross-compilation needs neither QEMU nor a C cross compiler. Race tests are a separate native Linux build with CGO enabled; do not apply `CGO_ENABLED=0` to the race command. Module caches can be reused, but they are performance caches, not evidence of a successful verification.

The source is a disposable, exact-ref Git checkout with full history and an independent object store. Mount that owned copy as the container's writable working directory; the user's checkout and its Git metadata are never writable build volumes. A second source copy inside the container adds I/O without an identified protection gain. The only other writable mounts are a fresh output directory and dependency/build caches. Build containers receive an explicit allowlist of nonsecret metadata, no `GITHUB_TOKEN`, no `GH_TOKEN`, and no `HOOKSPOT_*` credential variables. The publisher sees retained artifacts and required GitHub credentials, not the source tree or build hooks. Compile the releasecheck helper in Docker for Linux amd64 and arm64 into the output parent's private `tools/` directory; token-free verification uses the matching helper without source or Go compilation, and the separate credentialed publisher never executes it. These internal executables are not release assets or archive members. This also lets a retained ARM-host bundle be verified/resumed on an x86-64 CI publisher without rebuilding CLI artifacts. Keep the original builder platform/image ID in the immutable receipt; independently validate and pin the current native verification/publisher image against the same approved toolchain lock. Its platform/image ID belongs in separate execution/publication evidence, not a comparison requiring equality with a different architecture's builder image.

An explicit `-e` list alone is insufficient: an isolated Task7 probe demonstrated Docker injecting configured proxy values into a container without any `-e` options. Docker also documents automatically populated proxy build arguments. Clear the fixed uppercase/lowercase `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, `ALL_PROXY` and `FTP_PROXY` families at token-free run/build boundaries before selected code executes. Do not silently forward credential-bearing proxy URLs; ordinary dependency network access remains allowed. No new configurable proxy framework is needed. [Docker proxy configuration and automatic build arguments](https://docs.docker.com/engine/cli/proxy/).

### Environment, endpoint, and version contract

Create `release/environments.json`, tracked and reviewed because endpoints are public build metadata:

```json
{
  "schema_version": 1,
  "repository": "bgr11n/hookspot-cli",
  "stage": { "server_url": "", "branch": "stage" },
  "prod": { "server_url": "", "branch": "main" }
}
```

The empty URLs deliberately make release validation fail until supplied. The repository value is based on the observed origin and must be confirmed before publishing. Do not substitute example domains as real values. Select the record with an explicit `ENV=stage|prod`; export its canonical URL as `SERVER_URL` only to the build container. Reject an externally supplied generic `SERVER_URL` if it differs from that record. If it matches, accept it only as a redundant assertion, not as an override.

Validation before CLI compilation and again in release hooks must reject:

- missing/unknown environment; missing stage or prod mapping; identical canonical URLs for both environments;
- invalid absolute URL, absent host, invalid numeric port, userinfo, opaque URL, query, fragment, whitespace/control characters, or unsafe characters that could split linker arguments;
- non-HTTPS stage/prod URL; only dev/local test fixtures may use HTTP;
- an environment/URL mismatch, including a changed base path; a tag/channel mismatch; missing required release metadata.

`internal/endpoint.Parse(raw, environment)` returns a canonical base and methods for API and WS URLs. Support root or a deployment prefix such as the **test fixture** `/gateway/hookspot`; strip optional trailing slash, preserve that prefix when appending `cli/me`, project paths, or `cli/websocket`. Accept unreserved path segments; reject dot segments, repeated interior separators, encoded slash/backslash ambiguities, and unsupported percent-encoded paths with a clear error. If real infrastructure needs a broader path grammar, extend it with explicit fixtures before release.

Use `net/url`, never string scheme replacement. HTTPS maps to WSS and dev HTTP to WS. Add `vsn=2.0.0` using `url.Values`. For example, a local test base with `/gateway/hookspot/` must request `/gateway/hookspot/cli/me` and `/gateway/hookspot/cli/websocket?vsn=2.0.0`, with exactly one separator. Validate or escape identifier path segments. `internal/api` must reject redirects; an authenticated CLI API response must not forward `X-CLI-KEY` to another origin. A configured HTTPS URL that redirects to another canonical origin should be corrected in the manifest.

Preserve `cmd.version` and `cmd.serverURL`; add linker-populated `buildEnvironment`, `commit`, `sourceDate`, and `buildKind`. Defaults are respectively `dev`, `unknown`, `unknown`, and `dev`; existing version defaults to `dev`, URL remains empty. A development URL can be supplied at build time and validated as dev. Metadata absence must not silently select stage/prod or a production config path.

`version` and `version --json` run offline without loading credentials/configuration. Report version, environment, full commit, source commit date, build kind, runtime Go version, GOOS/GOARCH, and embedded canonical server URL (or unset). Never print credential values, selected config contents, or a full environment dump. `sourceDate` means commit timestamp; record invocation/build wall-clock time in the external receipt separately. Help/version remain usable with an invalid or unreadable config and an unset endpoint; network commands fail with a useful build-metadata error before authentication.

Example proposed human output format, with descriptive placeholders rather than actual release values:

```text
hookspot-stage <version> (environment=stage commit=<full-sha> go=go1.26.8 darwin/arm64 kind=release)
server=<approved-stage-url> source-date=<commit-time>
```

### Credentials, project selection, and migration

Use `hookspot` / `hookspot.exe` for prod and `hookspot-stage` / `hookspot-stage.exe` for stage. Both can coexist in PATH. Keep dev source builds named `hookspot` unless explicitly named by a developer; their metadata routes them to dev configuration.

Default files on all supported operating systems are `os.UserHomeDir()/.config/hookspot/{stage,prod,dev}/config.toml`. Retaining this home-relative convention avoids simultaneously migrating to Windows AppData/macOS Library paths. Every new persisted file has `schema_version = 1` and `environment = "stage"|"prod"|"dev"`.

Define resolution precisely:

| Setting | Precedence and behavior |
| --- | --- |
| Config path | Explicit `--config` > `HOOKSPOT_<ENV>_CONFIG_FILE` > legacy `HOOKSPOT_CONFIG_FILE` with matching `HOOKSPOT_ENVIRONMENT` assertion > environment default. The currently implemented CLI does not support `HOOKSPOT_CONFIG_FILE`; adding it is a documented new interface. |
| CLI key | Explicit `--cli-key` > `HOOKSPOT_<ENV>_CLI_KEY` > legacy `HOOKSPOT_CLI_KEY` with matching assertion > selected environment file. CLI-key flags remain supported but documentation should prefer scoped env or login because arguments can appear in process listings. |
| Project selection | Explicit `--project` UID > complete scoped organization/project slug pair > complete asserted legacy slug pair > stored selection. Treat each pair as one setting; never combine halves from different sources. Reject incomplete pairs. An explicit project UID must win over any environment slug pair. |
| Runtime environment | Immutable binary metadata. `HOOKSPOT_ENVIRONMENT` is an assertion for legacy variable use, never a backend selector. Unknown/conflicting assertions fail; no `--environment` runtime switch. Variables scoped to the other environment are ignored. |

For example, stage uses `HOOKSPOT_STAGE_CLI_KEY`, `HOOKSPOT_STAGE_ORGANIZATION_SLUG`, `HOOKSPOT_STAGE_PROJECT_SLUG`, and `HOOKSPOT_STAGE_CONFIG_FILE`; prod uses `HOOKSPOT_PROD_*`. Prefer these scoped variables when both programs run in the same shell. Preserve the deliberate absence of a `HOOKSPOT_PROJECT` UID binding (`internal/config/config_test.go:74`) unless separately requested.

For an explicitly selected config file, require an environment marker matching the binary before using any credential or making a request. A markerless legacy file requires migration. A missing explicit path is an error for ordinary commands; `login` may create a new marked file there. A missing default file is normal first-run state. An invalid default file must not prevent help/version.

Separate persisted typed settings from Viper's resolved flags/environment. A small `config.Store` owns the file record and methods such as `SaveCLIKey`, `SaveProject`, and `ClearCLIKey`; writes serialize only the intended persisted record. `project use` and automatic project resolution must not persist an environment-supplied key. `logout` removes a stored key and explains when a scoped/asserted environment credential is still active, without printing it.

Create each temporary config file with mode `0600` before writing; verify its actual access, then write, sync, close, and atomically replace the target in the same directory. Create default directories as `0700`. Refuse symlink targets, validate effective directory ancestry/ownership and mutation rights, and consistently use the validated path. Do not chmod or repair ACLs on arbitrary user directories. On Darwin, inspect ACLs because mode `0600` can coexist with inherited read grants; reject unsafe empty temps rather than repairing them and writing. A conservative relevant-allow-ACE rejection is acceptable without a full principal/group evaluator, provided errors explain unsupported private access and ordinary/deny-only ACL cases work. This promises no secrets in rejected empty inodes, not that those empty inodes were private from creation. On Windows, create with a protected DACL and use platform-appropriate replacement; Unix modes do not establish Windows privacy. Test validation/write/sync/close/publish failures preserve the old valid config.

Proposed migration interface:

```sh
hookspot-stage config migrate --from "$HOME/.config/hookspot/config.toml" --confirm-environment stage
hookspot config migrate --from "$HOME/.config/hookspot/config.toml" --confirm-environment prod
```

These are two independent, explicit operations, not a recommendation to import the same key into both. The migration reads only the named legacy file, ignores credential environment overrides, requires the confirmation value to match the binary, and validates the legacy key and selected project against that binary's backend after acknowledgement. Fail without writing on rejection. Refuse to overwrite an existing destination; leave the legacy file byte-for-byte unchanged. Fresh environment-specific login is the simpler alternative. Mark the compatibility break prominently in release/install notes.

### Version, source, and publication identity

- Production tags: `vMAJOR.MINOR.PATCH`, no leading-zero numeric components or build metadata.
- Stage tags: `vMAJOR.MINOR.PATCH-stage.N`, with positive integer N and no leading zeros.
- Snapshot version: `0.0.0-snapshot.<short-commit>`. Environment is separate metadata and an archive-name component. Snapshots are never publishable.
- Explicitly validate all tag syntax before invoking Git or passing a value to a shell command. Do not use “latest tag” discovery to choose source.
- Preserve every historical tag. Stop generating `v0.0.<commit-count>-stage.g<sha>` and legacy `stage_*`; do not rename, move, or reuse them. They may remain rebuildable through an explicitly documented historical/manual process, but the new publisher rejects them.

New stage publication requires a clean checkout and selected commit equal to fetched `origin/stage`. New prod publication requires the selected commit reachable from `origin/main` and identical to a completed, tested staging release with matching base version. Accept an existing candidate tag only when its peeled commit matches exactly, and preserve an existing remote annotated tag object instead of manufacturing another object at the same commit. Write the initial atomic `publication.json` binding the bundle, notes, exact tag object and completed eligibility checks before the first remote mutation. A retry of that recorded release may use its pinned commit after a branch advances; never require current tip equality to recover old uploads. An unrecorded bundle still needs new-publication eligibility unless an exact matching owned draft marker permits the documented lost-record recovery. Unsigned local records are auditable integrity evidence, not cryptographic attestations.

Staging validates code behavior, protocol compatibility, and packaging. Because stage/prod endpoints and environment metadata differ, **prod is a new build from the same source, not promotion of the stage binary**. Verify prod's embedded endpoint and config namespace independently. A staging success does not prove production credentials or infrastructure work.

## 4. Environment/platform/artifact matrix

Each environment produces the following six archives. `<version>` excludes the tag's leading `v`.

| Environment | Go target | Executable | Archive suffix | Runtime verification |
| --- | --- | --- | --- | --- |
| stage / prod | darwin/arm64 | hookspot-stage / hookspot | `_darwin_arm64.tar.gz` | Native Apple Silicon Mac |
| stage / prod | darwin/amd64 | hookspot-stage / hookspot | `_darwin_amd64.tar.gz` | Native Intel Mac; Rosetta is supplemental only |
| stage / prod | linux/amd64 | hookspot-stage / hookspot | `_linux_amd64.tar.gz` | Native x86-64 Linux |
| stage / prod | linux/arm64, assumed | hookspot-stage / hookspot | `_linux_arm64.tar.gz` | Native ARM64 Linux |
| stage / prod | windows/amd64 | hookspot-stage.exe / hookspot.exe | `_windows_amd64.zip` | Native x64 Windows |
| stage / prod | windows/arm64 | hookspot-stage.exe / hookspot.exe | `_windows_arm64.zip` | Native Windows on ARM |

Names start `hookspot_<env>_<version>`; example syntax: `hookspot_stage_0.1.0-stage.1_darwin_arm64.tar.gz`. Use lowercase Go OS/architecture names, retaining `amd64` consistently instead of translating it to `x86_64` in only some interfaces. Document that amd64 also runs on Intel x86-64 systems.

Each archive has exactly five regular files at its root: the correct executable, `README.md`, `INSTALL.md`, `THIRD_PARTY_NOTICES.txt`, and `build-info.json`. Unix executable mode is 0755; documents are 0644. Reject symlinks, traversal paths, extra config files, and missing members. Include dependency copyright/license notices for the shipped dependency set; do not invent a project license. If the owner supplies a root LICENSE later, deliberately update the expected member list to six.

Produce one `hookspot_<env>_<version>_checksums.txt` containing SHA-256 for exactly the six archives. **Seven uploaded assets per environment; fourteen across a stage/prod pair.** Internal GoReleaser `artifacts.json`, `metadata.json`, and resolved `config.yaml` are not uploaded. Preserve a separate local `receipt.json` with source/tag/environment, canonical URL, tool versions/image IDs, manifest digest, artifact hashes, verification results, and build invocation time. Keep release notes beside it; neither is silently added as an eighth asset.

Use `dist/<env>/<tag-or-snapshot>/<short-sha>/<unique-run-id>/artifacts/`. Each operation allocates a fresh parent, mounts that parent at `/out`, and uses literal `dist: /out/artifacts` in GoReleaser. This matters: v2.17.1's dist implementation uses the configured path directly, including for cleanup; it does not render an environment template there. No global `dist` cleanup, no `--clean` by default, and no writes through symlink output roots. Stage then prod builds must preserve both directories. Resume selects an existing parent explicitly and never rebuilds into it. [Pinned dist implementation](https://github.com/goreleaser/goreleaser/blob/v2.17.1/internal/pipe/dist/dist.go).

Use `-trimpath`, CGO 0, `GOAMD64=v1`, `GOARM64=v8.0`, commit timestamps for build metadata and binary mtime, and exact tool versions. Do not promise byte-identical archives across fresh invocations until proven; archive/container changes can affect hashes. The reliability guarantee is preservation and verification of the original successful files.

### OS baselines and signing

Go 1.26 supports macOS 12 and later; Go 1.27 raises that to macOS 13. The general Go Linux minimum is kernel 3.2, but that does not by itself certify ARM64 platform support or this application's terminal/network behavior on old distributions. Establish a practical Linux support baseline with native smoke tests on Ubuntu 22.04/24.04 or equivalent, and advertise only what is tested. Current Go Windows baseline is Windows 10 / Server 2016; use Windows 11 on ARM as the initial ARM64 product test baseline. Retain the conservative CPU feature levels above. [Official Go minimum requirements](https://go.dev/wiki/MinimumRequirements).

The fresh darwin/arm64 binary reports `Signature=adhoc`, no TeamIdentifier. It ran locally, but that is not a browser-download quarantine/Gatekeeper test. Immediate install instructions must accurately say Developer ID signing/notarization is absent, describe the expected macOS warning, and link to Apple's per-app approval guidance; do not recommend disabling Gatekeeper globally. Treat signing as required before a launch that promises a seamless signed download experience. [Apple guidance for opening software safely](https://support.apple.com/en-us/102445).

Optional Developer ID work requires an Apple Developer identity and notarization credentials. Conventional `codesign`/`xcrun notarytool` processing needs a macOS host with Apple command-line tools; compilation can remain entirely in Docker. Sign after the container build and before final archive/checksum verification and publication. Repackage through an explicitly tested step, then notarize the submission format Apple supports. Preserve per-architecture archives; a universal binary is unnecessary. Native GoReleaser notarization is documented as Pro in v2.17.1; do not make it an OSS prerequisite. Its separate cross-platform signing approach may be evaluated independently later. [Pinned notarization documentation](https://github.com/goreleaser/goreleaser/blob/v2.17.1/www/content/customization/sign/notarize.md).

## 5. Prioritized code-review findings

Severity expresses practical impact. “Release gate” means resolve before claiming the planned release system reliable; optional cleanup must not delay unrelated fixes. Passing static analysis is not proof that configuration knobs or fields have useful behavior.

### Confirmed findings

| ID / priority | Evidence | Impact | Recommended action | Verification |
| --- | --- | --- | --- | --- |
| R1 — High; credential handling gate | `docker-compose.yml:27` contains a credential literal; `:28` and `:29` hardcode selection values. `.gitignore:1` lacks local env exclusions; `.dockerignore:1` excludes only a small set. The value is deliberately not reproduced here. | A committed key may be available to every repository reader; Compose's literal also defeats intended shell selection. A copied `.env` can enter the Docker build context. Its validity was not tested. | Remove the literal in the later implementation, require injected dev credentials, and have the owner revoke/rotate it if live or confirm it is an inert fixture. Add `.env`/local secret exclusions and safe examples. Git history cleanup, if needed, is a separately coordinated destructive operation. | Secret-pattern/fixture review without printing matches; `docker compose config --services`; sentinel environment-selection test; inspect an archive/build context allowlist. |
| R2 — High; toolchain release gate | `go.mod:3`, `Makefile:1`, `Dockerfile:3`, `docker-compose.yml:17` pin Go 1.26.5. Containerized govulncheck found four reachable stdlib reports fixed in 1.26.6. | Shipping the current build carries known affected code paths, although reachability is not proof of exploitability. | Align on Go 1.26.8 and rerun all gates; ensure the GoReleaser build environment actually uses it. | `go version`, tests/vet/race, and govulncheck in the patched container; record no unresolved reachable reports or explicit evaluated exceptions. |
| R3 — High; environment isolation gate | `cmd/root.go:28` only checks nonempty URL; `.goreleaser.yaml:22` injects arbitrary env; `internal/config/config.go:38` is shared. | A staging tag can ship a prod endpoint, and either binary can load the other's credentials/project. | Reviewed environment manifest, immutable metadata, namespace-aware store, explicit migration, channel/URL checks. | Both environment builds, local-server URL tests, cross-environment file/variable rejection, no network on mismatch. |
| R4 — High; publication gate | `Makefile:69` pushes before `:70`; actual test/build hooks are `.goreleaser.yaml:5`. `--clean` targets shared dist. | A failed compile/test can leave a published tag; sequential builds destroy previous local artifacts; retries have no explicit artifact identity. | Validate/build/package before remote mutation, fresh output parents, immutable tags, draft upload and explicit resume. | Failure harness proves no remote writes on preflight/build failure; partial upload resumes only missing identical assets. |
| R5 — High; credential boundary gate | `internal/api/client.go:53` uses default HTTP redirects; `:163` sets `X-CLI-KEY`. A two-origin loopback probe confirmed the second origin receives a sentinel key. | Redirects can disclose the API key to another origin. | Reject API redirects, report a canonical-endpoint error without secret contents. | Two httptest servers; 302/307 never reach the second server with the key; direct requests still work. |
| R6 — High; persistence/isolation gate | `internal/config/config.go:80` saves Viper's merged settings; a probe reproduced an environment key appearing on disk when saving a project. `:84` chmods only after the write. | Unintended credential persistence; non-atomic writes can truncate a working config; initial permissions rely on process defaults until chmod. | Separate resolved/persisted state; atomic private writes; environment marker. Login may explicitly save a key; unrelated actions may not. | File content assertions using sentinels; interrupted/failed-write tests; initial/final mode and Windows ACL checks. |
| R7 — High; listener reliability gate | `internal/ws/client.go:179` joins before cancellation watcher `:188`; `:293` blocks reading without a join deadline. Probe cancellation did not finish within 250 ms after join receipt. `main.go:9` has no signal-derived context. | A server accepting the socket but not replying can stall initial connect indefinitely; normal signals bypass intended cleanup. | Install cancellation immediately after dial; bounded join read/write; wire signal context through ExecuteContext; cancel and await session workers. | Silent-join fixture returns on cancellation and timeout; subprocess SIGINT/SIGTERM cleanup on Unix, Ctrl-C on Windows. |
| R8 — Medium; listener liveness gate | `internal/ws/client.go:154` writes without a deadline; `:335` returns only from heartbeat on write error; `:199` has no read/liveness bound. `cmd/listen.go:451` waits in Scanner before checking cancellation. | Stalled writes/half-open sessions may not reconnect; replay input can outlive a command or remain blocked after cancellation. | Set write deadlines; close/notify session on heartbeat failure; bounded heartbeat acknowledgement/liveness policy; make replay input session-owned and closeable without closing global stdin. | No-ack/blocked-writer fixtures with short injected durations; ensure all spawned session goroutines finish after cancel; preserve idle healthy connections. |
| R9 — Medium; URL correctness gate | `cmd/listen.go:120` concatenates raw URL; API trims separately at `internal/api/client.go:50`; `cmd/listen.go:314` accepts arbitrary forward targets. | Trailing slash yields doubled WS paths; query/fragment or malformed target input produces inconsistent requests. | Shared validated base-URL builder; explicit forward-target HTTP(S) validation, preserving target prefix. | Root/prefix/trailing slash/query/fragment/invalid scheme tests for API, WS, and forward target. |
| R10 — Medium; documented runtime defect | `Dockerfile:16` comments out ENTRYPOINT; README `:119` names nonexistent Compose service. `Makefile:6` does not forward documented credential env vars or stdin; `:44` tears down the Compose project before dev. | Documented Docker commands fail; login state disappears with `--rm` unless persisted; Make run does not receive shell credentials. Dev can disrupt other project services. | Restore executable entrypoint, document `cli`, persist/mount environment-specific config when needed, allowlist dev env/input, remove unconditional Compose teardown. | Build image in Docker; run version/help with image entrypoint; exercise CLI service name and config volume with sentinel config; confirm dev preserves unrelated service. |
| R11 — Medium; release diagnostics gate | `cmd/version.go:15` prints only version; `cmd/root.go:42` loads config for it. A malformed explicit config caused version to exit 1 in the fresh binary. | Cannot identify backend/source/platform reliably or diagnose a broken config offline. | Metadata output and config-independent version/help. | Native version JSON assertions; invalid/unreadable config, absent URL, and no network access still permit version/help. |
| R12 — Medium; selection correctness | `cmd/listen.go:72` prioritizes slug pairs despite a resolved explicit project flag; README `:60` advertises flags over environment. | A user's explicit `--project` can connect to an unintended environment-supplied project. | Resolve project selection as one setting with explicit UID priority; avoid persisting transient source values. | CLI-level test sets slug env and `--project`; only the UID endpoint/topic is used. |
| R13 — Medium; output and memory reliability | `internal/printer/printer.go:111` returns success after ignored writer errors at `:149`, `:186`, `:192`, `:201`. Failing-writer probe produced HTTP 200 acknowledgement with nil error. `cmd/listen.go:384` reads the entire response; WS reads at `internal/ws/client.go:199` have no frame limit. | Print-only delivery can be acknowledged despite failed display; large payloads can consume unbounded memory despite display truncation. | Propagate printer errors; define bounded response/frame handling with backend-compatible limits. Display limits are not network limits. | Failing writer must fail handler without success reply; over-limit fixtures fail predictably without truncating and reporting success. |
| R14 — Medium; forwarding semantics | `internal/proxy/proxy.go:114` uses default redirects; `:150` follows them. | The observed status/body can be the redirected resource instead of the local target's response; redirect rules can change method and destination. | Return the first response (`CheckRedirect` with `http.ErrUseLastResponse`); review Host/hop-by-hop header handling without changing documented payload behavior casually. | 302/307 fixture preserves original status and Location; second endpoint is not contacted. |
| R15 — Medium; output/interactive hardening | `cmd/login.go:22` reads a terminal key with ordinary echoed input; startup/project/API text reaches raw terminal output at `cmd/listen.go:259`, `cmd/project.go:41`, `cmd/login.go:51`; reconnect errors at `cmd/listen.go:181` bypass final error escaping. | Credentials can be visible while typing; backend-controlled terminal text can contain control sequences. | Use terminal no-echo input with non-TTY fallback; reuse output escaping for untrusted text. Preserve printer's existing header redaction; optionally add CLI/API-key header names. | Fake terminal/piped input including final EOF; control-character fixtures; assert sentinel secrets never appear in captured output. |
| R16 — Medium; advertised no-op, optional cleanup | `cmd/root.go:69` registers `--log-level`; `internal/config/config.go:17`, `:31`, `:61` store/load it; no production code consumes its value. README `:60` advertises it. | The setting implies behavior that does not exist. Staticcheck does not catch a public/configuration no-op. | Deprecate the flag for one release with a concise warning when explicitly used; remove it from advertised settings and dev env. Do not add a logging subsystem solely to justify it. Remove persisted field/default later; tolerate old TOML keys. | Capture identical behavior for legacy values plus deprecation warning; ensure removing config storage preserves meaningful settings. |
| R17 — Low; optional unused-code cleanup | `cmd/listen.go:255` wrapper `printListenInfo` has only test callers (`cmd/listen_test.go:124`, `:150`); production calls `printListenInfoWithReplay` at `:115`. `internal/printer/printer.go:49` Options.Mode is never read; mode constants/aliases at `:28` are only plumbing. `cmd/errors.go:29` kind is set/tested but not used for runtime presentation/exit policy. | Misleading APIs and tests exercising a wrapper rather than the production call. No demonstrated runtime failure. | Have tests call the production formatter with replay=false and remove/move the wrapper. Remove unused internal Mode plumbing after confirming callers. Decide whether error-kind taxonomy has a near-term consumer; otherwise simplify separately. | Full tests/staticcheck plus caller inventory. Do not remove serialization fields, init registration, linker symbols, or useful test seams by name search alone. |
| R18 — Medium/Low; maintenance follow-up | `go.mod:6` uses archived Survey v2; `:10` has a 2021 x/term revision. All five direct dependencies have real production callers; tidy -diff and staticcheck were clean. | Terminal behavior maintenance is a risk, especially Windows ARM64. This is not proof of an unused or currently vulnerable direct dependency. | Incremental x/term update with terminal tests; evaluate replacing Survey separately if needed. Avoid a wholesale prompt/UI rewrite in the release patch. | Patched-toolchain govulncheck, go mod why/matching call sites, native interactive smoke tests. Survey archive status: [upstream](https://github.com/AlecAivazis/survey). |

### Unused-code audit boundaries and unresolved hypotheses

The audit did not establish an unused direct module. Cobra commands are installed through `init`, linker metadata is intentionally set outside ordinary Go calls, JSON DTO fields participate in decoding/encoding, and interfaces such as `deliveryForwarder` are meaningful test seams. Test-only helpers are not automatically obsolete production features. Preserve existing reconnect/error classification and replay synchronization, which have real callers and behavior tests.

- API `Active` flags, `Project.Sources`, `DisplayName`, and some UID fields are decoded but not all consumed in production (`internal/api/client.go:57`, `:72`). `resolveSources` checks connection count rather than Active flags (`cmd/listen.go:203`), and the connection label deliberately ignores DisplayName (`cmd/listen.go:306`, tested). The backend's filtering/field contract was unavailable: do not declare an authorization bug or remove/filter these fields without checking it.
- Delivery events are processed without validating topic/join identity (`internal/ws/client.go:215`), while close/error events check topic. Add protocol fixtures and confirm the backend expectation before classifying cross-topic processing as a reachable production defect.
- Existing chmod tests demonstrate Unix mode bits after a save, not Windows ACL safety. Native validation is required before claiming equivalent credential privacy on Windows.
- The runtime Alpine image's CA trust state was not built/inspected in this review. Verify it and ensure TLS trust is present; do not assert that CA certificates are missing based only on Dockerfile text.
- Current terminal redaction is intentionally header-specific; body/query content can contain user data. No test here established arbitrary credential reflection by the backend. Avoid logging resolved config/env or treating an archive string scan as proof that every future secret source is impossible.

Command state is another maintenance concern: `cmd/root.go:13`, `:35` and `cmd/listen.go:28` are package globals/singletons. Existing helper tests avoid many full-command execution paths. Keep tests that change globals serial and restore values/flags with cleanup; use subprocess behavior tests for the release-critical CLI contract. A command-constructor refactor could improve isolation later, but is not required just to introduce release metadata. This is a confirmed structural limitation, not a demonstrated concurrent production race.

## 6. Ordered implementation tasks

All checkboxes below are future work. The examples define the intended interface; none of the new commands/files described here were implemented during this review. Implement small commits in this order, with behavior tests for failures and compatibility, rather than tests that compare YAML strings.

### Task 1 — Remove credential exposure from the development path and align tools

**Files:** create `release/toolchain.env`, `docker/release.Dockerfile`; modify `go.mod`, `go.sum` only as required by the toolchain/dependency checks, `Makefile`, `Dockerfile`, `docker-compose.yml`, `.air.toml`, `.gitignore`, `.dockerignore`, `README.md`. No file removal. **Dependencies:** none.

- [ ] Remove committed Compose credential/selection literals; use required or empty environment injection with clear first-run diagnostics. Document that the owner must assess/revoke the old literal separately. Do not put its value into a test, issue, release note, or build log.
- [ ] Add `.env`, `.env.*` exclusions with an explicit exception for a placeholder `.env.example` if one is introduced. Exclude `dist`, `tmp`, local config, and env/credential files from Docker build context. Retain the narrow release archive whitelist independently of Docker ignores.
- [ ] Centralize the version values listed above. Lock Docker multiarch index digests and GitHub CLI archive hashes alongside versions in `release/toolchain.env`; verify platform selection rather than forcing amd64 on Apple Silicon. Update Go 1.26.8 consistently. Do not update every dependency as part of this task.
- [ ] Build the local tool image, for example `hookspot-release:go1.26.8-gr2.17.1-gh2.100.0`, using Docker only. Expose `make release-tools` as a thin script call once Task 7 is added. Until then, use an explicit `docker build -f docker/release.Dockerfile ...` invocation with values from the tracked version file.
- [ ] Restore `ENTRYPOINT ["hookspot"]` to the runtime Dockerfile, preserve its non-root user, and verify CA trust for HTTPS. Make its builder use the same patched Go version and explicit build metadata args. Runtime image publication remains out of scope.
- [ ] Use Compose service `cli` in documentation; pass the tracked tool versions with `docker compose --env-file release/toolchain.env ...`. Keep Air's pinned version and its build-time dev URL; add dev environment metadata to Air/Make/Docker builds. Remove unconditional `compose down` from `make dev`.
- [ ] Allowlist scoped dev credentials into `make run` and give interactive login stdin/TTY when requested. Explain config mounting/persistence for `docker compose run --rm cli`; do not imply that a discarded container preserves login. Correct `host.docker.internal` mapping for the service that uses it.

**Verify:** container `go version` is 1.26.8; GoReleaser/GH versions match; Go tests/vet/race/staticcheck/govulncheck pass on the patched toolchain; `go mod tidy -diff` is empty; runtime image `version`/`--help` uses ENTRYPOINT; Compose service lookup works and shell sentinel values win. Use `docker compose config --services`, not a config dump containing real credentials. Docker Desktop host networking is now available with opt-in on 4.34+, so replace the blanket README claim that it is Linux-only with a version-qualified statement. [Docker host networking](https://docs.docker.com/engine/network/drivers/host/).

**Compatibility:** dev-only environment variable changes need a README example; runtime images start the CLI instead of the inherited shell. No new distribution channel is introduced. A root project license remains an owner decision; retaining third-party notices is handled in Task 6.

Release-image Dockerfile core (the script supplies the verified image references and architecture-specific checksum from the lock file; these ARGs are required inputs):

```dockerfile
ARG GORELEASER_IMAGE
ARG GO_IMAGE
FROM ${GORELEASER_IMAGE} AS goreleaser
FROM ${GO_IMAGE}
COPY --from=goreleaser /usr/bin/goreleaser /usr/local/bin/goreleaser
ARG TARGETARCH
ARG GH_VERSION
ARG GH_SHA256
RUN set -eu; \
    test -n "$GH_VERSION"; \
    test -n "$GH_SHA256"; \
    case "$TARGETARCH" in amd64|arm64) ;; *) exit 1 ;; esac; \
    curl -fLsS "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz" -o /tmp/gh.tar.gz; \
    printf '%s  %s\n' "$GH_SHA256" /tmp/gh.tar.gz | sha256sum -c -; \
    tar -xzf /tmp/gh.tar.gz --strip-components=2 -C /usr/local/bin \
      "gh_${GH_VERSION}_linux_${TARGETARCH}/bin/gh"
ENV GOTOOLCHAIN=local GOFLAGS=-mod=readonly
WORKDIR /work
CMD ["goreleaser", "--help"]
```

This uses the Go Debian image's curl/Git/CA tools; verify their presence in the locked image during Task 1. Use BuildKit's TARGETARCH for the image's native architecture. Do not copy the entire Alpine Go toolchain over the patched Go image. The old GoReleaser executable's own compiler version does not change the selected compiler used to build Hookspot; the latter must be asserted separately.

### Task 2 — Implement canonical endpoint and environment validation

**Files:** create `release/environments.json`, `internal/endpoint/endpoint.go`, `internal/endpoint/endpoint_test.go`, `tools/releasecheck/main.go`, `tools/releasecheck/environment.go`, `tools/releasecheck/environment_test.go`; modify `cmd/root.go`, `cmd/listen.go`, `internal/api/client.go`, `internal/api/client_test.go`, `internal/proxy/proxy.go`, `internal/proxy/proxy_test.go`. **Dependencies:** Task 1 for tool versions; owner-supplied URLs are required only to validate real release inputs, not to write fixture tests.

- [ ] Implement the section 3 URL grammar and canonicalization once. Suggested interface: `Parse(raw string, environment string) (Base, error)`, `Base.API(relativePath string) *url.URL`, `Base.WebSocket() *url.URL`, and a safe path-segment helper. Avoid functions returning unchecked concatenated strings.
- [ ] Add `releasecheck env --environment stage|prod --server-url <value>`; it reads the tracked manifest, compares canonical values, validates both records, and prints only nonsecret diagnostics. The release script uses the manifest-selected value, not user-provided shell interpolation. Unknown fields/schema versions in the manifest fail validation.
- [ ] Replace API/WS URL concatenation with these methods. Preserve supported deployment prefixes and `vsn=2.0.0`; reject invalid input before creating clients or looking up credentials.
- [ ] Configure API redirect rejection and proxy first-response behavior. API errors should identify the endpoint/status without including header values. Keep the existing API/forwarding timeouts.
- [ ] Validate a forward target as HTTP(S) with a host, optional prefix, no userinfo/query/fragment; retain the documented convenience of adding `http://` when no scheme is supplied. Escaped path segments must not turn an API UID into a second route.

**Tests:** root URL and prefixed URL each with/without trailing slash produce identical canonical routes; HTTPS->WSS and dev HTTP->WS; malformed scheme/host/port/userinfo/query/fragment/unsafe segment/unknown env fail; swapped stage/prod URLs fail; a pair of httptest servers proves an API redirect cannot forward the sentinel key; forwarding returns original 302/307. Run `go test ./internal/endpoint ./internal/api ./internal/proxy ./tools/releasecheck` in the tool container.

**Compatibility:** endpoint remains build-time fixed. Trailing slash becomes supported consistently. Previously accepted malformed URLs now fail explicitly. If actual approved URLs need a currently rejected path form, extend the validator and test fixtures before release; do not bypass it with an override.

**Interfaces produced:** `endpoint.Base` privately owns a `url.URL`; its API/WS methods return fresh URL values so callers cannot mutate the canonical base. `endpoint.Segment(string) (string, error)` accepts nonempty ASCII letters/digits/underscore/hyphen for backend UIDs and rejects path separators. Change `api.New` to `New(base endpoint.Base, cliKey string) *Client`; callers validate first. Change proxy construction to `New(target string) (*Forwarder, error)` so bad targets fail immediately. Update every caller/test in this task; the higher-level API request methods retain their existing names.

Representative behavior test for `internal/endpoint/endpoint_test.go` (run red before implementing Parse, then green):

```go
func TestStageBasePreservesPrefix(t *testing.T) {
    for _, raw := range []string{
        "https://stage.example.invalid/gateway/hookspot",
        "https://stage.example.invalid/gateway/hookspot/",
    } {
        base, err := Parse(raw, "stage")
        if err != nil { t.Fatal(err) }
        if got := base.API("cli/me").String(); got !=
            "https://stage.example.invalid/gateway/hookspot/cli/me" {
            t.Fatalf("API URL = %q", got)
        }
        if got := base.WebSocket().String(); got !=
            "wss://stage.example.invalid/gateway/hookspot/cli/websocket?vsn=2.0.0" {
            t.Fatalf("WS URL = %q", got)
        }
    }
}
```

The fixture domain supplies test data only; the release manifest remains empty until approved endpoints are provided. Add the invalid-input and redirect cases enumerated above to exercise failures, rather than asserting implementation string operations.

### Task 3 — Add offline build identity and command context

**Files:** create `cmd/build_info.go`, `cmd/version_test.go`; modify `cmd/root.go`, `cmd/version.go`, `main.go`, `cmd/errors_test.go`, plus linker flags in `Makefile`, `.air.toml`, and `Dockerfile`. **Dependencies:** Tasks 1–2.

- [ ] Define the six linker metadata strings described above, keeping the current symbol names for version/serverURL. Add a typed read-only build-info view used by command output and config environment selection.
- [ ] Implement stable `version --json` keys: `version`, `environment`, `commit`, `source_date`, `build_kind`, `go_version`, `os`, `arch`, and `server_url`. Human output uses the actual executable/environment name. Keep metadata publicly printable and credentials absent.
- [ ] Move config initialization behind commands that require it, using a narrow command annotation/helper or explicit bypass for version/help. Do not make version inherit a malformed-config failure. Avoid a whole command-tree rewrite solely for this change.
- [ ] Expose `ExecuteContext(ctx)` while retaining an `Execute()` compatibility wrapper if useful. `main` supplies `signal.NotifyContext` for interrupt and platform-supported termination signals, stops signal handling before exiting, and passes the context through the existing supervisor/forwarder. Preserve exit 0 on intentional cancellation and nonzero on command/auth/config failures.

**Tests:** execute version/help with no config, malformed explicit config, no URL, and an unreachable network; validate all JSON fields using injected build strings; test missing release metadata rejects network commands but dev version/help work. Build all six targets to catch signal/platform compile issues; exercise Unix signals and Windows Ctrl-C on native hosts. Version output must not contain any sentinel key.

**Compatibility:** existing `hookspot version` remains valid with richer output; scripts should use the new JSON form. No runtime endpoint flag is added. Keep error presentation once per failure and do not claim Unix SIGTERM behavior for Windows process termination.

The context handoff should be this narrow, with the existing error presenter retained:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
err := cmd.ExecuteContext(ctx)
stop()
code := cmd.HandleError(os.Stderr, err)
if code != 0 { os.Exit(code) }
```

This preserves the current `HandleError(out io.Writer, err error) int` interface. Add `context`, `os/signal`, and `syscall` imports to main. Handle unsupported platform signal semantics without introducing native compilation into the release workflow.

### Task 4 — Isolate config, persistence, and selected projects

**Files:** modify `internal/config/config.go`, `internal/config/config_test.go`, `cmd/root.go`, `cmd/login.go`, `cmd/logout.go`, `cmd/project.go`, `cmd/listen.go` and corresponding command tests; create `internal/config/store.go`, `internal/config/store_test.go`, `internal/config/write_unix.go`, `internal/config/write_windows.go`, `cmd/config.go`, `cmd/config_test.go`. **Dependencies:** Tasks 2–3.

- [ ] Introduce `config.Options{Environment, ExplicitPath}` and `config.Store`. Keep persisted fields separate from Viper's resolved values; make file-path origin and project-selection origin explicit enough to enforce the precedence table. Do not serialize `v.AllSettings()` or `WriteConfigAs` for unrelated updates.
- [ ] Implement scoped variables and matching legacy assertions. Validate schema/environment marker before resolving credentials. An explicit `--project` must override an environment slug pair; reject incomplete pairs rather than combining sources.
- [ ] Implement store operations for explicit credential persistence, project persistence, and logout. Only login/migration intentionally save a key. Make logout explain still-active environment credentials without their values.
- [ ] Implement private atomic writes, with cleanup on errors, effective ancestry/ownership checks, Darwin ACL inspection before secret reads/writes, and Windows protected-DACL creation/replacement. Keep old valid config on failed writes. Detect unsafe symlink targets and ancestor control while supporting safe consistently resolved parent aliases. Never change permissions/ACLs of arbitrary existing user directories. Keep OS-specific helpers small and documented; do not build a general ACL evaluator or file-lock framework.
- [ ] Implement `config migrate --from PATH --confirm-environment ENV` with exact source-file reading, explicit network validation on the selected backend, no env key substitution, no destination overwrite, and unchanged legacy source.
- [ ] Use no-echo terminal reading for login; retain tested noninteractive newline/final-EOF and cancellation paths. Terminal mode changes/restoration belong synchronously to the caller, not a goroutine running `term.ReadPassword`. A fallback byte-reader is guarded once per process, bounded in size and uses a buffered result channel; never close global stdin or claim that a blocked read itself exits on cancellation. Give restoration errors precedence over cancellation so `HandleError` cannot suppress them. Test delayed reader startup and actual isolated-PTY Ctrl-C without touching the user's terminal. Escape user/backend-controlled displayed identifiers consistently.

**Tests:** a table spanning stage/prod/dev, defaults/scoped paths/explicit paths, marker mismatch, legacy markerless file, absent path, and malformed TOML; stage login/project change leaves prod byte-identical and vice versa; project-save/logout with env sentinel never creates that key in the file; migration only writes after matching acknowledgement and successful target validation; failed login/migration preserves both files; Unix file mode and Windows access checks; flag UID beats slug env. Run command tests serial or as subprocesses to avoid singleton contamination.

**Compatibility:** this intentionally breaks unsafe automatic use of the legacy shared file and generic environment variables. Provide explicit migration and scoped examples in release notes. Tolerate obsolete TOML keys such as `log_level` while the flag is deprecated; they must not prevent migration.

**Store interface produced:** replace the old `config.New(string)`/global Save API at its callers with the following contract; keep implementations private except for these operations:

```go
type Options struct {
    Environment     string
    ExplicitPath    string
    ExplicitPathSet bool // distinguish absent flag from --config=""
}
type Overrides struct {
    CLIKey  *string // nil means flag not supplied
    Project *string
}
func New(opts Options) (*Store, error)
func (s *Store) Resolve(flags Overrides) (Config, error)
func (s *Store) Path() string
func (s *Store) SaveCLIKey(key string) error
func (s *Store) SaveProject(uid string) error
func (s *Store) ClearCLIKey() error
```

`Config` retains CLIKey/Project/OrganizationSlug/ProjectSlug as resolved values. File schema/environment belong to the persisted record. Explicit empty config path is an input error; explicit empty credentials do not silently fall back to a saved key. The command layer derives Overrides from Cobra flag Changed state. Migration is a command-layer operation: parse the legacy record, validate against the selected API, then write a new marked record atomically, without changing either store on failed validation. Network validation does not belong inside a generic config-file writer.

### Task 5 — Close demonstrated listener/output failures

**Files:** modify `internal/ws/client.go`, `internal/ws/client_test.go`, `cmd/listen.go`, `cmd/listen_test.go`, `internal/printer/printer.go`, `internal/printer/printer_test.go`, `main.go`; add focused fixture helpers in the affected test packages. **Dependencies:** Tasks 2–4 for context/URL/config behavior.

- [ ] Own cancellation of the underlying socket as soon as TCP dial succeeds, before the HTTP WebSocket upgrade or proxy CONNECT, and retain it through join/session cleanup. Pinned Gorilla1.5.3 DialContext does not itself interrupt a stalled HTTP upgrade response read on cancellation; waiting until it returns to install the watcher is too late. Preserve proxy/TLS behavior, never mutate the global default dialer, and avoid a detached dial goroutine. Set an initial join timeout (proposed 10 seconds) and write deadline (proposed 10 seconds). Keep timeout values injectable through an internal options/test constructor, not public CLI flags.
- [ ] Use the existing writer mutex for both deadline changes and writes. Treat heartbeat write failure as session failure by closing/notifying the connection; do not leave the reader blocked. Keep the 30-second interval and a proposed 90-second receive-idle window. Renew liveness only for a valid delivery on the joined topic or a matching successful Phoenix heartbeat reply. Keep serial delivery handling; rearm the read deadline after completing a valid delivery and its response so bounded local processing does not consume the receive-idle window. Stale/foreign replies and malformed messages must not renew it. This is an activity-based liveness policy, not a strict deadline for every heartbeat acknowledgement; ongoing valid deliveries also prove the connection is alive. Bound pending reference tracking without per-heartbeat workers.
- [ ] Keep one owner of read operations/deadlines. Ensure cancellation wins over ensuing connection errors, protocol/auth rejection remains nonretryable, and cleanup waits for owned session workers. Preserve existing reconnection and handler-error behavior. Prompt cancellation covers interruptible network operations, not arbitrary blocking callbacks: a diagnostic using the real printer and an unread OS pipe confirms that closing the WebSocket cannot unblock synchronous stdout. Retain synchronous ordered output and document this limit; do not introduce detached delivery workers or claim all output can be cancelled. Record interruptible command-owned output as a separate design question if it is needed.
- [ ] At the process boundary, restore default signal handling promptly after the first cancellation so a later Ctrl-C can force exit from blocked synchronous output. One small watcher waits for ctx.Done(), calls the existing stop function and closes its done channel; normal ExecuteContext completion calls stop and joins that watcher before the existing error presenter. Keep the command on its current execution path, without detached command/handler workers. Isolated Linux/macOS subprocess probes reproduce the current second-interrupt trap and verify first-signal graceful completion, second-signal exit and normal-return cleanup with this change. Document that force exit may interrupt cleanup; do not claim the first signal unblocks arbitrary writers or that an immediate second signal before restoration is guaranteed. Native Windows remains separately verified.
- [ ] Make print-only `Printer.Handle` return writer failures instead of an HTTP 200 response. Surface forward-display errors separately from transport failure; do not fabricate a 502 solely because stdout failed. Attempt to acknowledge a completed local response with its real status/body, then stop for the known handler failure even if that acknowledgement also fails; parent cancellation takes precedence. Never turn a known fatal output failure into a retryable connection error. Propagate startup/reconnect-notice writer failures too. Tests define actual acknowledgement behavior, preserving the existing delivery-correlation contract.
- [ ] Add bounded network reads. Initial proposed limits are 32 MiB for a WS frame and 16 MiB for a local response body, with a `limit+1` check rather than silent truncation. Confirm these against backend webhook limits before enabling them; adjust constants/tests if the service contract is larger. Preserve the existing bounded API error body and add a reasonable bounded successful API JSON response (proposed 1 MiB). Display truncation remains independent.
- [ ] Give replay input one command-lifetime owner and cancel it when listen returns. An arbitrary `io.Reader` cannot be made cancellable by checking context after Scanner.Scan: use an owned closeable input abstraction in tests and a platform-supported interruptible terminal handle in production. Never close process-global stdin from a library. If a platform needs a single process-lifetime console reader, document that bounded design and do not claim the reader exits on context cancellation; do not spawn it per reconnect.

**Tests:** join accepted/no reply; cancellation during dial/join/read/write; heartbeat write error and receive-idle expiry; healthy idle connection; multiple slow but individually bounded deliveries queued ahead of heartbeat replies without false expiry, followed by true inactivity expiry; oversized frames/responses; failed printer writer; no success reply after failed print-only delivery; original response semantics on redirect; repeated start/stop without accumulating session workers; blocked-output limitation and cancellation-error precedence after the output resumes. Use short injected durations and completion channels instead of long sleeps. Run affected packages with `-race` in native Linux Docker and run signal/console cases natively.

**Compatibility:** timeout/bounds are deliberate behavior changes and belong in release notes. Exact payload limit and terminal-reader portability need backend/native verification, not a guessed guarantee. If these optional bounds/console details are split into a follow-up, the join-cancellation, heartbeat failure, signal wiring, and print-only acknowledgement fixes remain the pre-release core of this task.

### Task 6 — Make GoReleaser package both environments consistently

**Files:** modify `.goreleaser.yaml`; create `docs/releases/INSTALL.md`, `docs/releases/THIRD_PARTY_NOTICES.txt`, `tools/releasecheck/metadata.go`, `tools/releasecheck/artifacts.go`, `tools/releasecheck/artifacts_test.go`. **Dependencies:** Tasks 1–4; complete Task 5's core before publication.

The following proposed configuration uses fields supported by v2.17.1. A temporary version of it passed that executable's schema check during review; its new hooks cannot run until their implementation exists. Put environment validation before expensive hooks when implementing.

```yaml
version: 2
project_name: hookspot
dist: /out/artifacts
env:
  - GOTOOLCHAIN=local
  - GOFLAGS=-mod=readonly
before:
  hooks:
    - go run ./tools/releasecheck env --environment={{ .Env.RELEASE_ENV }} --server-url={{ .Env.SERVER_URL }}
    - go mod download
    - go test ./...
    - go run ./tools/releasecheck metadata --version={{ .Version }} --commit={{ .FullCommit }} --source-date={{ .CommitDate }} --kind={{ if .IsSnapshot }}snapshot{{ else }}release{{ end }} --output=/src/build-info.json
builds:
  - id: hookspot
    main: .
    binary: '{{ if eq .Env.RELEASE_ENV "stage" }}hookspot-stage{{ else }}hookspot{{ end }}'
    env: [CGO_ENABLED=0]
    flags: [-trimpath, -buildvcs=true]
    ldflags:
      - >-
        -s -w
        -X hookspot/cmd.version={{ .Version }}
        -X hookspot/cmd.serverURL={{ .Env.SERVER_URL }}
        -X hookspot/cmd.buildEnvironment={{ .Env.RELEASE_ENV }}
        -X hookspot/cmd.commit={{ .FullCommit }}
        -X hookspot/cmd.sourceDate={{ .CommitDate }}
        -X hookspot/cmd.buildKind={{ if .IsSnapshot }}snapshot{{ else }}release{{ end }}
    goos: [darwin, linux, windows]
    goarch: [amd64, arm64]
    goamd64: [v1]
    goarm64: [v8.0]
    mod_timestamp: '{{ .CommitTimestamp }}'
archives:
  - id: cli
    ids: [hookspot]
    formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]
    name_template: 'hookspot_{{ .Env.RELEASE_ENV }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}'
    builds_info:
      mode: 0755
    files:
      - README.md
      - src: docs/releases/INSTALL.md
        strip_parent: true
      - src: docs/releases/THIRD_PARTY_NOTICES.txt
        strip_parent: true
      - build-info.json
checksum:
  name_template: 'hookspot_{{ .Env.RELEASE_ENV }}_{{ .Version }}_checksums.txt'
  algorithm: sha256
snapshot:
  version_template: '0.0.0-snapshot.{{ .ShortCommit }}'
changelog:
  disable: true
release:
  disable: true
```

The wrapper validates `RELEASE_ENV` before evaluating the binary-name template; its `else` is not permission to accept unknown environments. The metadata hook also reads the approved environment/URL from its allowlisted environment and verifies them. No `templated_extra_files`, native Pro notarization, dist-path template, or unverified OSS continuation feature is required.

Official pinned references for these exact fields: [Go builder and linker templates](https://github.com/goreleaser/goreleaser/blob/v2.17.1/www/content/customization/builds/builders/go.md), [template variables](https://github.com/goreleaser/goreleaser/blob/v2.17.1/www/content/customization/general/templates.md), [archive formats/files/modes](https://github.com/goreleaser/goreleaser/blob/v2.17.1/www/content/customization/package/archives.md), [snapshot version_template](https://github.com/goreleaser/goreleaser/blob/v2.17.1/www/content/customization/publish/snapshots.md), [disabling SCM publication](https://github.com/goreleaser/goreleaser/blob/v2.17.1/www/content/customization/publish/scm/_index.md). The snapshot documentation has an older explanatory reference to `name_template`; use the current schema key `version_template`, which was checked successfully.

The pinned source also confirms `snapshot.Pipe` precedes `before.Pipe`, so the metadata before-hook sees the resolved snapshot Version. The normal dist creation follows the before-hook. [v2.17.1 pipeline order](https://github.com/goreleaser/goreleaser/blob/v2.17.1/internal/pipeline/pipeline.go#L64-L103).

Actual Task 6 packaging exposed a schema-check limitation: v2.17.1 accepts the proposed absolute archive source in configuration validation, but its archive globber rejects `/out/build-info.json` when packaging. Generate metadata exclusively at `/src/build-info.json` in the disposable source checkout and include the relative root filename instead. Ignore exactly `/build-info.json` so generation does not mark VCS source dirty; never generate it in the user's checkout. Retain the exact generated bytes as `/out/build-info.json` before cleaning up transient source. The first real six-target snapshot passed with this source-local input and unchanged `vcs.modified=false`; no Pro feature or arbitrary glob workaround is needed. [Pinned archive-file evaluation](https://github.com/goreleaser/goreleaser/blob/v2.17.1/internal/archivefiles/archivefiles.go#L18-L38).

- [ ] Generate public `build-info.json` with mode 0644 from GoReleaser's resolved version/commit/kind, not a second independent version calculation. Include the environment URL, Go version, tool version, and six-target declaration; per-executable OS/arch remains available through version JSON. Set a predictable 022 umask for nonsecret build outputs; keep credential-store creation explicitly 0600 independent of umask.
- [ ] Implement `releasecheck artifacts --environment ENV --dist /out --tag TAG` (snapshot uses `--snapshot` instead of tag) to inspect tar/zip members, permissions, SHA-256, Mach-O/ELF/PE machine type, available Go/VCS/target build information, public metadata against expected build inputs, and the exact asset set. During creation, compare archived binary hashes with the fresh GoReleaser binary artifacts. Custom linker-global values require exact-binary `version --json` observations; neither `debug/buildinfo` under `-trimpath` nor a string scan proves them. Use standard-library archive/debug packages; avoid needing host `file`, jq, or Python for the supported workflow.
- [ ] Bound compressed/expanded archive data, member count/size and JSON with fixed internal limits comfortably above real packages. Bound the actual opened-file read, not only a prior path-size check. Use exact root-name equality, reject links/duplicates/traversal and special permission bits, fully read bounded members, and consume gzip through verified EOF rather than assuming tar EOF or gzip Close validates the trailer. Reject nonpadding trailing decompressed content. Check ZIP local/central header consistency: a reproduced archive with a traversal local filename but safe central filename passed Go's central-directory view while ordinary tar/unzip reported different names. Reject this ambiguity without a general ZIP framework or an unproven extraction-escape claim. Check executable kind/64-bit machine and overflow-safe file-backed section/segment extents; parser success alone does not prove that declared data fits in the file. Reject universal Mach-O. These are structural integrity checks, not comprehensive runtime proof.
- [ ] Write `receipt.json` once after checks, helper hashes and build results are complete, recording checks actually executed and those still requiring runtime/native evidence. Later verification compares against the recorded hashes and does not regenerate them. A checksum/file-format pass is not a runtime pass. Reject detected corruption/truncation, unsafe archive paths, duplicate members, unexpected target count, and wrong executable names.
- [ ] Assemble third-party notices from the union of actual shipped dependencies across all six targets, including platform-only modules and the pinned Go runtime/distribution's relevant notices, and preserve upstream license/copyright text. Do not silently omit notices merely because there is no project LICENSE. A checked-in auditable document and refresh instructions suffice; no new license framework is needed. Installation docs identify source/tag, stage/prod names, config migration, checksums, and unsigned Mac status.

**Verify:** container `goreleaser check .goreleaser.yaml` exits 0; both snapshot and tagged nonpublishing builds produce exactly six archives plus checksum file with expected members; all checksums verify; sequential stage/prod builds retain both outputs; token-less builds work. Check `.Version` is the final snapshot version in archived JSON and each runtime-observed binary; record unobserved targets. Run `go test ./tools/releasecheck` with corrupt/missing/extra/archive-traversal fixtures, decompression bombs, bad gzip trailers, appended tar data, ZIP CRC failures, and executable extents beyond EOF.

**Compatibility:** archive names change and include environment; document the new names rather than maintaining ambiguous legacy aliases. Old assets remain untouched. Do not claim deterministic rebuild equivalence to a retained prior archive.

### Task 7 — Implement token-free local entry points and source isolation

**Files:** create `scripts/release.sh`, `scripts/release_test.sh`, `tools/releasecheck/receipt.go`, `tools/releasecheck/receipt_test.go`; modify `Makefile`; extend `tools/releasecheck/main.go`. **Dependencies:** Tasks 1–6.

Define these script operations, all with `--environment stage|prod` except `tools`:

```text
release.sh tools
release.sh check --environment ENV
release.sh snapshot --environment ENV [--ref REF]
release.sh build --environment ENV --tag TAG
release.sh verify --environment ENV --dist PARENT
release.sh publish --environment ENV --tag TAG [--ref REF] --notes PATH [--from-stage-tag TAG --stage-acceptance PATH] [--dist PARENT]
release.sh status --environment ENV --tag TAG [--dist PARENT]
release.sh resume --environment ENV --tag TAG --dist PARENT
```

- [ ] Use portable shell compatible with macOS Bash 3.2/Linux; no Bash 4-only associative arrays, GNU-only realpath/date/sed assumptions, eval of user input, or secrets in command arguments. Use tracked config/version files as data with strict parsing. Ensure Make entry points do not include/evaluate unchecked lock contents before the parser runs; test both direct-script and Make entry paths.
- [ ] Validate parameters, Git repository/full history, manifest, tool compatibility, and clean tree before building. Reject shallow, partial/promisor, replacement-ref and legacy-graft source views before reading selected blobs, with guidance to prepare a normal full local checkout. A nonshallow partial clone can still auto-fetch during git show; a replacement ref can make preflight and clone see different contents for one SHA. Use the original-object view consistently. Require a clean worktree by default even for snapshots; users can commit to a temporary branch. Do not offer an undocumented dirty-production override. Read-only retained-artifact verification need not require a clean current worktree; publication-policy compatibility is a separate check.
- [ ] Resolve REF to a full commit once. Prepare a disposable full-history checkout using an independent clone (`--no-local --no-checkout`, not shared objects/alternates) without changing the user's branch/index; verify commit and tag object in the copy before checkout. This must support linked-worktree callers. Keep the invoking reviewed script as the small host coordinator; read selected-commit control files as data and run selected build scripts/hooks only in the token-free isolated container. Never re-execute selected-source shell code on the host with ambient credentials. Mount only the independent disposable source as the writable container work directory, never the original checkout or its shared Git metadata. Validate that selected source supports this release interface; do not quietly build historical code using today's config.
- [ ] Prevent checkout itself from running selected source on the host. An isolated fixture reproduced a tracked `post-checkout` hook selected through inherited `core.hooksPath`, and a selected `.gitattributes` invoking a configured smudge filter; both saw a host sentinel. Disable hooks and isolate disposable clone/checkout from global/system/environment-injected Git configuration, or perform checkout only in the token-free container. Disabling hooks alone does not disable filters. Inspect the original source's actual partial/promisor configuration before suppressing config for its disposable copy; do not hide a forbidden source view. Never edit the user's real Git configuration.
- [ ] Replace the disposable clone's host-path origin with the validated manifest's public repository URL, without fetching or pushing. Do not remove all remote metadata: pinned GoReleaser v2.17.1 unconditionally reads the URL while constructing source identity, even when publication is disabled. Source configuration checks must include enabled worktree configuration/includes; a local-only promisor lookup was reproduced accepting a source that lazily fetched during a blob read.
- [ ] Snapshot: use a harmless `GORELEASER_CURRENT_TAG=v0.0.0` only as a snapshot parsing input, then `goreleaser release --config .goreleaser.yaml --snapshot --skip=publish`. No tag ref is created. Its explicit snapshot template supplies the actual snapshot version.
- [ ] Tagged build: require the named tag already exists locally, peel and check out exactly that commit, reject channel mismatch, and set `GORELEASER_CURRENT_TAG` to it. Run `goreleaser release --config .goreleaser.yaml --skip=publish`. Do not skip tag validation. Nonpublishing build does not create/fetch/push tags or require a GitHub token.
- [ ] Allocate an exclusive output parent and record source/tool identity; no global cleanup. `check` validates config and manifest without packaging; snapshot/build automatically inspect artifacts and run the native Linux target compatible with the build host, using isolated configuration. `verify` checks an existing parent without modifying archives.
- [ ] Retain/hash the source-selected release controls, including `docker/release.Dockerfile`, so a different-architecture verifier can validate the same approved recipe and lock. Verification cross-checks decoded retained manifest/public metadata identities against receipt fields as well as hashes; changing only a receipt URL/repository must not pass against unchanged control bytes. Use retained paths, not current-checkout controls. Record actual creation and invocation timestamps without inventing a creation time to hide clock inconsistency.
- [ ] Compile `tools/releasecheck` with CGO disabled for Linux amd64 and arm64 in the build container into `/out/tools/releasecheck-linux-amd64` and `/out/tools/releasecheck-linux-arm64`. Hash these internal helpers into the receipt and exclude them from upload. Run helper verification in a token-free container. The separate credentialed publisher executes only trusted image tools and fixed validated shell; never execute a retained bundle helper with GitHub credentials. Validate and pin the actual native verification/publisher image ID against approved toolchain inputs, separately from the original builder platform/ID; allow cross-architecture retained-bundle verification. Mount only the seven allowlisted assets read-only for upload.
- [ ] Make aliases delegate to the script, with consistent variables: `ENV`, `REF`, `TAG`, `DIST`, `NOTES_FILE`, `FROM_STAGE_TAG`, `STAGE_ACCEPTANCE`. The acceptance path applies only to the operator's production publication workflow, not ordinary CLI use. Preserve `release-check` as an alias but require explicit ENV. Replace current `stage-release` policy with a thin fixed-stage call; add `prod-release` fixed-prod. Fail on contradictory ENV supplied to a fixed-environment target.

**Tests:** isolated Git fixture repositories and fake Docker/GitHub executables record operations. Assert snapshot/check/build do not create/delete/change tags, fetch, push, or publish even when token variables exist in the parent environment. Read-only tag inspection is allowed: pinned GoReleaser itself uses `git tag -l` and `git tag --points-at`. Missing env, dirty tree, invalid tag, tag not at selected source, incompatible toolchain, duplicate output path, and failed tests must fail without remote writes. Real integration builds remain in Docker; the fake harness validates operation boundaries rather than YAML text.

**Compatibility:** old `make stage-release SERVER_URL=...` without explicit TAG/approved manifest now fails with migration guidance. The new tagged build intentionally does not create a missing tag. New local source/output copies are temporary; retained artifact parents survive failure for diagnosis and resume.

### Task 8 — Implement exact-artifact publication and recovery

**Files:** extend `scripts/release.sh`, `scripts/release_test.sh`, `tools/releasecheck/receipt.go`, `tools/releasecheck/receipt_test.go`; create `docs/releases/RUNBOOK.md`. **Dependencies:** Task 7 and required acceptance evidence; real repository/URLs/version must be supplied before an actual run.

- [ ] Read GitHub state and fetched branch/tag identities before mutation. Parse annotated tags by object and peeled commit, validate exact repository identity against the effective fetch and push URLs (including pushurl and URL rewrites), and detect an existing complete release early. Reject multiple destinations and mirror configuration; do not assume raw remote.origin.url is the actual push destination. Set `GIT_TERMINAL_PROMPT=0`; do not require an interactive credential prompt midway through a release.
- [ ] Require `GITHUB_TOKEN` for publish, resume, and status. Publish/resume require write-capable credentials; status is read-only but always requires a read-capable token so the isolated GitHub query can distinguish drafts from absence. Reject absence early; use a read-only repository/release query to check access before long work. Fine-grained PAT needs Contents: write on this repository, or future Actions `contents: write`. Do not assume a GitHub release token automatically configures SSH Git push access.
- [ ] Keep Git push using the maintainer's already configured SSH/credential helper; CI can use a temporary scoped HTTPS helper. Never embed the token in a remote URL, arguments, release notes, or build command. Disable shell tracing. Pass the token by environment name into the publisher-only container (`-e GITHUB_TOKEN`), without echoing it or mounting user credential directories into the builder.
- [ ] Obtain a local per-repository publication lock (`mkdir` with owner metadata, trap cleanup; no automatic stale-lock theft). Before creating any remote state, run feasible checks/tests, create or verify the local annotated tag at the pinned SHA, build the tagged bundle token-free, verify it, collect required native evidence, and prepare reviewed release notes. Reuse an existing remote tag object exactly; equality of peeled commits does not authorize a different annotated tag object. Creating a local tag is allowed only in publish, not snapshot/build. No published tag is ever moved. Persist initial publication identity and completed eligibility checks atomically before tag push so a lost push response followed by branch advancement remains recoverable.
- [ ] If `--dist` supplies a prebuilt tagged bundle, validate its source-selected manifest/toolchain/configuration, notes linkage, and all required evidence; publish those bytes without rebuilding. Do not substitute the invoking checkout's newer controls during recovery. An explicit current-policy prohibition may refuse recovery with an explanation, but must not silently rebuild or change identity. Snapshots are ineligible. Retain the receipt and hash of the notes used.
- [ ] Production requires both `--from-stage-tag TAG` and `--stage-acceptance PATH`. Define a small operator record with schema version, repository, exact stage tag object/commit, completed release ID, exact seven asset names/sizes/hashes, accepted flag, reviewer, UTC acceptance time and named successful staging checks. A token-free helper can generate the identity-bearing JSON template from the retained completed-stage records; it starts unaccepted. The operator records actual staging checks before marking it accepted. Validate structure and remote stage identity/assets before prod mutation; never infer staging acceptance from upload or local smoke alone. Retain the exact acceptance record and its hash with the production publication record so resume does not rediscover a path among old build directories. This records operator evidence, not automated proof that a human ran a check.
- [ ] If a newly built bundle lacks required native evidence, exit before tag push with its retained parent and complete requirements, smoke-collection, manual-record, aggregation, and `--dist` retry instructions. After reports are collected on the needed hosts, rerun publish with `--dist`; this reuses the local tag and original artifacts. Waiting for external evidence never grants permission to skip the gate.
- [ ] Recheck tag/branch/repository/channel immediately before remote writes. Push the pinned tag object to the sole explicit `refs/tags/<validated-tag>` destination, without force or `--tags`, explicitly suppressing follow-tags and pruning. Ambient Git configuration must not expand the push or redirect it to another repository. Then create a draft release with `--verify-tag --latest=false`; stage is a prerelease, prod is not. Creation collision is a stop, not permission to append blindly to another publisher's draft.
- [ ] Upload exactly seven allowlisted files; no glob over all dist files and no `--clobber`. Download/list every uploaded asset and compare local/remote name, length, and SHA-256 before leaving draft. Record release ID and asset IDs in a separate atomic `publication.json`; the build receipt remains unchanged.
- [ ] Finalize stage with prerelease=true/latest=false. Finalize prod with prerelease=false/latest=true only after comparing against existing stable SemVer and rejecting an older/equal conflicting release. Recheck latest immediately before finalization. A deliberate historical/backport release must use a separately documented latest=false policy; do not silently demote the current stable default.
- [ ] Implement the recovery table in section 7. Ordinary publish encountering an existing draft does not mutate it; explicit resume with the original parent is required. Identical complete publication reports already complete without uploads or notes edits. Successful artifacts are immutable.

Relevant containerized GitHub CLI commands (variables are validated and quoted by the script):

```sh
# After the explicit tag push and all local gates:
gh release create "$tag" --repo "$repository" --verify-tag --draft \
  --prerelease --latest=false --title "$tag" --notes-file /out/notes.md
# Production creation uses --prerelease=false instead.

# Explicit file arguments come from the verified seven-asset manifest.
gh release upload "$tag" <seven-validated-asset-paths> --repo "$repository"

# Only after remote asset verification:
gh release edit "$tag" --repo "$repository" --draft=false --prerelease --latest=false
# Production finalization:
gh release edit "$tag" --repo "$repository" --draft=false --prerelease=false --latest=true
```

`<seven-validated-asset-paths>` is explanatory notation, not a shell token to execute. `gh release create` can create a missing tag unless `--verify-tag` is used; that flag is mandatory. GitHub's release API distinguishes prerelease and make_latest, and immutable releases further constrain post-publication edits. Use supported command/API behavior, not GoReleaser Pro continuation. [Create](https://cli.github.com/manual/gh_release_create), [upload](https://cli.github.com/manual/gh_release_upload), [edit](https://cli.github.com/manual/gh_release_edit), [GitHub releases API and permissions](https://docs.github.com/en/rest/releases/releases).

**Tests:** mocked API/state transitions for missing token, denied read/write access, tag push failure, existing tag at wrong SHA, competing draft creation, each upload boundary, matching/mismatched existing assets, interrupted finalization, and old prod version/latest protection. No `--clobber`, force push, or automatic asset deletion is permitted. If GitHub permission failure cannot be detected read-only in advance, retain the built bundle and fail at the first denied mutation with clear resume guidance.

**Compatibility:** the release remains a GitHub release of GoReleaser-produced packages. Native GoReleaser SCM publishing is intentionally disabled to avoid a second artifact-producing invocation. A local lock cannot serialize an unrelated publisher on another machine; document one active release coordinator, use draft-creation collision checks, and use repository-wide CI concurrency later. Do not claim a distributed atomic latest transaction.

### Task 9 — Add artifact/native acceptance and complete operator docs

**Files:** create `scripts/smoke.sh`, `scripts/smoke.ps1`, `tools/releasecheck/smoke.go`, `tools/releasecheck/smoke_test.go`, `tools/releasecheck/native_evidence.go`, `tools/releasecheck/native_requirements.go`, `tools/releasecheck/path_safety.go`, `tools/releasecheck/cli_integration_test.go`, and `docs/releases/NATIVE_CHECKS.md`; extend `scripts/release.sh`, `scripts/release_test.sh`, `tools/releasecheck/main.go`, `tools/releasecheck/publication.go`, `tools/releasecheck/publication_test.go`, `tools/releasecheck/artifacts_test.go`, `docs/releases/INSTALL.md`, and `docs/releases/RUNBOOK.md`; modify `README.md` and status notes in the five existing spec files and three existing plan files identified in section 1. **Dependencies:** Tasks 1–8.

- [ ] Native smoke scripts extract/execute the selected already-built archive by its absolute extracted binary path, never PATH lookup. Isolate HOME/config and Windows USERPROFILE, remove inherited application credentials/assertions/config overrides, close stdin, and impose finite command timeouts. Collect receipt SHA-256, exact archive basename/SHA-256, executed binary SHA-256, target/collector/host architecture facts, both version/help exit statuses and timeout outcomes, and separate raw stdout/stderr. Each invocation uses a fresh report directory and retains failures. They compile nothing. Support Bash 3.2 `scripts/smoke.sh --dist PARENT --environment ENV` on Mac/Linux and Windows PowerShell 5.1+ equivalents; an optional target selector may support deliberate supplemental runs.
- [ ] Keep collection portable: fixed scalar records plus raw command output can be parsed later by the token-free Docker helper. Do not introduce host Go, jq, Python, or Add-Type compilation. PowerShell 5.1 redirection/Out-File defaults to UTF-16LE: capture process bytes or deliberately encode UTF-8 without BOM using built-in .NET. Keep script text compatible with that baseline. [Microsoft encoding documentation](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_character_encoding).
- [ ] Record native evidence separately beside the immutable receipt/artifacts; validate its archive/binary/receipt identity and all nine version fields in the container. Distinguish target, collector process and host architecture, provenance, and `native|emulated|unknown`. Docker's outer launcher supplies daemon architecture plus selected container platform; matching in-container uname to the binary is insufficient. On Mac, the collector's Rosetta status is not automatically the tested thin binary's status. Windows process architecture and older .NET OSArchitecture may reflect emulation; uncertain facts remain unknown unless documented operator provenance resolves them without overriding contradictions. Reports can be copied from other native hosts manually before CI exists. [Microsoft OSArchitecture limitations](https://learn.microsoft.com/en-us/dotnet/api/system.runtime.interopservices.runtimeinformation.osarchitecture).
- [ ] Define the required targets/checks and first-support, routine or affected-platform rationale before the publication consumer evaluates reports. Missing reports never shrink requirements. Keep prior support-baseline references distinct from current-binary observations, and identify each actual check: version/help evidence does not prove config privacy or input/signal behavior. Successful emulated runs can observe metadata but cannot satisfy native requirements. This is an operator evidence record, not cryptographically attested host identity.
- [ ] Implement `releasecheck native-requirements --dist /out` to produce `native/requirements.json` bound to the receipt, with all six explicit target rows and conservative first-support requirements. Implement the `native-review-template` helper and shell command to validate the current and first-support retained trees, then create a conservative, create-exclusive routine-review template in an existing directory outside both trees. It leaves human fields blank, marks every target affected and unavailable, and includes all six named checks; the operator must review every row before `native-requirements` accepts it. The optional `--review /review.json --baseline-dist /baseline` pair selects a reviewed routine policy before publication: validate one concrete prior bundle with actual successful native version/help observations across all six targets, its required checks and exact receipt/archive/binary identities; import its receipt, seven assets and necessary evidence into `native/baseline/`, excluding nested baselines. Do not build an inheritance chain. The review records operator, cumulative baseline/current commits, rationale, declared available targets and required named checks for each target. Fixed names are `version`, `help`, `network`, `config-private`, `password-input`, `signal-cancel`. Require current version/help for every declared available target and every affected platform, plus the affected behavioral checks; existing release-specific behavior gates remain in force. Availability/change impact/manual checks are explicit operator attestations, while identities, complete target coverage, statuses, raw output and provenance consistency are machine-validated. Empty routine rows mean no current native check required, never passed. Invalid reviewed inputs fail; absence of an explicit valid routine review means first-support. Bind requirements/evidence hashes in initial publication state before mutation, then reject changed inputs. Manual behavioral evidence must identify the exact artifact, check, procedure, result and operator; a generic tested note is insufficient.
- [ ] Add Linux-container integration fixtures to exercise actual release binaries against isolated local TLS API/WS servers: disable external networking, map the approved endpoint host(s) to the fixture address, generate a fixture CA/certificate for those hosts, and trust it only through the test process's certificate-file environment. Serve the approved base paths/ports. Never disable TLS verification, modify global trust, or add a production runtime URL override. Use sentinel environment-specific keys and projects, and assert the correct host/path/key/topic is used. This verifies embedded URLs without calling production. Label its test-local records `local-tls-phoenix-v1`; they are never publication inputs and never satisfy `network_release_v1`.
- [ ] Keep simpler unit/command fixtures for bad config/mismatched environment/no network. The integration fixture must not weaken the publisher's approved-URL validation with an undocumented test override.
- [ ] Document native installation and uninstall, explicit stage downloads versus stable latest, checksums, side-by-side names, config migration, token setup through a secret manager, and the recovery table. Link README to the runbook instead of duplicating release logic.
- [ ] Write a short release note for the migration break and known unsigned Mac status. Release notes may be supplied as `NOTES_FILE` outside the source checkout, copied/hash-recorded into the output parent; this avoids changing the tested source merely to write production release notes.

**Verify:** the section 8 matrix passes with exact recorded commands/outcomes. First production support claim needs native version/help evidence for all six targets, collected manually if necessary; lack of a host is explicitly recorded as unverified and not silently treated as a pass. Routine releases repeat build/checksum/metadata and available-host smoke checks; changes to networking, terminal, config, dependencies, or toolchain require the affected native-platform checks again. A future CI matrix automates this evidence without changing artifact creation.

**Compatibility:** no additional native package manager or installer is required. Updating historical documents means a superseded banner/link, not rewriting history to pretend an old design matched current code.

### Task 10 — Optional, separately reviewable cleanup

**Files:** `cmd/root.go`, `internal/config/config.go`, `cmd/listen.go`, `cmd/listen_test.go`, `internal/printer/printer.go`, `cmd/errors.go`, affected tests/README; `go.mod`/`go.sum` only for a deliberate terminal dependency update. **Dependencies:** core release tasks and clean verification; not a publication prerequisite by itself.

- [ ] Deprecate the unused log-level flag, remove its advertised setting and dead persistence plumbing, and tolerate old config entries. Do not implement speculative logging infrastructure.
- [ ] Remove the test-only production formatter wrapper after migrating its tests to the actual implementation; remove unused internal printer Mode fields/aliases and evaluate the unconsumed command error kind. Preserve DTO/interface/linker/init usages.
- [ ] Update x/term in a focused change and test Windows/TTY behavior; separately decide whether the archived Survey dependency needs replacement. Keep project prompt behavior stable.
- [ ] Consider a command factory only if more full-command tests justify it; avoid broad command-global refactoring in the release wiring patch.

**Verify:** behavior tests and staticcheck, `go mod tidy -diff`, vulnerability scan for dependency changes, and native terminal smoke where affected. No new tests solely to assert that a removed unused field is absent.

## 7. Local command examples and release runbook

### Existing versus proposed commands

**Existing today:** `make test`, `make vet`, `make build SERVER_URL=...`, `make run SERVER_URL=... ARGS=...`, `make release-check`, and `make stage-release SERVER_URL=... GITHUB_TOKEN=...`. The last command creates/pushes a tag and publishes; it was not run in this review. Do not use its inline token form as new documentation.

**Proposed after implementation:**

| Make interface | Purpose | Creates/pushes tags or publishes? | Token needed? |
| --- | --- | --- | --- |
| `make release-tools` | Build local pinned tool image | No | No |
| `make release-check ENV=stage` | Validate manifest, selected environment, tools, and GoReleaser config | No | No |
| `make release-snapshot ENV=prod REF=HEAD` | Build/package snapshot of selected committed source | No | No |
| `make release-build ENV=stage TAG=v0.1.0-stage.1` | Build/package an existing exact local tag | No | No |
| `make release-verify ENV=prod DIST=/absolute/output/parent` | Verify a retained bundle's immutable artifact and receipt bytes | No | No |
| `make stage-release TAG=v0.1.0-stage.1 REF="$FULL_COMMIT" NOTES_FILE=/absolute/notes.md` | Full stage preflight, tagged build, draft upload, finalize prerelease | Yes, only after gates | Publishing token plus existing Git push access |
| `make prod-release TAG=v0.1.0 REF="$FULL_COMMIT" FROM_STAGE_TAG=v0.1.0-stage.1 STAGE_ACCEPTANCE=/absolute/operator-records/stage-acceptance.json NOTES_FILE=/absolute/notes.md` | Rebuild same staged source for prod, verify, publish stable | Yes, only after gates | Same |
| Add `DIST=/absolute/output/parent` to either publishing target | Publish that already-verified tagged bundle, without rebuilding | Publication only; verifies/pushes exact tag if needed | Same |
| `make release-status ENV=stage TAG=v0.1.0-stage.1 [DIST=/absolute/output/parent]` | Read release state; with `DIST`, also verify retained identity and the exact/absent remote tag | No | Read-capable GitHub token |
| `make release-resume ENV=stage TAG=v0.1.0-stage.1 DIST=/absolute/output/parent` | Explicit recovery of the recorded release using retained bytes | May create missing release/upload missing assets/finalize | Yes |

`REF` is shell notation for an existing full commit/ref, not an arbitrary string passed to eval. `TAG` is always explicit for tagged operations; `GORELEASER_CURRENT_TAG` prevents ambiguity when stage and prod tags point to the same commit. New tags created by publish are annotated. An existing correct tag is never converted or moved just to change its annotation style.

Example local nonpublishing sequence after committing the implementation and populating the approved manifest:

```sh
make release-tools
make release-check ENV=stage
make release-check ENV=prod
make release-snapshot ENV=stage REF=HEAD
make release-snapshot ENV=prod REF=HEAD
# The script prints each distinct retained output parent.
make release-verify ENV=stage DIST=/absolute/path/printed/by/stage/build
make release-verify ENV=prod DIST=/absolute/path/printed/by/prod/build

# Only when this exact tag already exists locally:
make release-build ENV=stage TAG=v0.1.0-stage.1
```

No token is necessary for these operations, including a repository that already has tags. Source/module downloads may need ordinary network access; that is independent of GitHub publishing authorization. The implementation should provide a dependency-cache warmup followed by network-disabled artifact verification, without silently skipping missing dependencies.

### Publishing sequence

1. Supply and review real URL/repository inputs, resolve the committed credential's disposition, and inventory remote version history. Choose the next base version and staging candidate number. Prepare release notes outside the source tree if needed so the tested commit stays fixed.
2. Check out/resolve the intended committed source with a clean worktree. Run the nonpublishing validations and available snapshot/native checks first. Set publishing credentials through the approved secret manager or current shell environment; never paste them into Make arguments or notes. The script passes `GITHUB_TOKEN` by name into the publisher and does not supply any application CLI key to builds.
3. Invoke `make stage-release ...` with explicit TAG/REF/NOTES_FILE. Read-only preflight checks repository access, branch/source, remote tags/releases, and version policy before substantial work. Feasible tests/build checks finish before the first remote mutation. A local annotated tag may be created after initial checks so a real tagged build can validate exact release metadata.
4. Complete tagged packaging, artifact verification, and required native evidence. Immediately before mutation, recheck source/tag and remote state. Push only the selected tag, create a draft, upload exactly seven assets, download and compare them, then finalize stage as prerelease/latest=false. Retain the entire output parent and receipt.
5. Exercise the actual staging service using environment-specific credentials outside build/publication logs. Record the tested stage tag/commit, then generate and deliberately complete the create-exclusive stage-acceptance record. This authenticated deployment smoke is an operator check, not a build hook and not something performed in this review.
6. Merge without losing the tested commit, or stage the new resulting commit if identity changes. Invoke `make prod-release TAG=... REF="$FULL_COMMIT" FROM_STAGE_TAG=... STAGE_ACCEPTANCE=/absolute/operator-records/stage-acceptance.json NOTES_FILE=...` with the same source SHA and base version. Prod must rebuild with the prod endpoint/name/config identity; repeat artifact and relevant native/environment checks. Finalize only after the asset set is complete and latest policy passes.
7. Inspect the public stable/latest download and explicit staging release page after publication. Keep original artifacts, receipt, notes, and native evidence through the release's support lifetime, at minimum 90 days while the retention policy is established. Do not infer success solely from the local uploader exit status.

Preflight ordering is intentionally explicit: **input/tool/access checks → source/tag/branch policy → tests → local tag if needed → token-free tagged build → artifact/native checks → remote-state recheck → exact tag push → draft → upload/remote verification → publish**. Some write-permission/network failures can only be discovered during mutation; recovery preserves prior successes.

### Recovery policy

| Observed state | Safe action |
| --- | --- |
| Validation/test/build failure, no remote state | Fix source, commit, and rerun preflight. Preserve failed output for diagnosis. A local-only tag can be inspected by the operator; never automatically move a tag to new code. Use a new candidate tag for changed source. |
| Local/remote tag exists at the intended SHA, no release | Reuse the tag, verify retained tagged bundle, and create only the missing draft. Ordinary nonpublishing build may rebuild into a fresh parent, but publication must not substitute bytes for successful remote assets. |
| Tag resolves to another SHA | Abort. Do not force push, delete, or retarget it. Select a new version/candidate after review. |
| Draft exists with a matching receipt and partial assets | Only `release-resume` may proceed. Recheck tag/environment/repository, download existing assets, compare digests, skip identical assets, and upload missing ones from the original parent. |
| Draft has mismatched/unexpected/zero-byte or starter assets | Stop and identify the exact asset/state. Do not delete/replace automatically. Operator may inspect and explicitly remove a failed placeholder, then resume, or choose a new release tag. |
| Release is already published with the identical complete set | Read-only success/already-complete result. No notes edit, asset upload, rebuild, or latest toggle. |
| Published release has different/incomplete assets | Treat as an incident. Do not mutate successful published assets silently; assess repository immutability and choose an explicitly reviewed recovery or a new corrective release. |
| Network fails after finalization request | Query release/draft state and verify assets before retrying anything. A lost response may still mean publication succeeded. |
| Original bundle is lost | Recover the original from retention. Do not rebuild and overwrite known successful files under the same tag on an assumption of reproducibility. If recovery cannot establish identity, use a new tag. |
| Another publisher owns a local lock or wins draft creation | Stop with owner/release identity. Do not auto-take over; use explicit status/resume after coordination. |

### Installation instructions to ship

Use an explicit release page/tag for staging. Production can use GitHub's stable latest page, but installation instructions should still show the downloaded version and checksum. Download the single matching archive and that environment/version's checksum file. Compare the archive SHA-256 against its named entry **before extraction**; with only one archive downloaded, do not run a whole-manifest check that fails merely because the other five archives are absent.

On macOS/Linux, use `shasum -a 256 ARCHIVE` or `sha256sum ARCHIVE`, compare the full digest to its exact checksum entry, then extract into a temporary directory. Install the appropriate executable with `install -m 0755 hookspot[-stage] "$HOME/.local/bin/hookspot[-stage]"`, ensure that directory is on PATH, and run `version --json` and `--help`. README must explain that the bracket notation represents two actual names, not a literal shell command.

On Windows, use `Get-FileHash -Algorithm SHA256`, compare to the matching checksum entry, and use `Expand-Archive`. Place `hookspot.exe` and/or `hookspot-stage.exe` in a user-owned PATH directory. Run each one's version/help in PowerShell; do not equate the ZIP's stored Unix mode with Windows permission semantics. Uninstall removes the selected executable; config removal is a separate explicit user action.

The release notes and archive INSTALL must explain the configuration migration and absence of Developer ID signing/notarization for macOS. Do not make package managers, administrator privileges, globally installed Go, or a publishing token prerequisites to installing/running a downloaded CLI.

## 8. Verification matrix and measurable acceptance criteria

### Checks actually performed during this review

All Go build/test/analysis commands ran in Docker. Host-side actions were read-only source/archive inspection and execution of already-built binaries. Host was macOS 26.5.2 on ARM64; Docker server was Linux ARM64 under OrbStack (Docker client 29.4.0). Initial sandbox Docker access required escalation. An initial network-disabled test attempt failed because dependency cache entries were missing; a subsequent network-enabled container populated its caches and the full check completed. No host Go/global tool install was performed.

**Tool/config inspection:**

```sh
docker run --rm --network none --entrypoint sh goreleaser/goreleaser:v2.17.1 \
  -c 'goreleaser --version && go version && goreleaser release --help && goreleaser check --help'
```

Observed GoReleaser 2.17.1 and contained Go 1.26.5. Current YAML schema check succeeded without publishing credentials. The check command accepts positional configuration files: `goreleaser check .goreleaser.yaml`; do not use release's `--config` option as an assumed check flag. A temporary proposed YAML also passed `goreleaser check /audit/proposed.goreleaser.yaml` in this image; its unimplemented helper hooks were not executed.

**Existing tests, coverage, vet, formatting, module consistency:**

```sh
docker run --rm -v "$PWD":/src:ro -w /src \
  -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build \
  -e GOTOOLCHAIN=local -e GOFLAGS=-mod=readonly golang:1.26.5 \
  sh -c 'go version && go test -count=1 -coverprofile=/tmp/hookspot-coverage.out ./... && go tool cover -func=/tmp/hookspot-coverage.out && go vet ./... && gofmt -l main.go cmd internal && go mod tidy -diff && go mod verify'
```

Exit 0: all six packages with tests passed; main has no tests; vet had no diagnostics; gofmt listed no files; tidy produced no diff; module verification passed. Coverage showed meaningful gaps in command execution/configuration (cmd 57.5%, config 58.6%); percentages alone are not acceptance criteria. Existing tests are useful but do not cover release behavior, real command-global initialization, redirect credential handling, or environment isolation.

**Race detection:**

```sh
docker run --rm --network none -v "$PWD":/src:ro -w /src \
  -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build \
  -e GOTOOLCHAIN=local -e GOFLAGS=-mod=readonly golang:1.26.5 \
  go test -race -count=1 ./...
```

Exit 0 for all test packages. This is native Linux ARM64 race testing, not macOS/Windows race testing. Successful race tests do not cover unexercised cancellation/writer failure paths.

**Static and vulnerability analysis:**

```sh
docker run --rm -v "$PWD":/src:ro -w /src \
  -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build \
  -e GOTOOLCHAIN=local -e GOFLAGS=-mod=readonly golang:1.26.5 \
  go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...

docker run --rm -v "$PWD":/src:ro -w /src \
  -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build \
  -e GOTOOLCHAIN=local -e GOFLAGS=-mod=readonly golang:1.26.5 \
  go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...
```

Staticcheck exited 0 with no findings. The module v0.8.1 corresponds to Staticcheck 2026.2.1; the 2026 release line supports Go 1.26, and this exact command ran successfully. Govulncheck v1.7.0 was also compatible enough to analyze this source/toolchain successfully; it failed its vulnerability gate, not tool startup. These commands download tools into container/module caches, not the host's global bin directory. [Staticcheck Go 1.26 support](https://staticcheck.dev/changes/2026.1/), [Staticcheck release](https://github.com/dominikh/go-tools/releases/tag/2026.2.1), [govulncheck source tag](https://github.com/golang/vuln/tree/v1.7.0).

Govulncheck returned scanner exit 3 (reported by `go run` as exit 1) for four reachable standard-library reports, all found in Go 1.26.5 and fixed in Go 1.26.6:

| Report | Scanner evidence and interpretation |
| --- | --- |
| [GO-2026-6218](https://pkg.go.dev/vuln/GO-2026-6218) | net/url path-resolution complexity; reported trace from `internal/proxy/proxy.go:150` through HTTP client use. |
| [GO-2026-6090](https://pkg.go.dev/vuln/GO-2026-6090) | crypto/tls post-handshake message bounds; reported paths include WS dial and proxy HTTP/TLS. |
| [GO-2026-5972](https://pkg.go.dev/vuln/GO-2026-5972) | encoding/asn1 recursion bound; reported reachable standard-library path. Reachability does not prove that Hookspot input can exploit it. |
| [GO-2026-5026](https://pkg.go.dev/vuln/GO-2026-5026) | net/http's vendored IDNA handling; reported through proxy HTTP client use. |

An additional run with `-show verbose` confirmed 21 modules including the root module and stdlib analysis. It listed GO-2026-6091, GO-2026-6089, and GO-2026-5942 at package level, and GO-2026-6088 at module level, without a reachable vulnerable symbol in this code. Do not inflate these into eight proven exploitable application defects. The proposed 1.26.8 image was not built/tested in this analysis; clearing the scanner is an implementation acceptance gate.

**Fresh current-config snapshot:**

```sh
docker run --rm --network none -v "$PWD":/src:ro \
  -v /tmp/hookspot-release-review.4V5uV0:/audit \
  -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build \
  -e GOTOOLCHAIN=local -e GOFLAGS=-mod=readonly \
  --entrypoint sh goreleaser/goreleaser:v2.17.1 -c \
  'mkdir /work && tar -C /src --exclude=./dist --exclude=./tmp -cf - . | tar -C /work -xf - && cd /work && SERVER_URL=https://stage.example.invalid GORELEASER_CURRENT_TAG=v0.1.0-stage.1 goreleaser release --snapshot --skip=publish --timeout=10m && cp -a dist /audit/current'
```

Exit 0. `stage.example.invalid` was an inert diagnostic URL, not a proposed real endpoint. The synthetic current-tag input did not create a Git tag. GoReleaser explicitly skipped publishing; no token was injected. This built the unchanged current code, not the proposed architecture, into a disposable source copy and `/tmp`; existing repository `dist/` remained untouched.

Observed snapshot version `0.1.0-stage.1-SNAPSHOT-f8e35de`; six archives plus checksums; all six SHA-256 values verified; four tarballs/two ZIPs; Mach-O ARM64/x86-64, static ELF ARM64/x86-64, and PE ARM64/AMD64 matched their filenames. Each archive contained current README mode 0644 and executable mode 0755 (ZIP attributes do not establish Windows ACLs). The diagnostic endpoint string was present; that string check does not replace a request-level endpoint test.

Native `version` and `--help` passed for the built darwin/arm64 binary on this Mac and linux/arm64 binary inside Docker. `go version -m` inspection confirmed the build's Go version, source revision, and CGO-disabled metadata. `codesign -dv --verbose=2 /tmp/hookspot-release-review.4V5uV0/current/hookspot_darwin_arm64_v8.0/hookspot` reported an ad-hoc linker signature with no TeamIdentifier. Version with a malformed explicit config failed, confirming R11.

The temporary diagnostics in `/tmp/hookspot-release-review.4V5uV0/audit_test.go` were executed with:

```sh
docker run --rm --network none -v "$PWD":/src:ro \
  -v /tmp/hookspot-release-review.4V5uV0:/audit:ro \
  -v hookspot-gomod:/go/pkg/mod -v hookspot-gocache:/root/.cache/go-build \
  -e GOTOOLCHAIN=local -e GOFLAGS=-mod=readonly golang:1.26.5 \
  sh -c 'mkdir -p /work/tools/audit && cp /src/go.mod /src/go.sum /work/ && cp -R /src/internal /work/ && cp /audit/audit_test.go /work/tools/audit/ && cd /work && go test -count=1 -v ./tools/audit'
```

All four probe tests passed by **reproducing** R5, R6, R7, and R13. They used sentinel credentials and loopback servers, released blocked resources afterward, and were not added to production/test source in the repository. They are evidence of defects, not passing regression tests for fixes.

### Execution limits and unavailable material

- No tagged nonpublishing build was run: no valid current-format tag was available locally, and creating tags was forbidden. Snapshot cross-compilation does not prove the proposed tagged gate or publication behavior.
- No stage/prod environment-specific implementation exists yet. Proposed URL validation, config isolation/migration, fixed runtime Docker image, patched Go 1.26.8 build, helper hooks, upload/recovery scripts, and acceptance tests are unrun future work.
- darwin/amd64, linux/amd64, and both Windows binaries were inspected but not run natively. No Windows ARM64 host, Intel Mac host, notarization credentials, or authenticated backend/release configuration was available for this review. Cross-compilation is not runtime certification.
- No real application key was validated, no remote tag/release created, no tag pushed, no GitHub release published, and no API/WS call made to a real stage/prod backend by the diagnostic fixtures.
- No runtime-image CA/security scan or actual browser-downloaded quarantine test was performed. The inspected local Mac binary had no browser quarantine provenance.
- Required Hookdeck/config/version/workflow/docs content was retrieved successfully from pinned GitHub source. Some browser requests failed/cache-missed and were recovered with raw-source retrieval. Apple's JavaScript-heavy notarization page did not expose full content to the browser reader; the pinned GoReleaser guide and Apple support page supplied the limited signing context above. Backend limits/contracts, actual endpoint ownership, signing account availability, and GitHub repository rules remain external inputs.

### Planned verification matrix

| Gate | Exact intended check/interface | Meaningful acceptance condition |
| --- | --- | --- |
| Formatting/modules | Container `gofmt -l main.go cmd internal tools`; `go mod tidy -diff`; `go mod verify` | No formatting output or module drift; versions agree; no host build. |
| Unit/behavior tests | Container `go test -count=1 ./...` | Existing behavior remains covered; new environment, config, URL, cancellation, and output failures pass. |
| Vet/race | Container `go vet ./...`; native Linux container `go test -race -count=1 ./...` | No diagnostics/races in exercised paths; explicitly record runner/architecture. |
| Static/dependencies | Pinned container Staticcheck and govulncheck commands above, using Go 1.26.8 | Compatible tools execute; no unresolved reachable vulnerability report; unused-code claims also have call/data-flow evidence. |
| GoReleaser schema | `make release-check ENV=stage` and `ENV=prod` | Both valid inputs pass; unknown/missing/malformed/mismatched inputs fail before build. A schema pass alone is insufficient. |
| Full snapshots | `make release-snapshot ENV=stage REF="$FULL_COMMIT"` and prod equivalent | Six targets each, no publishing token, no tag/push/publication; environment names and endpoints correct. |
| Tagged packages | `make release-build ENV=stage TAG=v0.1.0-stage.1` and prod equivalent | Exact chosen tag/SHA, expected version/kind, all six targets; no tag creation/push/release. Same-SHA multiple tags do not confuse selection. |
| Artifact integrity | `make release-verify ENV=... DIST=...` | Exactly six archives/seven upload assets; correct Mach-O/ELF/PE machines, executable names/modes, five members, SHA-256, metadata; reject tampered/missing/extra/path-traversal assets. |
| Sequential outputs | Build stage then prod and compare stage hashes before/after | Different retained parents; stage files unchanged; no `--clean` over common dist. |
| Native smoke | `scripts/smoke.sh` / `scripts/smoke.ps1` on all six native targets for first support validation | Version/help exit 0, metadata matches actual OS/arch, no credential/config/network requirement. Emulation is marked separately. |
| Embedded endpoint behavior | Container fixture in Task 9 against actual compatible Linux binaries | Correct scheme/host/base path/API/WS query, correct environment key/topic; TLS verified using fixture CA; no external network. |
| Config/migration | Container unit/subprocess tests plus native Windows filesystem tests | Stage/prod files and project selections stay independent; explicit path/env precedence works; mismatches stop before network; no unintended key persistence; migration is explicit and nondestructive. |
| Source/tag failures | `scripts/release_test.sh` isolated Git fixtures | Dirty tree, wrong branch/source, unknown tag, mismatched channel, missing stage evidence, changed prod SHA, and moved remote tag fail without unauthorized writes. |
| Publication failures | Script mock GitHub/publisher fixtures | Missing token fails before build/mutation; partial draft resumes missing identical files only; existing published release is idempotent; no silent artifact replacement/latest contamination. |
| Native lifetime/terminal | Unix signals, Windows Ctrl-C, interactive login/project/replay | Cleanup is bounded; key entry is not echoed; output escaping and project precedence are correct. Supported-platform limitations are documented honestly. |
| Distribution docs | Follow archive INSTALL from a fresh user account/directory | No Go/global tools required, checksum checked before execution, stage/prod coexist, migration and unsigned Mac caveat are discoverable. |

### Measurable completion criteria

1. A clean checkout with Docker/Git/Make can validate and build stage or prod without a GitHub token, global Go, or CI; generated binaries/packages come only from Docker compilation.
2. Both environments produce six correctly named archives and one six-entry checksum file, with environment-specific executable/config identity and complete public metadata. No credentials/config files/build logs enter the archives.
3. The Go version in every development/release build path is the approved patched version; tests/vet/race/staticcheck and vulnerability checks have recorded outcomes. Proposed checks not executed cannot be labeled passing.
4. All source/tag/environment/URL/config mismatch cases fail before network authentication or remote publication as applicable. Snapshot/build commands demonstrably never create/push tags or publish, even with ambient tokens.
5. Stage publication always has prerelease=true and latest=false. Prod uses the stable source/tag policy, a rebuilt prod endpoint, and stable-only latest logic. Uploaded files exactly match the retained verified hashes.
6. Build/upload failure retains usable evidence. Explicit resume never moves a tag or overwrites an already successful asset. An existing complete identical release is a read-only no-op.
7. Native smoke coverage and limitations are recorded per target. First production support validation covers all six natively; signing/Gatekeeper expectations match the actual Mac distribution process.
8. Only the concrete defects needed for environment safety, build security, reliable lifecycle, and documented distribution are fixed in core work. Optional dependency/UI/unused-code cleanup stays separately reviewable.

## 9. Future CI migration and optional follow-ups

### Minimal future GitHub Actions structure

Initial implementation adds **no workflow file**. Once local commands work, a future `.github/workflows/release.yml` can invoke them without duplicating tag parsing, URL policy, matrix definitions, upload logic, or recovery rules.

Start with `workflow_dispatch` inputs `environment`, `tag`, and `from_stage_tag` when applicable. Require an existing exact tag for the first CI integration; CI does not implicitly manufacture a tag from a branch tip. Check out that tag's peeled commit, fetch full history/tags and the approved stage/main refs, and verify SHA equality. Checkout action must use `fetch-depth: 0`; pin reviewed immutable action revisions at adoption time. Never check out a containing branch after resolving a tag. A future `push: tags: [v*]` trigger must use the same strict tag/environment validator and reject legacy/unknown conventions; the presence of a tag is not a backend selector.

| Future job | Runner and responsibilities | Shared interfaces |
| --- | --- | --- |
| validate/build | Linux amd64 or ARM64 with Docker; default `contents: read` job permission; exact full-history tag checkout; approved manifest/tool image; no GitHub token or publishing credentials in the build container | `make release-check ENV=...`; container analysis; `make release-build ENV=... TAG=...`; `make release-verify ENV=... DIST=...` |
| native-smoke | Matching native Linux, Apple Silicon Mac, Intel Mac, Windows x64, and Windows ARM64 runners; download built archives/receipt, compile nothing | `scripts/smoke.sh` / `scripts/smoke.ps1`; retain evidence tied to binary/archive hashes |
| publish | After required smoke jobs; Linux with Docker/Git; exact source/tag checkout plus downloaded original bundle/evidence; narrowly scoped Contents: write token | `make stage-release ... DIST=...` or `make prod-release ... FROM_STAGE_TAG=... DIST=...`; no rebuild |

Use GitHub artifact retention for the full output parent, notes, receipt, and native evidence, with at least 90 days initially and an appropriate longer release archive policy. Keep build caches separate from retained release artifacts. Restrict access to any logs that could contain operational data; the build path should not have application credentials in the first place.

Use a repository-wide publication concurrency group, such as `hookspot-cli-publication`, with `cancel-in-progress: false`; serializing stage/prod publication also protects latest comparisons. Jobs should default to `contents: read`; grant `contents: write` only to the publication job. Use the Actions repository token if repository rules permit tag/release writes; otherwise a narrowly scoped GitHub App/fine-grained token is an external setup input. Signed-in maintainer SSH agent assumptions must not leak into CI.

If CI uses an already remote tag, that tag necessarily predates this run's build gates; the script still performs all feasible checks before creating a GitHub release. Prefer local validation before a manually created/pushed tag. Do not claim a tag-triggered job can retroactively prevent its triggering tag from existing. The local end-to-end publisher continues to build before pushing its new tag.

When enabling automated publication, choose one active coordinator: do not leave both an automatic tag-triggered publisher and a local command racing to upload the same release. Local commands can remain useful for builds/status/explicit recovery; the same state checks apply. Repository rules/protected environments may add review gates, but those are deployment policy choices, not hidden requirements of the scripts.

Mac/Windows native runners need only run downloaded binaries and test filesystem/console behavior; Go compilation remains in Docker on the build runner. Intel Mac and Windows ARM64 runner availability is an external constraint; use suitable self-hosted hosts if the account's hosted runner catalog does not provide them. Do not substitute emulated testing without labeling it. Optional conventional Apple signing needs a Mac runner and ephemeral keychain/credentials, separate from the token-free builder.

### Optional follow-ups

- Developer ID signing/notarization, Windows signing, and browser-download installation verification when distribution audience requires them. Signing changes archive hashes, so it must occur before final checksum verification/publication.
- Homebrew, npm, Scoop, package installers, shell-completion packaging, and container registries only when demand warrants their extra manifests, credentials, version propagation, and tests. Hookdeck provides patterns, not a requirement to copy its distribution footprint.
- SBOMs, release attestations, signature files, independent reproducible-build comparisons, and supply-chain verification. If added, update asset counts/whitelists and acceptance criteria explicitly.
- A focused terminal-dependency refresh or command-constructor refactor after the safety/release work, with actual behavior coverage.

### Plan self-review

The design keeps environment, channel/version, and OS/architecture independent; requires stage and prod plus all six targets; preserves the immutable endpoint contract; covers config migration and explicit variable/path behavior; and never needs a token for local builds. It uses v2.17.1-verified OSS schema/flags, a literal safe dist path, and an explicit uploader instead of claiming unsupported continuation features. Publication ordering, immutable tags/assets, latest behavior, and partial recovery are specified separately from compilation success.

Confirmed defects have code or probe evidence; hypotheses and maintenance cleanup are labeled. Patched builds, proposed hooks/scripts, native platforms unavailable here, backend behavior, and signing are not represented as tested. External inputs remain empty/explicit rather than fabricated. Implementation changes, credential removal/rotation, tags, pushes, publication, and CI setup all remain future work.
