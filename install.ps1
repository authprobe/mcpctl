$ErrorActionPreference = "Stop"

$package = "github.com/authprobe/mcpctl/cmd/mcpctl@latest"

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
  Write-Error "mcpctl installer needs Go until release binaries are published. Install Go from https://go.dev/dl/, then run: go install $package"
}

Write-Host "Installing mcpctl with go install..."
go install $package

$goBin = go env GOBIN
if ([string]::IsNullOrWhiteSpace($goBin)) {
  $goBin = Join-Path (go env GOPATH) "bin"
}

Write-Host "Installed mcpctl to $goBin\mcpctl.exe"
Write-Host "If mcpctl is not on PATH, add $goBin to your user PATH."
