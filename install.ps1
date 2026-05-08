$ErrorActionPreference = "Stop"

$Repo = "authprobe/mcpctl"
$Version = if ($env:MCPCTL_VERSION) { $env:MCPCTL_VERSION } else { "edge" }
$InstallDir = if ($env:MCPCTL_INSTALL_DIR) { $env:MCPCTL_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "mcpctl\bin" }
$AllowGoFallback = if ($env:MCPCTL_ALLOW_GO_FALLBACK) { $env:MCPCTL_ALLOW_GO_FALLBACK } else { "1" }
$GoPackage = "github.com/authprobe/mcpctl/cmd/mcpctl@main"

$Arch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq "Arm64") {
  "arm64"
} else {
  "x86_64"
}

$Asset = "mcpctl_Windows_$Arch.zip"
if ($Version -eq "latest") {
  $Url = "https://github.com/$Repo/releases/latest/download/$Asset"
} else {
  $Url = "https://github.com/$Repo/releases/download/$Version/$Asset"
}

$TempDir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())
$Archive = Join-Path $TempDir $Asset
$DownloadOk = $false

New-Item -ItemType Directory -Path $TempDir | Out-Null
try {
  Write-Host "Downloading mcpctl $Version for Windows/$Arch..."
  try {
    Invoke-WebRequest -Uri $Url -OutFile $Archive -UseBasicParsing
    $DownloadOk = $true
  } catch {
    Write-Warning "Release artifact download failed: $($_.Exception.Message)"
  }

  if ($DownloadOk) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    Expand-Archive -Path $Archive -DestinationPath $TempDir -Force
    Copy-Item -Path (Join-Path $TempDir "mcpctl.exe") -Destination (Join-Path $InstallDir "mcpctl.exe") -Force
    Write-Host "Installed mcpctl to $(Join-Path $InstallDir "mcpctl.exe")"
  } else {
    if ($AllowGoFallback -ne "1") {
      throw "failed to download $Url"
    }
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
      throw "failed to download $Url and Go fallback is unavailable because Go is not installed"
    }
    Write-Host "Release artifact unavailable; falling back to go install..."
    $env:GOPROXY = "direct"
    go install $GoPackage
    $GoBin = go env GOBIN
    if ([string]::IsNullOrWhiteSpace($GoBin)) {
      $GoBin = Join-Path (go env GOPATH) "bin"
    }
    $InstallDir = $GoBin
    Write-Host "Installed mcpctl to $(Join-Path $GoBin "mcpctl.exe")"
  }
} finally {
  Remove-Item -Path $TempDir -Recurse -Force -ErrorAction SilentlyContinue
}

$PathParts = $env:PATH -split ";"
if ($PathParts -notcontains $InstallDir) {
  Write-Host "If mcpctl is not on PATH, add $InstallDir to your user PATH."
}
