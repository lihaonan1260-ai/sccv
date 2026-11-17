# Build script for the sccv single binary (K8 dual-form: bare launch = GUI,
# any argument = CLI with the original scc semantics).
# ASCII only, CRLF line endings, English comments only.
#
# Usage:
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\build-sccv.ps1
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\build-sccv.ps1 -CliOnly
#
# Exit codes: 0 = built, 3 = go toolchain not found, other = go build failure.

param(
    # Debug variant without -H=windowsgui: keeps the console subsystem so
    # stdout/stderr show up directly in a terminal during development.
    [switch]$CliOnly
)

$ErrorActionPreference = "Stop"

# Default configuration block (override here or via the -CliOnly switch).
$outDir = "dist"
$ldflags = "-s -w -H=windowsgui"
$outFile = Join-Path $outDir "sccv.exe"

if ($CliOnly) {
    $outFile = Join-Path $outDir "sccv-cli.exe"
    $ldflags = "-s -w"
}

# Locate go.exe: PATH first, then the known worktools install, else stop.
$go = $null
$goCmd = Get-Command "go.exe" -ErrorAction SilentlyContinue
if ($goCmd -ne $null) {
    $go = $goCmd.Source
} elseif (Test-Path "D:\WORKTOOLS\go\bin\go.exe") {
    $go = "D:\WORKTOOLS\go\bin\go.exe"
} else {
    [Console]::Error.WriteLine("go.exe not found: not on PATH and D:\WORKTOOLS\go\bin\go.exe is missing.")
    [Console]::Error.WriteLine("Install Go 1.21+ or fix the fallback path inside scripts\build-sccv.ps1.")
    exit 3
}

# Repo root = parent of the scripts directory this script lives in.
$repoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $repoRoot
try {
    if (-not (Test-Path $outDir)) {
        New-Item -ItemType Directory -Path $outDir | Out-Null
    }

    & $go build -trimpath -ldflags $ldflags -o $outFile .
    if ($LASTEXITCODE -ne 0) {
        [Console]::Error.WriteLine("go build failed with exit code $LASTEXITCODE.")
        exit $LASTEXITCODE
    }

    if (-not (Test-Path $outFile)) {
        [Console]::Error.WriteLine("go build reported success but $outFile is missing.")
        exit 4
    }

    Write-Host ("built " + $outFile + " (ldflags: " + $ldflags + ")")
    exit 0
} finally {
    Pop-Location
}
