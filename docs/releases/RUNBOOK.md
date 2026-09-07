# Release runbook

Release publication always uploads the seven files with the exact bytes recorded by a verified tagged build. It never rebuilds during resume, moves a tag, replaces an asset, or publishes through GoReleaser.

Before the first real release, fill and review the stage and production URLs in
`release/environments.json`. Use one active release coordinator. Configure
ordinary Git push access/authentication separately. Inject `GITHUB_TOKEN` from
a secret manager only into status, publish, and
resume: status needs read access (including to a private repository), while
publish and resume need repository Contents write access. Never persist the
token in `.env`, source, logs, URLs, Make arguments, notes, or history.

## Preflight and retained output

Maintainers need an ordinary full Git checkout, Git, Make, Bash, Docker, and
Docker buildx. Host Go, GoReleaser, `gh`, jq, Python, application credentials,
and `GITHUB_TOKEN` are not prerequisites for nonpublishing checks and builds.

Keep the checkout clean, with full Git history and the intended public origin.
The tooling isolates and neutralizes inherited source hooks and smudge filters
so they cannot execute, and rejects replacement objects, lazy promisor fetches,
and a dirty selected source. Resolve the selected branch/tag to one full commit
and review the committed environment manifest before continuing. Until
approved stage and production URLs are committed, release publication is
blocked.

Bootstrap and inspect only the locked release image:

```sh
make release-tools
make release-check ENV=stage
make release-check ENV=prod
```

The tool lock, Docker recipe, and bootstrap source are label- and digest-bound.
Do not substitute a locally rebuilt helper or mutable image reference. Release
source preparation uses a disposable full-history repository and runs selected
source only inside the token-free container. Host Git authentication may read
the selected public source, but neither GitHub publisher token is passed into
source preparation, builds, or verification. The credentialed publisher sees
only retained allowlisted release assets and reviewed notes; it receives
neither source code nor the retained helper binaries.

A snapshot is a safe pre-publication exercise using the supported Make
wrappers:

```sh
env -u GITHUB_TOKEN -u GH_TOKEN make release-tools
env -u GITHUB_TOKEN -u GH_TOKEN make release-check ENV=stage
# Capture this only after reviewing the selected source and manifest.
FULL_COMMIT="$(git rev-parse --verify --end-of-options HEAD^{commit})"
: "${FULL_COMMIT:?reuse the reviewed full commit captured during preflight}"
env -u GITHUB_TOKEN -u GH_TOKEN make release-snapshot ENV=stage REF="$FULL_COMMIT"
# Use the retained parent printed above.
env -u GITHUB_TOKEN -u GH_TOKEN \
  make release-verify ENV=stage DIST=/absolute/retained-parent

# Build only an already-reviewed local release tag; this does not publish it.
env -u GITHUB_TOKEN -u GH_TOKEN \
  make release-build ENV=stage TAG=v1.2.3-stage.1
```

Each fresh retained parent contains exactly six platform archives and one
checksum file under `artifacts/`, plus `receipt.json`, `build-info.json`, the
artifact/metadata records, build log, copied controls, embedded Linux helpers,
and release Docker recipe. Those are immutable build output. Native
requirements, raw reports, manual checks, aggregated evidence, stage
acceptance, and `publication.json` are separate operator/publication records.
Native requirements, evidence, and manual records are create-exclusive.
`publication.json` is created exclusively, then its publisher-owned state
transitions are replaced atomically. Never edit immutable build records to add
any of these. Keep failed native reports as diagnostics, but only exact
successful records selected by the typed validator become eligibility input.

Every uploaded archive has exactly five root members: the environment's
executable, `README.md`, `INSTALL.md`, `THIRD_PARTY_NOTICES.txt`, and
`build-info.json`. Release notes/body, receipts, controls, helpers, logs,
native records, stage acceptance, and publication state remain retained local
records; they are not release assets.

This command verifies the retained artifact and receipt bytes only:

```sh
make release-verify ENV=stage DIST=/absolute/retained-parent
```

It does not establish native evidence, real staged-backend acceptance, or
publication eligibility.

## Native evidence

For a routine review, generate a conservative create-exclusive template outside
both retained parents, then deliberately fill every blank field and row before
passing it to the authoritative validator:

```sh
: "${BASELINE_DIST:?set BASELINE_DIST to the reviewed first-support retained parent}"
scripts/release.sh native-review-template --environment stage \
  --dist /absolute/retained-parent --baseline-dist "$BASELINE_DIST" \
  --output /absolute/operator-records/routine-review.json
```

