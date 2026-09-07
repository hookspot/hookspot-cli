param(
    [Parameter(Mandatory = $true)][string]$Dist,
    [Parameter(Mandatory = $true)][ValidateSet("stage", "prod")][string]$Environment,
    [string]$Target = "",
    [string]$HostOS = "unknown",
    [string]$HostArch = "unknown",
    [string]$HostKind = "unknown",
    [string]$Provenance = "standalone",
    [string]$Operator = "unknown",
    [string]$Procedure = "unknown",
    [ValidateSet("native", "emulated", "unknown")][string]$Execution = "unknown",
    [string]$DaemonArch = "unknown"
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version 2.0

function Fail([string]$Message) {
    throw "smoke: $Message"
}

function Normalize-Arch([string]$Value) {
    switch ($Value.ToLowerInvariant()) {
        "amd64" { return "amd64" }
        "x86_64" { return "amd64" }
        "arm64" { return "arm64" }
        "aarch64" { return "arm64" }
        default { return "unknown" }
    }
}

function Test-Fact([string]$Value) {
    return $Value -match '^[a-z0-9_-]+$'
}

function Get-SHA256([string]$Path) {
    $stream = [System.IO.File]::Open($Path, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::Read)
    try {
        $hasher = [System.Security.Cryptography.SHA256]::Create()
        try {
            return ([System.BitConverter]::ToString($hasher.ComputeHash($stream))).Replace("-", "").ToLowerInvariant()
        } finally {
            $hasher.Dispose()
        }
    } finally {
        $stream.Dispose()
    }
}

function Invoke-Observation([string]$Label, [string[]]$Arguments) {
    $stdoutPath = Join-Path $script:ReportDir ($Label + ".stdout")
    $stderrPath = Join-Path $script:ReportDir ($Label + ".stderr")
    $stdout = [System.IO.File]::Open($stdoutPath, [System.IO.FileMode]::CreateNew, [System.IO.FileAccess]::Write, [System.IO.FileShare]::Read)
    $stderr = [System.IO.File]::Open($stderrPath, [System.IO.FileMode]::CreateNew, [System.IO.FileAccess]::Write, [System.IO.FileShare]::Read)
    $process = New-Object System.Diagnostics.Process
    try {
        $start = $process.StartInfo
        $start.FileName = $script:BinaryPath
        $start.UseShellExecute = $false
        $start.CreateNoWindow = $true
        $start.RedirectStandardInput = $true
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        $start.Arguments = (($Arguments | ForEach-Object {
            if ($_ -notmatch '^[a-zA-Z0-9._/-]+$') { Fail "internal command argument is invalid" }
            $_
        }) -join " ")
        $start.EnvironmentVariables.Clear()
        $start.EnvironmentVariables["SystemRoot"] = $env:SystemRoot
        $start.EnvironmentVariables["WINDIR"] = $env:WINDIR
        $start.EnvironmentVariables["PATH"] = (Join-Path $env:SystemRoot "System32")
        $start.EnvironmentVariables["HOME"] = $script:IsolatedHome
        $start.EnvironmentVariables["USERPROFILE"] = $script:IsolatedHome
        $start.EnvironmentVariables["TEMP"] = $script:IsolatedTemp
        $start.EnvironmentVariables["TMP"] = $script:IsolatedTemp
        if (-not $process.Start()) { Fail "cannot start $Label observation" }
        $process.StandardInput.Close()
        $stdoutCopy = $process.StandardOutput.BaseStream.CopyToAsync($stdout)
        $stderrCopy = $process.StandardError.BaseStream.CopyToAsync($stderr)
        $processTimedOut = -not $process.WaitForExit(15000)
        if ($processTimedOut) {
            try { $process.Kill() } catch {}
            [void]$process.WaitForExit(2000)
        }
        $copyTasks = [System.Threading.Tasks.Task[]]@($stdoutCopy, $stderrCopy)
        $drained = $false
        $captureFailed = $false
        try { $drained = [System.Threading.Tasks.Task]::WaitAll($copyTasks, 5000) } catch { $captureFailed = $true; $drained = $true }
        $drainTimedOut = -not $drained
        if (-not $drained) {
            try { $process.StandardOutput.BaseStream.Dispose() } catch {}
            try { $process.StandardError.BaseStream.Dispose() } catch {}
            try { [void][System.Threading.Tasks.Task]::WaitAll($copyTasks, 1000) } catch {}
        }
        if ($captureFailed) {
            try { $process.StandardOutput.BaseStream.Dispose() } catch {}
            try { $process.StandardError.BaseStream.Dispose() } catch {}
            try { [void][System.Threading.Tasks.Task]::WaitAll($copyTasks, 1000) } catch {}
        }
        $timedOut = $processTimedOut -or $drainTimedOut
        $exitCode = 124
        if (-not $timedOut) { $exitCode = $process.ExitCode }
        return @{ Exit = $exitCode; Timeout = $timedOut; CaptureFailed = $captureFailed }
    } finally {
        $stdout.Dispose()
        $stderr.Dispose()
        $process.Dispose()
    }
}

try {
    $Dist = [System.IO.Path]::GetFullPath($Dist)
    $distInfo = Get-Item -LiteralPath $Dist
    if (-not $distInfo.PSIsContainer -or ($distInfo.Attributes -band [System.IO.FileAttributes]::ReparsePoint)) {
        Fail "dist is missing or unsafe"
    }
    $collectorArch = Normalize-Arch $env:PROCESSOR_ARCHITECTURE
    if ($collectorArch -eq "unknown") { Fail "collector architecture is unsupported" }
    $targetOS = "windows"
    $targetArch = $collectorArch
    if ($Target -ne "") {
        if ($Target -notmatch '^windows/(amd64|arm64)$') { Fail "Windows target must be windows/amd64 or windows/arm64" }
        $targetArch = $Matches[1]
    }
    foreach ($fact in @($HostOS, $HostArch, $HostKind, $Provenance, $Operator, $Procedure, $Execution, $DaemonArch)) {
        if (-not (Test-Fact $fact)) { Fail "host provenance contains invalid data" }
    }
    if ($HostKind -notin @("physical", "vm", "unknown")) { Fail "host kind is unsupported" }
    if ($Provenance -notin @("operator", "docker-daemon", "standalone")) { Fail "provenance is unsupported" }

    $receiptPath = Join-Path $Dist "receipt.json"
    $receiptInfo = Get-Item -LiteralPath $receiptPath
    if ($receiptInfo.PSIsContainer -or ($receiptInfo.Attributes -band [System.IO.FileAttributes]::ReparsePoint)) {
        Fail "receipt is missing or unsafe"
    }
    $artifactDir = Join-Path $Dist "artifacts"
    $archives = @(Get-ChildItem -LiteralPath $artifactDir -File -Filter ("hookspot_{0}_*_{1}_{2}.zip" -f $Environment, $targetOS, $targetArch))
    if ($archives.Count -ne 1 -or ($archives[0].Attributes -band [System.IO.FileAttributes]::ReparsePoint)) {
        Fail "expected exactly one matching retained archive"
    }
    $archive = $archives[0]
    $binaryName = if ($Environment -eq "stage") { "hookspot-stage.exe" } else { "hookspot.exe" }

    $nativeDir = Join-Path $Dist "native"
    $reportsDir = Join-Path $nativeDir "reports"
    $targetDir = Join-Path $reportsDir ("{0}-{1}" -f $targetOS, $targetArch)
    foreach ($path in @($nativeDir, $reportsDir, $targetDir)) {
        if (Test-Path -LiteralPath $path) {
            $info = Get-Item -LiteralPath $path
            if (-not $info.PSIsContainer -or ($info.Attributes -band [System.IO.FileAttributes]::ReparsePoint)) {
                Fail "native report path is unsafe"
            }
        } else {
            [void](New-Item -ItemType Directory -Path $path)
        }
    }
    $reportID = "report." + [Guid]::NewGuid().ToString("N")
    $script:ReportDir = Join-Path $targetDir $reportID
    [void](New-Item -ItemType Directory -Path $script:ReportDir)
    $extractDir = Join-Path $script:ReportDir ("extract." + [Guid]::NewGuid().ToString("N"))
    [void](New-Item -ItemType Directory -Path $extractDir)

    try {
        [void][System.Reflection.Assembly]::LoadWithPartialName("System.IO.Compression.FileSystem")
        $zip = [System.IO.Compression.ZipFile]::OpenRead($archive.FullName)
        try {
            $entries = @($zip.Entries | Where-Object { $_.FullName -eq $binaryName })
            if ($entries.Count -ne 1) { Fail "archive does not contain exactly one selected binary" }
            $script:BinaryPath = Join-Path $extractDir $binaryName
            $input = $entries[0].Open()
            $output = [System.IO.File]::Open($script:BinaryPath, [System.IO.FileMode]::CreateNew, [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)
            try { $input.CopyTo($output) } finally { $output.Dispose(); $input.Dispose() }
        } finally {
            $zip.Dispose()
        }

        $script:IsolatedHome = Join-Path $extractDir "home"
        $script:IsolatedTemp = Join-Path $extractDir "tmp"
        [void](New-Item -ItemType Directory -Path $script:IsolatedHome)
        [void](New-Item -ItemType Directory -Path $script:IsolatedTemp)
        $receiptSHA = Get-SHA256 $receiptPath
        $archiveSHA = Get-SHA256 $archive.FullName
        $binarySHA = Get-SHA256 $script:BinaryPath
        $version = Invoke-Observation "version" @("version", "--json")
        $help = Invoke-Observation "help" @("--help")

        $record = @(
            "SCHEMA_VERSION=1",
            "REPORT_ID=$reportID",
            "ENVIRONMENT=$Environment",
            "TARGET_OS=$targetOS",
            "TARGET_ARCH=$targetArch",
            "COLLECTOR_OS=windows",
            "COLLECTOR_ARCH=$collectorArch",
            "HOST_OS=$HostOS",
            "HOST_ARCH=$HostArch",
            "HOST_KIND=$HostKind",
            "PROVENANCE=$Provenance",
            "OPERATOR=$Operator",
            "PROCEDURE=$Procedure",
            "EXECUTION=$Execution",
            "DAEMON_ARCH=$DaemonArch",
            "REQUESTED_PLATFORM=$targetOS/$targetArch",
            "COLLECTOR_TRANSLATED=unknown",
            "RECEIPT_SHA256=$receiptSHA",
            "ARCHIVE_NAME=$($archive.Name)",
            "ARCHIVE_SHA256=$archiveSHA",
            "BINARY_NAME=$binaryName",
            "BINARY_SHA256=$binarySHA",
            "VERSION_EXIT=$($version.Exit)",
            "VERSION_TIMEOUT=$($version.Timeout.ToString().ToLowerInvariant())",
            "VERSION_CAPTURE_FAILED=$($version.CaptureFailed.ToString().ToLowerInvariant())",
            "HELP_EXIT=$($help.Exit)",
            "HELP_TIMEOUT=$($help.Timeout.ToString().ToLowerInvariant())",
            "HELP_CAPTURE_FAILED=$($help.CaptureFailed.ToString().ToLowerInvariant())"
        ) -join "`n"
        $record += "`n"
        $recordTemp = Join-Path $script:ReportDir ".record.env.tmp"
        [System.IO.File]::WriteAllText($recordTemp, $record, (New-Object System.Text.ASCIIEncoding))
        [System.IO.File]::Move($recordTemp, (Join-Path $script:ReportDir "record.env"))
        Write-Output "smoke report: $($script:ReportDir)"
        if ($version.Exit -ne 0 -or $version.Timeout -or $version.CaptureFailed -or $help.Exit -ne 0 -or $help.Timeout -or $help.CaptureFailed) {
            Fail "version or help observation failed; report retained"
        }
    } finally {
        if (Test-Path -LiteralPath $extractDir) { Remove-Item -LiteralPath $extractDir -Recurse -Force }
    }
} catch {
    [Console]::Error.WriteLine($_.Exception.Message)
    exit 1
}
