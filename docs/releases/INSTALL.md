# Install Hookspot CLI

Every release is published to npm, Homebrew, and GitHub Releases from one
build, so all three channels install the same `hookspot` binary. None of them
needs Go, Docker, a compiler, or administrator access. After installing,
confirm the binary before logging in:

```sh
hookspot version --json
```

The JSON identifies the version, source commit, environment, compiled endpoint,
Go version, and target platform. The endpoint is part of the executable and
cannot be changed at runtime.

## npm

Requires Node 18 or newer. The package bundles binaries for macOS, Linux, and
Windows on amd64 and arm64; a small launcher runs the one matching your
platform.

```sh
npm install -g hookspot
```

## Homebrew

Works on macOS and Linux. The formula lives in the `hookspot/homebrew-hookspot`
tap and installs the archive from the GitHub Release, verified against its
SHA-256. Homebrew does not quarantine the binary, so macOS does not warn before
the first run.

```sh
brew install hookspot/hookspot/hookspot
```

## GitHub release archive

Each release has exactly seven assets: six platform archives and one checksum
file. Archives are named `hookspot_<version>_<os>_<arch>.tar.gz`, or `.zip` on
Windows, and contain `hookspot` (`hookspot.exe` on Windows) next to this guide,
`README.md`, `THIRD_PARTY_NOTICES.txt`, and `build-info.json`. The checksum
file is `hookspot_<version>_checksums.txt`. `darwin` means macOS, `amd64` means
Intel/AMD 64-bit, and `arm64` means Apple Silicon or another ARM64 system.

Download from the [latest release](https://github.com/hookspot/hookspot-cli/releases/latest)
or pick an explicit version from the [release list](https://github.com/hookspot/hookspot-cli/releases).

The checksum shipped in the same release protects downloaded byte integrity;
it is not an independent signature. Review the repository, release, and exact
tag identity before trusting it.

### Download and verify on macOS or Linux

The macOS executables are not signed with an Apple Developer ID and are not
notarized. An ARM64 executable may still contain an ad-hoc linker signature;
that is not Apple identity verification. macOS may warn before the first
execution of a plain download. Verify the checksum and release/tag identity,
then use Apple's per-app approval flow if needed; never disable Gatekeeper
globally. See <https://support.apple.com/en-us/102445>.

Set the version and target inside this subshell from the release page. The
example selects macOS ARM64; Linux targets are `linux_amd64` and
`linux_arm64`, and macOS targets are `darwin_amd64` and `darwin_arm64`.

```sh
(
set -eu
VERSION=1.2.3
TARGET=darwin_arm64
TAG="v$VERSION"
ARCHIVE="hookspot_${VERSION}_${TARGET}.tar.gz"
CHECKSUMS="hookspot_${VERSION}_checksums.txt"
BASE="https://github.com/hookspot/hookspot-cli/releases/download/$TAG"

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
install -m 0755 "$DOWNLOAD_DIR/extracted/hookspot" "$INSTALL_DIR/hookspot"
"$INSTALL_DIR/hookspot" version --json
"$INSTALL_DIR/hookspot" --help
)
```

Ensure `$HOME/.local/bin` is on your `PATH`; for example, add this to your shell
profile and start a new shell:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

### Download and verify on Windows

Set `$Version` and `$Target` from the release page. The example selects Windows
AMD64; Windows ARM64 uses `windows_arm64`.

```powershell
& {
$ErrorActionPreference = 'Stop'
$Version = '1.2.3'
$Target = 'windows_amd64'
$Tag = "v$Version"
$Archive = "hookspot_${Version}_${Target}.zip"
$Checksums = "hookspot_${Version}_checksums.txt"
$Base = "https://github.com/hookspot/hookspot-cli/releases/download/$Tag"

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
  $Installed = Join-Path $InstallDir 'hookspot.exe'
  Copy-Item (Join-Path $Extracted 'hookspot.exe') $Installed
  & $Installed version --json
  if ($LASTEXITCODE -ne 0) { throw "hookspot.exe version --json failed with exit code $LASTEXITCODE." }
  & $Installed --help
  if ($LASTEXITCODE -ne 0) { throw "hookspot.exe --help failed with exit code $LASTEXITCODE." }
} finally {
  Remove-Item -Recurse -Force $DownloadDir
}
}
```

Add `%USERPROFILE%\.local\bin` to your user `PATH` if it is not already
present, then open a new terminal.

## Quick start

The minimal interactive flow is:

```sh
hookspot login
hookspot project use
hookspot listen
```

`project use` selects the only accessible project automatically or opens an
arrow-key picker when several are available. Noninteractive callers must pass
`PROJECT_UID`, `ORGANIZATION`, or `ORGANIZATION PROJECT`. Names match exactly
without regard to case and names containing spaces must be quoted. A single
path-safe argument is resolved as a UID first; only a 404 falls back to an
organization-name match.

Commands choose one complete config file: explicit `--config`, then
`HOOKSPOT_CONFIG_FILE`, then `.hookspot/<environment>/config.toml` in the
current directory, then the global `~/.config/hookspot/<environment>/config.toml`
(`prod` for released binaries). The CLI does not search parent directories, and
an invalid local file blocks fallback. `project use --local` creates or updates
the local record and cannot be combined with a custom config path. A new local
record may copy the CLI key persisted in the global record, so treat it as
plaintext credentials; flag and environment keys are never copied.

A legacy shared `~/.config/hookspot/config.toml` is never imported
automatically. Review `hookspot config migrate --help` first, then:

```sh
hookspot config migrate --from "$HOME/.config/hookspot/config.toml" \
  --confirm-environment prod
```

Migration contacts the backend to validate both the stored key and the selected
project. It never substitutes an environment-variable key, refuses an existing
destination, and leaves the legacy source unchanged.

The selected config is access-restricted plaintext, not encrypted. Logout
removes only the saved key in that selected config; service-side revocation or
rotation is a separate backend action.

Third-party terms and attributions are in `THIRD_PARTY_NOTICES.txt` inside the
archive.

## Update

Update through the channel you installed from:

```sh
npm install -g hookspot@latest
brew upgrade hookspot
```

For an archive install, download the new release's matching archive and
checksum file, repeat the exact checksum check above, replace only the
installed executable, then run its `version --json` and `--help` using the
exact installed path. Updating does not migrate, merge, or delete
configuration.

## Uninstall

Remove the executable through the channel you installed from:

```sh
npm uninstall -g hookspot
brew uninstall hookspot
rm "$HOME/.local/bin/hookspot"
```

On Windows, remove an archive install without administrator access:

```powershell
Remove-Item "$HOME\.local\bin\hookspot.exe"
```

Credential and configuration removal is separate. `hookspot logout` clears the
saved key but does not unset `HOOKSPOT_CLI_KEY`; unset the environment variable
yourself. Delete the config directory only when you intentionally want to
remove its saved key and project. The same home-relative location applies on
Windows:

```text
~/.config/hookspot/prod
```