Set `BASELINE_DIST` to a retained parent from the same repository/environment.
Its requirements mode must be exactly `first-support`, its aggregated evidence
must pass every required behavior, it must contain native version/help for all
six targets, and it must have no nested baseline. The template defaults
`created_at`, `operator`, `rationale`, and every `reason` to blank; every target
is `affected: true`, `available: false`, with `version`, `help`, `network`,
`config-private`, `password-input`, and `signal-cancel`. Review and complete
every row. Set `created_at` to an RFC3339 UTC timestamp (for example,
`2026-09-07T12:34:56Z`), set every other blank to nonempty single-line text,
and leave the prefilled identity and target order unchanged.
`native-review-template` requires a new file in an already-existing
ordinary directory with symlink-free ancestry outside both retained parents.

Publication requires an identity-bound requirements file, successful native version/help reports for every required target, the named behavioral records, and one aggregated evidence file. Create requirements once for a retained parent. Use the first form for first support, or the second form for a reviewed routine release; they are alternatives because the output is create-exclusive.

For a first-support release, run only:

```sh
scripts/release.sh native-requirements --environment stage --dist /absolute/retained-parent
```

For a routine release, run only:

```sh
: "${BASELINE_DIST:?reuse the reviewed first-support retained parent}"
scripts/release.sh native-requirements --environment stage --dist /absolute/retained-parent \
  --review /absolute/operator-records/routine-review.json --baseline-dist "$BASELINE_DIST"
```

The immutable procedures in [NATIVE_CHECKS.md](NATIVE_CHECKS.md) govern both
collector facts and operator assertions. Read them immediately before the
following smoke and manual workflow. The committed `.invalid` integration
fixture is regression evidence only and is not a native or publication pass.

Read `native/requirements.json`, then collect each required version/help report
on the matching host. Take the target from its exact requirements row and all
host, provenance, execution, operator, and daemon values from truthful current
observations; blocking defaults remain blocking.

```sh
: "${TARGET_OS:?copy the required target OS from native/requirements.json}"
: "${TARGET_ARCH:?copy the required target architecture from native/requirements.json}"
: "${HOST_OS:?set the actual collector host OS}" "${HOST_ARCH:?set the actual collector host architecture}"
: "${HOST_KIND:?set physical, vm, or unknown}" "${PROVENANCE:?set operator, docker-daemon, or standalone truthfully}"
: "${OPERATOR_ID:?set the lowercase operator ID}" "${EXECUTION:?set native, emulated, or unknown truthfully}"
: "${DAEMON_ARCH:?set the observed daemon architecture or unknown}"
scripts/smoke.sh --dist /absolute/retained-parent --environment stage \
  --target "$TARGET_OS/$TARGET_ARCH" --host-os "$HOST_OS" --host-arch "$HOST_ARCH" \
  --host-kind "$HOST_KIND" --provenance "$PROVENANCE" --operator "$OPERATOR_ID" \
  --procedure native_release_v1 --execution "$EXECUTION" --daemon-arch "$DAEMON_ARCH"
```

Unknown host facts, standalone provenance, and emulated/unknown execution do
not qualify as a native publication pass.
Operator IDs must be lowercase for both collectors. The exact domains are
HostKind `physical|vm|unknown`, Provenance `operator|docker-daemon|standalone`,
and Execution `native|emulated|unknown`. HostOS is `darwin|linux|windows`;
HostArch is `amd64|arm64` (or `unknown` when unproven); DaemonArch is
`amd64|arm64|unknown`. For a native pass, unknown and standalone facts block;
operator provenance requires daemon arch `unknown`, while docker-daemon
provenance requires the matching observed daemon arch.

Only `$TargetOS/$TargetArch` comes from the requirements row. Set every other
Windows variable from actual host/operator facts and use `native` only when proven:

```powershell
$Required = @(
  'TargetOS', 'TargetArch', 'HostOS', 'HostArch', 'HostKind',
  'Provenance', 'OperatorID', 'Execution', 'DaemonArch'
)
foreach ($Name in $Required) {
  if (-not (Get-Variable $Name -ValueOnly -ErrorAction SilentlyContinue)) {
    throw "Set $Name truthfully."
  }
}
.\scripts\smoke.ps1 -Dist C:\absolute\retained-parent -Environment stage `
  -Target "$TargetOS/$TargetArch" -HostOS $HostOS -HostArch $HostArch -HostKind $HostKind `
  -Provenance $Provenance -Operator $OperatorID -Procedure native_release_v1 `
  -Execution $Execution -DaemonArch $DaemonArch
```

`native-manual` does not run or prove behavioral checks. After actually performing each required behavioral procedure, use it only to retain that identity-bound result. Its procedure ID must identify a reviewed, concrete checklist that was followed. Repeat this command for every named `network`, `config-private`, `password-input`, or `signal-cancel` row in the requirements file:

```sh
: "${REQUIRED_TARGET:?copy target from one exact native/requirements.json row}"
: "${REQUIRED_CHECK:?copy check from that same row}"
: "${PROCEDURE_ID:?set its reviewed immutable procedure ID}"
: "${OPERATOR_ID:?set the lowercase operator ID}"
scripts/release.sh native-manual --environment stage --dist /absolute/retained-parent \
  --target "$REQUIRED_TARGET" --check "$REQUIRED_CHECK" --result pass \
  --operator "$OPERATOR_ID" --procedure "$PROCEDURE_ID"
