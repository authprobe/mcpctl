#!/bin/sh
set -eu

REPO="authprobe/mcpctl"
VERSION="${MCPCTL_VERSION:-edge}"
INSTALL_DIR="${MCPCTL_INSTALL_DIR:-}"
ALLOW_GO_FALLBACK="${MCPCTL_ALLOW_GO_FALLBACK:-1}"
GO_PACKAGE="github.com/authprobe/mcpctl/cmd/mcpctl@main"

case "$(uname -s)" in
  Darwin)
    ASSET_OS="Darwin"
    ;;
  Linux)
    ASSET_OS="Linux"
    ;;
  *)
    echo "unsupported operating system: $(uname -s)" >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  arm64|aarch64)
    ASSET_ARCH="arm64"
    ;;
  x86_64|amd64)
    ASSET_ARCH="x86_64"
    ;;
  *)
    echo "unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

if [ -z "${INSTALL_DIR}" ]; then
  if [ -d "/usr/local/bin" ] && [ -w "/usr/local/bin" ]; then
    INSTALL_DIR="/usr/local/bin"
  else
    INSTALL_DIR="${HOME}/.local/bin"
  fi
fi

ASSET="mcpctl_${ASSET_OS}_${ASSET_ARCH}.tar.gz"
if [ "${VERSION}" = "latest" ]; then
  URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"
else
  URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
fi

TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT HUP INT TERM
ARCHIVE="${TMPDIR}/${ASSET}"
download_ok=0

echo "Downloading mcpctl ${VERSION} for ${ASSET_OS}/${ASSET_ARCH}..."
if command -v curl >/dev/null 2>&1; then
  if curl -fsSL "${URL}" -o "${ARCHIVE}"; then
    download_ok=1
  fi
elif command -v wget >/dev/null 2>&1; then
  if wget -q "${URL}" -O "${ARCHIVE}"; then
    download_ok=1
  fi
else
  echo "curl or wget is required to download release artifacts." >&2
fi

if [ "${download_ok}" -eq 1 ]; then
  mkdir -p "${INSTALL_DIR}"
  tar -xzf "${ARCHIVE}" -C "${TMPDIR}"
  cp "${TMPDIR}/mcpctl" "${INSTALL_DIR}/mcpctl"
  chmod 0755 "${INSTALL_DIR}/mcpctl"
  echo "Installed mcpctl to ${INSTALL_DIR}/mcpctl"
else
  if [ "${ALLOW_GO_FALLBACK}" != "1" ]; then
    echo "failed to download ${URL}" >&2
    exit 1
  fi
  if ! command -v go >/dev/null 2>&1; then
    echo "failed to download ${URL}" >&2
    echo "Go fallback is unavailable because Go is not installed." >&2
    exit 1
  fi
  echo "Release artifact unavailable; falling back to go install..."
  GOPROXY=direct go install "${GO_PACKAGE}"
  GOBIN="$(go env GOBIN)"
  if [ -z "${GOBIN}" ]; then
    GOBIN="$(go env GOPATH)/bin"
  fi
  INSTALL_DIR="${GOBIN}"
  echo "Installed mcpctl to ${GOBIN}/mcpctl"
fi

case ":${PATH}:" in
  *":${INSTALL_DIR}:"*)
    ;;
  *)
    echo "If mcpctl is not on PATH, add this to your shell profile:"
    echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    ;;
esac
