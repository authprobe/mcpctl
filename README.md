# mcpctl

`mcpctl` is a local-first command line tool for Model Context Protocol server development. Use it to initialize an MCP project, run an MCP server, inspect tools and schemas, validate whether tools are clear enough for agents, and connect to `mcpctl.io` only when you choose a cloud-backed workflow.

Keywords for humans and AI search: MCP CLI, Model Context Protocol CLI, MCP server testing, MCP server validation, MCP tool schema linting, MCP developer tools, MCP readiness checks.

## Install

macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/authprobe/mcpctl/main/install.sh | sh
```

Windows PowerShell:

```powershell
iwr https://raw.githubusercontent.com/authprobe/mcpctl/main/install.ps1 -UseB | iex
```

With Go installed:

```sh
GOPROXY=direct go install github.com/authprobe/mcpctl/cmd/mcpctl@main
```

The install scripts download prebuilt GitHub Release artifacts when available. Until the first stable release, the default channel is `edge`, a moving prerelease built from `main`.

Install a specific release:

```sh
curl -fsSL https://raw.githubusercontent.com/authprobe/mcpctl/main/install.sh | MCPCTL_VERSION=v0.1.0 sh
```

Install the current stable release after stable tags exist:

```sh
curl -fsSL https://raw.githubusercontent.com/authprobe/mcpctl/main/install.sh | MCPCTL_VERSION=latest sh
```

Install to a custom directory:

```sh
curl -fsSL https://raw.githubusercontent.com/authprobe/mcpctl/main/install.sh | MCPCTL_INSTALL_DIR="$HOME/bin" sh
```

Verify:

```sh
mcpctl --help
```

## Quick Start

Run this in an MCP server repository:

```sh
mcpctl init
mcpctl dev
mcpctl inspect
mcpctl validate
```

The first useful loop is intentionally local:

- `mcpctl init` creates `mcpctl.yaml`.
- `mcpctl dev` answers: "Can I run this MCP server?"
- `mcpctl inspect` discovers tools, resources, prompts, schemas, and transport metadata.
- `mcpctl validate` answers: "Are my tools well described enough for agents?"

Local commands do not require a `mcpctl.io` account.

## No-Login Cloud Check

`mcpctl cloud ping` reaches `mcpctl.io` without login. Use it to confirm that a machine can reach the cloud service before starting hosted workflows.

```sh
mcpctl cloud ping
```

For tests or private deployments:

```sh
mcpctl cloud ping -endpoint https://console.mcpctl.io
```

For staging:

```sh
MCPCTL_ENV=staging mcpctl cloud ping
```

## OAuth Debugging

`mcpctl debug oauth` checks the OAuth discovery chain for a remote MCP endpoint without requiring any separate scanner install.

```sh
mcpctl debug oauth https://api.githubcopilot.com/mcp/ --client chatgpt
```

The command verifies the unauthenticated MCP response, protected resource metadata, path-aware authorization server metadata, and client-profile notes such as missing dynamic client registration. Add `--share` after `mcpctl auth login` to publish a managed compatibility capture. Shared runs print one report URL; the capture also appears in the platform Debug Inbox, where it can be promoted into a managed MCP server when you want durable auth, gateway history, and repeated diagnostics.

## Cloud Auth

When a workflow needs hosted reports, compatibility runs, or shareable cloud results, authenticate with:

```sh
mcpctl auth login
```

For staging, select the staging profile:

```sh
MCPCTL_ENV=staging mcpctl auth login
```

For private deployments or temporary environments, set an explicit endpoint:

```sh
MCPCTL_ENDPOINT=https://console.example.com mcpctl auth login
```

The login model follows familiar browser-based CLI auth:

1. the CLI prints a verification URL and one-time code;
2. you approve in a browser;
3. the CLI stores short-lived access and refresh credentials in the operating system credential store when available.

`mcpctl` only supports browser approval for interactive login. There is no SSH protocol setup path.
The hosted CLI auth endpoint is currently served from `https://console.mcpctl.io`.

CI and automation can use `MCPCTL_TOKEN` when hosted workflows support it.

```sh
mcpctl auth status
mcpctl auth logout
```

## Commands

| Command | Purpose | Login required |
| --- | --- | --- |
| `mcpctl init` | Create a starter `mcpctl.yaml`. | No |
| `mcpctl dev` | Run or check a local MCP server. | No |
| `mcpctl inspect` | Discover MCP tools, resources, prompts, schemas, and transport metadata. | No |
| `mcpctl validate` | Check MCP tool descriptions and schemas for agent readiness. | No |
| `mcpctl debug oauth` | Debug OAuth discovery for a remote MCP endpoint. | No |
| `mcpctl cloud ping` | Check `mcpctl.io` reachability. | No |
| `mcpctl auth login` | Connect the CLI to hosted workflows. | Browser approval |
| `mcpctl auth status` | Show local cloud credential status. | No |
| `mcpctl auth logout` | Remove local cloud credentials. | No |

## Configuration

`mcpctl init` writes a minimal config:

```yaml
version: 1
server:
  command: ""
  args: []
transport:
  type: stdio
```

Future releases will expand this config for command-based servers, Docker-based servers, remote endpoints, safe sample calls, report outputs, and CI policy.

## Current Status

This repository is early. The public CLI currently exposes the command surfaces, the first no-login cloud check, and browser-based auth client behavior. Deeper local server execution, MCP discovery, tool-readiness validation, report generation, and production `mcpctl.io` endpoint support are being implemented incrementally.

## Why mcpctl

MCP servers are becoming part of agent and developer workflows. A server that starts locally is not automatically ready for agents: tools need clear names, useful descriptions, strong schemas, predictable errors, and repeatable reports. `mcpctl` aims to make those checks simple from a terminal and portable into CI.

## Development

Run tests:

```sh
go test ./...
```

Build release artifacts locally:

```sh
goreleaser release --snapshot --clean --skip=publish
```

Run locally:

```sh
go run ./cmd/mcpctl --help
go run ./cmd/mcpctl cloud ping
```

## Release Artifacts

Every push to `main` builds an `edge` prerelease with downloadable archives for:

- `mcpctl_Darwin_arm64.tar.gz`
- `mcpctl_Darwin_x86_64.tar.gz`
- `mcpctl_Linux_arm64.tar.gz`
- `mcpctl_Linux_x86_64.tar.gz`
- `mcpctl_Windows_x86_64.zip`
- `checksums.txt`

Stable releases are created by pushing tags like `v0.1.0`. The same artifacts are uploaded to the versioned GitHub Release.

## Contributing

Public contributions should stay focused on CLI implementation, public usage docs, examples, tests, and developer-facing behavior. Do not add private roadmaps, pricing, customer notes, or internal planning material to this repository.