```

Aggregate only after all required reports and manual records are present, then retry the original publish command with the same retained parent:

```sh
scripts/release.sh native-evidence --environment stage --dist /absolute/retained-parent
make stage-release TAG=v1.2.3-stage.1 NOTES_FILE=/absolute/release-notes.md \
  DIST=/absolute/retained-parent
```

Use `--environment prod` and the original production publish arguments for production evidence. An unavailable host, emulated/unknown execution, missing reviewed procedure (including native Windows checks), missing manual record, or raw report without requirements and aggregation remains unverified and blocks publication; do not relabel it as passed.

Collectors may run as soon as each native host is available, and failed reports
remain diagnostic while a later unique report can succeed. First support still
requires native version/help observations for all six targets. The initial
Windows targets additionally require their reviewed config-private,
password-input, and signal-cancel procedures. The network procedure is bound to
the receipt's original native Linux builder target, not inferred from the
collector or Docker daemon architecture.

Routine requirements need both a reviewed routine policy and one concrete
retained baseline from the same repository and environment. Its requirements
mode must be exactly `first-support`, its valid aggregated evidence must contain
native version/help for all six targets, and it cannot contain another nested
baseline. The review
names available and affected targets and checks: available or affected targets
need current version/help, affected named behaviors recur, and the original
Linux builder-target network check remains mandatory. An empty row for a target
declared unavailable and unchanged means no current check is required; it never
means that target passed. The review cannot shrink the mandatory policy floor.
Operator host/procedure claims are reviewed attestations, not cryptographic
proof. Unknown or emulated reports can be useful diagnostics but never satisfy
a native requirement.

The repository's isolated `.invalid` test exercises exact binaries from
synthetic retained bundles and labels its records `local-tls-phoenix-v1`.
Those records are test-local, are never publication inputs, and never satisfy
the real `network_release_v1` procedure. The test does not approve public
service URLs or establish native Windows behavior.

## Stage

Reuse the exact preflight `FULL_COMMIT` value for every stage and production
command below; never recompute it after review.

Prepare reviewed notes outside the checkout. They must call out the
compatibility break and current macOS distribution status; for example:

```text
Breaking: stage and production now use separate executables, credentials, and
config files. The legacy shared config is not loaded automatically; use the
explicit environment-confirming migration command described in INSTALL.md.

macOS archives are not Apple Developer-ID signed or notarized. An ARM64 binary
may still contain an ad-hoc linker signature, which is not Apple identity.
```

After review, run:

```sh
make release-tools
make release-check ENV=stage
: "${FULL_COMMIT:?reuse the reviewed full commit captured during preflight; never recompute it}"
make stage-release TAG=v1.2.3-stage.1 REF="$FULL_COMMIT" NOTES_FILE=/absolute/release-notes.md
```

The equivalent direct script option is `--notes`, not `NOTES_FILE`:

```sh
: "${FULL_COMMIT:?reuse the reviewed full commit captured during preflight; never recompute it}"
scripts/release.sh publish --environment stage --tag v1.2.3-stage.1 \
  --ref "$FULL_COMMIT" --notes /absolute/release-notes.md
```

The command checks the fetched stage tip, creates or reuses the exact local annotated tag, builds and verifies the tagged bundle, and stops before a remote write if required native evidence is incomplete. Follow the printed retained-parent path and the complete [native evidence](#native-evidence) workflow, then retry with `DIST=/absolute/retained-parent`. Keep that parent unchanged.

After the completed stage release has passed actual authenticated staging checks, create an operator record:

```sh
scripts/release.sh acceptance-template \
  --dist /absolute/retained-parent \
  --output /absolute/operator-records/stage-acceptance.json
```

The template starts with `accepted: false`. Record the reviewer, a UTC second-precision `accepted_at`, `accepted: true`, and distinct successful staging-check names. This is an explicit operator claim, not a generated test result.
Its output must be a new file in an already-existing ordinary directory with
symlink-free ancestry outside the retained stage parent.

## Production

Production is a separate build from the same tested commit, with production endpoint and executable metadata:

```sh
: "${FULL_COMMIT:?reuse the reviewed full commit captured during preflight; never recompute it}"
make prod-release TAG=v1.2.3 REF="$FULL_COMMIT" \
  FROM_STAGE_TAG=v1.2.3-stage.1 \
  STAGE_ACCEPTANCE=/absolute/operator-records/stage-acceptance.json \
  NOTES_FILE=/absolute/release-notes.md
