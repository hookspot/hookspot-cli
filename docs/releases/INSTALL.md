# Install Hookspot CLI

Downloaded releases require no Go, Docker, compiler, package manager,
administrator access, or publishing token.

Each release has exactly seven assets: six platform archives and one checksum
file. Archive names identify the environment, version, operating system, and
architecture. Production archives contain `hookspot` (`hookspot.exe` on
Windows); staging archives contain `hookspot-stage` (`hookspot-stage.exe`).
`darwin` means macOS, `amd64` means Intel/AMD 64-bit, and `arm64` means Apple
Silicon or another ARM64 system.

Use the repository's [stable-latest release page](https://github.com/bgr11n/hookspot-cli/releases/latest)
for production. Staging is not the stable latest release: open the explicit
reviewed stage tag in the [release list](https://github.com/bgr11n/hookspot-cli/releases)
and use its stage version, tag, archives, and checksum file.

## Download and verify on macOS or Linux

The macOS executables are not signed with an Apple Developer ID and are not
notarized. An ARM64 executable may still contain an ad-hoc linker signature;
that is not Apple identity verification. macOS may warn before the first
execution. Verify the checksum and release/tag identity, then use Apple's
per-app approval flow if needed; never disable Gatekeeper globally. See
<https://support.apple.com/en-us/102445>.

Set the values inside this subshell from the reviewed release page. A staging
version includes the `-stage.N` suffix. This production example selects macOS
ARM64. For stage, set `ENVIRONMENT=stage`; the executable name follows it
automatically.

The checksum shipped in the same release protects downloaded byte integrity;
it is not an independent signature. Review the repository, release, and exact
tag identity before trusting it.

```sh
(
set -eu
VERSION=1.2.3
ENVIRONMENT=prod
TARGET=darwin_arm64
TAG="v$VERSION"
ARCHIVE="hookspot_${ENVIRONMENT}_${VERSION}_${TARGET}.tar.gz"
CHECKSUMS="hookspot_${ENVIRONMENT}_${VERSION}_checksums.txt"
BASE="https://github.com/bgr11n/hookspot-cli/releases/download/$TAG"
case "$ENVIRONMENT" in
  prod) BINARY=hookspot ;;
  stage) BINARY=hookspot-stage ;;
  *) echo "ENVIRONMENT must be prod or stage" >&2; exit 1 ;;
esac

DOWNLOAD_DIR=$(mktemp -d "${TMPDIR:-/tmp}/hookspot-install.XXXXXX")
trap 'rm -rf "$DOWNLOAD_DIR"' EXIT
curl -fL "$BASE/$ARCHIVE" -o "$DOWNLOAD_DIR/$ARCHIVE"
curl -fL "$BASE/$CHECKSUMS" -o "$DOWNLOAD_DIR/$CHECKSUMS"

awk -v name="$ARCHIVE" '$2 == name { print $1 "  " $2 }' \
  "$DOWNLOAD_DIR/$CHECKSUMS" >"$DOWNLOAD_DIR/$ARCHIVE.sha256"
test "$(wc -l <"$DOWNLOAD_DIR/$ARCHIVE.sha256" | tr -d ' ')" -eq 1
case "$(uname -s)" in
  Linux) (cd "$DOWNLOAD_DIR" && sha256sum -c "$ARCHIVE.sha256") ;;
  Darwin) (cd "$DOWNLOAD_DIR" && shasum -a 256 -c "$ARCHIVE.sha256") ;;
  *) echo "Use the Windows instructions on Windows" >&2; exit 1 ;;
esac

mkdir "$DOWNLOAD_DIR/extracted"
tar -xzf "$DOWNLOAD_DIR/$ARCHIVE" -C "$DOWNLOAD_DIR/extracted"
INSTALL_DIR="$HOME/.local/bin"
mkdir -p "$INSTALL_DIR"
install -m 0755 "$DOWNLOAD_DIR/extracted/$BINARY" "$INSTALL_DIR/$BINARY"
"$INSTALL_DIR/$BINARY" version --json
"$INSTALL_DIR/$BINARY" --help
)
```

Ensure `$HOME/.local/bin` is on your `PATH`; for example, add this to your shell
profile and start a new shell:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

For staging, also set its explicit stage version, tag, and target. Linux targets
are `linux_amd64` and `linux_arm64`; macOS targets are `darwin_amd64` and
`darwin_arm64`.

## Download and verify on Windows

Set `$Version`, `$Environment`, and `$Target` from the reviewed release page.
This example selects production Windows AMD64:

As on Unix, the checksum and archive come from the same release. The checksum
detects changed bytes but is not an independent signature; review the exact
release/tag identity before running it.

```powershell
& {
$ErrorActionPreference = 'Stop'
$Version = '1.2.3'
$Environment = 'prod'
$Target = 'windows_amd64'
$Tag = "v$Version"
$Binary = if ($Environment -eq 'stage') { 'hookspot-stage.exe' } elseif ($Environment -eq 'prod') { 'hookspot.exe' } else { throw 'Environment must be prod or stage.' }
$Archive = "hookspot_${Environment}_${Version}_${Target}.zip"
$Checksums = "hookspot_${Environment}_${Version}_checksums.txt"
$Base = "https://github.com/bgr11n/hookspot-cli/releases/download/$Tag"

$DownloadDir = Join-Path ([IO.Path]::GetTempPath()) ("hookspot-install-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $DownloadDir | Out-Null
try {
  $ArchivePath = Join-Path $DownloadDir $Archive
  $ChecksumsPath = Join-Path $DownloadDir $Checksums
  Invoke-WebRequest -UseBasicParsing "$Base/$Archive" -OutFile $ArchivePath
  Invoke-WebRequest -UseBasicParsing "$Base/$Checksums" -OutFile $ChecksumsPath

  $ChecksumEntries = @(Get-Content $ChecksumsPath | Where-Object {
    $Parts = $_ -split '\s+', 2
    $Parts.Count -eq 2 -and $Parts[1] -eq $Archive
  })
  if ($ChecksumEntries.Count -ne 1) { throw 'Expected exactly one checksum entry for the selected archive.' }
  $Expected = ($ChecksumEntries[0] -split '\s+', 2)[0].ToLowerInvariant()
  $Actual = (Get-FileHash -Algorithm SHA256 $ArchivePath).Hash.ToLowerInvariant()
  if ($Actual -ne $Expected) { throw 'Downloaded archive checksum does not match.' }

  $Extracted = Join-Path $DownloadDir 'extracted'
  Expand-Archive $ArchivePath -DestinationPath $Extracted
  $InstallDir = Join-Path $HOME '.local\bin'
  New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
  $Installed = Join-Path $InstallDir $Binary
  Copy-Item (Join-Path $Extracted $Binary) $Installed
  & $Installed version --json
  if ($LASTEXITCODE -ne 0) { throw "${Binary} version --json failed with exit code $LASTEXITCODE." }
  & $Installed --help
  if ($LASTEXITCODE -ne 0) { throw "${Binary} --help failed with exit code $LASTEXITCODE." }
} finally {
  Remove-Item -Recurse -Force $DownloadDir
}
}
```

Add `%USERPROFILE%\.local\bin` to your user `PATH` if it is not already
present, then open a new terminal. Use the explicit stage release page and
`hookspot-stage.exe` for staging. Windows ARM64 uses `windows_arm64`.

## Quick start

The minimal interactive flow is:

```sh
hookspot login
hookspot project use
hookspot listen
```

Project listing and explicit UID selection are optional alternatives:

```sh
hookspot project list
hookspot project use PROJECT_UID
```

Staging and production keep separate keys, configuration, and project
selection. This is a compatibility break from the old shared credential and
generic environment variables: no legacy file is imported automatically.
Review help, then run only the example for the intended environment. Do not
use this as a recipe to import one key or project into both stage and
production.

Production migration:

```sh
hookspot config migrate --from "$HOME/.config/hookspot/config.toml" \
  --confirm-environment prod
```

Staging migration, only when the legacy file is known to contain staging
credentials:

```sh
hookspot-stage config migrate --from "$HOME/.config/hookspot/config.toml" \
  --confirm-environment stage
```

Migration contacts the selected environment's backend to validate both the
stored key and selected project. It never substitutes an environment-variable
key, refuses an existing destination, and leaves the legacy source unchanged.

The staging quick start uses the same flow with `hookspot-stage login`,
`hookspot-stage project use`, and `hookspot-stage listen`. The selected config
is access-restricted plaintext, not encrypted. Logout removes only the local
saved key; service-side revocation or rotation is a separate backend action.

Third-party terms and attributions are in `THIRD_PARTY_NOTICES.txt` inside the
archive.

## Update

Download the new release's matching archive and checksum file, repeat the exact
checksum check above, replace only the installed executable, then run its
`version --json` and `--help` using the exact installed path. Updating does not
migrate, merge, or delete configuration.

## Uninstall

Remove only the user-owned executable you installed:

```sh
rm "$HOME/.local/bin/hookspot"
# or: rm "$HOME/.local/bin/hookspot-stage"
```

On Windows, remove the selected executable without administrator access:

```powershell
Remove-Item "$HOME\.local\bin\hookspot.exe"
# or: Remove-Item "$HOME\.local\bin\hookspot-stage.exe"
```

Credential and configuration removal is separate. `hookspot logout` clears the
saved production key but does not unset `HOOKSPOT_PROD_CLI_KEY`; similarly,
`hookspot-stage logout` does not unset `HOOKSPOT_STAGE_CLI_KEY`. Unset the
matching environment variable yourself. Delete an environment's config
directory only when you intentionally want to remove its saved key and project.
The same home-relative locations apply on Windows:

```text
~/.config/hookspot/prod
~/.config/hookspot/stage
```
