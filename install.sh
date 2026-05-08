#!/bin/sh
set -eu

PACKAGE="github.com/authprobe/mcpctl/cmd/mcpctl@main"

if ! command -v go >/dev/null 2>&1; then
  echo "mcpctl installer needs Go until release binaries are published." >&2
  echo "Install Go from https://go.dev/dl/, then rerun this command:" >&2
  echo "  go install ${PACKAGE}" >&2
  exit 1
fi

echo "Installing mcpctl with go install..."
GOPROXY=direct go install "${PACKAGE}"

GOBIN="$(go env GOBIN)"
if [ -z "${GOBIN}" ]; then
  GOBIN="$(go env GOPATH)/bin"
fi

echo "Installed mcpctl to ${GOBIN}/mcpctl"
echo "If mcpctl is not on PATH, add this to your shell profile:"
echo "  export PATH=\"${GOBIN}:$PATH\""