```

The publisher verifies the completed remote stage release and downloads every accepted stage asset before any production mutation. It checks stable history before the first production write and again immediately before finalization. It fails closed when the current stable latest tag is not strict `vMAJOR.MINOR.PATCH` or is newer/equal.

## Status and recovery

`GITHUB_TOKEN` is always required for status. Status is read-only. With `DIST`,
it verifies retained state and reports the remote tag as exact or absent;
`absent` is reported only when the release plan is also absent. A mismatch, or
a missing tag for an existing release, fails closed. Without `DIST`, status
prints only a successful release lookup; lookup failure never means or reports
absence. `resume`, not status, is the mutating recovery command.

```sh
make release-status ENV=stage TAG=v1.2.3-stage.1
make release-status ENV=stage TAG=v1.2.3-stage.1 DIST=/absolute/retained-parent
```

Resume only from the original retained parent:

```sh
make release-resume ENV=stage TAG=v1.2.3-stage.1 DIST=/absolute/retained-parent
```

| Observed state | Action |
| --- | --- |
| Validation or build failed | Fix the cause and use a new candidate tag if source changed. Preserve the failed parent for diagnosis. |
| Exact tag exists, no release; valid `publication.json` exists | Resume from the retained parent; the exact tag object is reused without another tag push. |
| Exact tag exists, no release; `publication.json` is absent | `release-resume` fails closed. Rerun the original stage/prod publish with the same `DIST`, notes, and promotion inputs so initial state is recreated. |
| Tag object or peeled commit differs | Stop. Never force, delete, or retarget it. |
| Matching draft has missing assets | Use explicit resume. Existing assets are freshly downloaded and hashed; only missing names are uploaded. |
| Asset is unexpected, incomplete, starter, wrong-sized, or has different bytes | Stop and inspect it. The tool never deletes or clobbers an asset. |
| Matching release is already published and complete | The command reports already complete without editing or uploading. |
| A published release is incomplete or has different assets | Treat it as an incident. Do not mutate it; investigate and use a reviewed corrective release. |
| A create, upload, or finalize response was lost | Run status, then explicit resume. Every retry reconciles remote state before another write. |
| `publication.json` is lost but the other retained inputs remain | Explicit resume restores it only from the exact owned marker, tag, release, and downloaded asset bytes. A foreign or absent release is never adopted. |
| Original retained parent or its immutable inputs are lost | Restore those exact bytes from retention or choose a new tag. Do not rebuild over a known release. |
| Local publication lock exists | Coordinate with its recorded owner. Locks are never stolen automatically. |

The local lock and immediate latest recheck reduce races but are not a distributed transaction. Do not run another local or automated publisher concurrently. Preserve the retained artifacts, receipt, notes, native evidence, acceptance, and publication record for the supported lifetime (at least 90 days while policy is established).

## Unresolved before the first release

Before authorizing the first publication, record decisions for every item below.
None is claimed complete by this repository change:

- approved public stage and production URLs;
- final repository identity and first release version;
- disposition and revocation assessment for the removed historical credential
  literal, without copying that value into notes or logs;
- native hosts and evidence for every required target, including Windows; and
- the Apple signing/notarization and intended audience decision.

## CLI behavior checked before release

Stage and production use distinct executable names, embedded endpoints,
credentials, and config files. Login may create a new explicitly selected file;
ordinary commands reject a missing explicit file. Legacy generic environment
variables need an environment assertion, and a legacy config is imported only
by the explicit migration command. Private writes validate file type,
ownership/access, ancestors, and platform-specific ACL rules rather than
repairing a user path. Native Windows privacy and input behavior remain a
separate release gate until run on Windows.

API redirects are rejected before a CLI key can be forwarded. Delivery bodies
accept padded or unpadded standard Base64; responses use padded Base64. Local
forwarding reports the first response, including redirects, without following
it. Successful API JSON is capped at 1 MiB, WebSocket frames at 32 MiB, and
local response bodies at 16 MiB. These are client limits, not statements about
the deployed backend.

Join and WebSocket writes have 10-second bounds. Heartbeats run every 30
seconds, and 90 seconds without qualifying receive activity ends a session. A
valid serial delivery renews the receive window after processing. Output
failures stop processing: failed inspect output is not acknowledged as success,
while a completed forward response is attempted with its real result before a
display failure stops the session.

Cancellation covers dialing, proxy CONNECT, HTTP/TLS upgrade, join, and active
network work. Synchronous output to an unread operating-system pipe can still
block graceful completion. Once the first signal's handler has completed, a
later Ctrl-C uses default handling and can force exit, possibly interrupting
cleanup. Login restores terminal mode on ordinary cancellation; its single
process-lifetime fallback reader may remain blocked until process exit. Do not
claim native Windows filesystem, input, or signal results without a Windows
host.
