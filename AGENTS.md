# Agent Guidelines

This is the public repository for the `mcpctl` command line tool.

## Project Repository Map

- `/Users/chintan/code/mcpctl`: public `mcpctl` CLI.
- `/Users/chintan/code/mcpd`: backend for `mcpctl.io`.
- `/Users/chintan/code/mcpd-console`: interface/BFF that talks to `mcpd`.
- `/Users/chintan/code/mcpd-marketing`: frontend marketing site that routes users into `mcpd-console`.

## Rules

- Keep this repository safe for public visibility.
- Do not add internal planning, roadmap, customer, pricing, or commercial-service materials.
- Keep public documentation focused on install, usage, contribution, and implementation details.
- If code is added later, every function added or modified must include a concise documentation comment.

## GitHub CLI Identity

Before running any `gh` command in this repository or sibling MCPCTL
repositories, bind `GH_TOKEN` to the GitHub account named by the repo-local git
commit identity. For these repos, `git config user.name` is expected to be
`authprobe`; do not rely on the global active `gh` account.

```sh
export GH_TOKEN="$(env -u GH_TOKEN -u GITHUB_TOKEN gh auth token --hostname github.com --user "$(git config --get user.name)")"
```

If that command fails, stop and fix the `gh` auth state rather than falling
back to another GitHub account.

## Validation

When implementation code exists, document and run the relevant test command before committing.
