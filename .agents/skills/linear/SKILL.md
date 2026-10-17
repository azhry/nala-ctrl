---
name: linear-issue-management
description: Read, update, triage, relate, and report Linear issues and projects. Use when a task names a Linear issue or asks for Linear work-item changes.
---

# Linear issue management

Use Linear for task scope, status, comments, and delivery links. Follow the active repository's `AGENTS.md` and `.agents/workflows/delivery.md` for project-specific sequencing and verification.

## Read and update

1. Discover the connected Linear tools and use them when authenticated.
2. Never use a browser, browser automation, or screen capture to inspect a Linear issue, its comments, relations, state, or rendered content. Read and re-read issues through the connector or documented API.
3. Before mutation, read the exact issue, comments, relations, project, and valid team states. Resolve ambiguous names to IDs before changing anything.
4. If the connector is unavailable or returns an authorization error, use the documented API fallback in [TOOLING.md](TOOLING.md). Do not retry with guessed GraphQL fields or bypass authorization failures.
5. Preserve user-supplied reference material verbatim. Follow the repo's issue-description template and readiness gate before implementation.
6. After implementation, attach the existing PR, report verified command outcomes and limitations, and move the issue to the team's valid review state. Mark complete only when the PR is merged or the team's process confirms completion.

## API fallback and credentials

The fallback helper is `scripts/linear_tooling.py`. It reads only `LINEAR_API_KEY` from the current process environment and never reads `.agents/config.md` itself. Load that one allowlisted key through the non-printing mechanism in `AGENTS.md`; never paste a literal token into a command, file, URL, issue, or transcript, and never dot-source a project config file.

Read [TOOLING.md](TOOLING.md) for the exact connector and fallback operation map. If an operation is missing, use the official Linear GraphQL schema or documentation before adding it, and add isolated tests for the helper. Never expose credentials or local session data.
