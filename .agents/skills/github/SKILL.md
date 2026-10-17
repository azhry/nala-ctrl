---
name: github-delivery
description: Inspect Git history and deliver repository work through commits, branches, pull requests, issues, reviews, and checks. Use when implementing and publishing code or managing GitHub work.
---

# GitHub delivery

Git is the local version-control system. GitHub hosts repositories, pull requests, issues, reviews, checks, and releases. Follow the active repository's `AGENTS.md` and delivery workflow for branch, review, and publication rules.

## Workflow

1. Inspect `git status --short --branch`, the current branch, remote, and repository instructions before editing or staging.
2. Read [TOOLING.md](TOOLING.md), resolve the official GitHub CLI, and run its non-interactive authentication and repository-access checks before source edits.
3. For an existing PR, continue on its branch and update that PR. For new work, follow the repository's required base branch and topic-branch convention.
4. Preserve unrelated work. Stage only intended paths, inspect the staged diff, and record exact verification results.
5. Follow the repository's explicit delivery authorization. Some repositories authorize commit, push, and PR creation as part of an implementation request; honor that instruction without asking again. Never publish secrets or bypass an authorization failure.
6. Update the linked issue with the PR and handoff evidence, then report commit, checks, limitations, and PR URL.

## CLI and API fallback

Use the official `gh.exe`/`gh` CLI or connected GitHub app. Do not invoke interactive login or inject credentials from project config. If both app and CLI are unavailable, read [TOOLING.md](TOOLING.md) before using the bundled API fallback; consult official GitHub documentation for any operation the helper does not support.

The helper reads `GITHUB_TOKEN` only from the current process environment. Follow the active repository's non-printing secret-loading rules; never print, commit, or transmit a credential.

## Pull requests

Update an existing PR rather than creating a duplicate. Build the body from the applicable project template, include the exact review/merge order, issue link, commands and statuses, and known limitations. Keep automated regression evidence separate from unrun human acceptance. Use a draft PR by default unless the repository or user asks otherwise.
