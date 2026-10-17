# GitHub operations

Use Git for local history, the connected GitHub integration when available, and the official GitHub CLI when the connector lacks a needed operation. Follow the active repository's `AGENTS.md` and `.agents/workflows/delivery.md` for branch, PR shape, and publication rules. This repository is a local Wails desktop app; do not add service, database, or staging assumptions to delivery evidence.

## Git commands

```bash
git status --short --branch
git diff --check
git add <intended-paths>
git diff --cached --check
git diff --cached
git commit -m "feat(scope): summary"
git push -u origin <task-branch>
```

The repository instructions determine whether commit and push are authorized. Preserve unrelated files and stage explicit paths only.

## Resolve and preflight the official CLI

Before source edits or delivery commands, verify the official GitHub CLI and repository access. Do not assume `gh` on PATH is the CLI; a Node/npm package can shadow it. Never invoke interactive login or load a token from a project config file.

On PowerShell, resolve a real executable outside Node/npm paths, then keep and reuse its full path:

```powershell
$gh = Get-Command gh.exe -All |
  Where-Object { $_.Source -notmatch '(?i)node_modules|\\nodejs\\gh(?:\.cmd|\.ps1)?$' } |
  Select-Object -First 1 -ExpandProperty Source
if (-not $gh) { throw 'Official GitHub CLI executable not found' }
& $gh --version
& $gh auth status
& $gh repo view OWNER/REPO --json nameWithOwner,viewerPermission
```

On POSIX shells:

```bash
type -a gh
gh_bin="$(command -v gh)"
"$gh_bin" --version
"$gh_bin" auth status
"$gh_bin" repo view OWNER/REPO --json nameWithOwner,viewerPermission
```

All three checks must succeed and identify the intended repository. The bundled `gh_preflight.ps1` performs the same checks on Windows:

```powershell
& .agents/skills/github/scripts/gh_preflight.ps1 -Repository OWNER/REPO
```

If GitHub access fails, stop before implementation or publication and report the exact integration or credential that needs repair.

## GitHub integration and CLI

Discover exact callable names from the active tool list. Connector schemas differ by host, so inspect the available tool definition before calling. Common operations include create/fetch/search PR, add comment, inspect checks, and compare commits.

Use the resolved CLI path for PR operations. Check for an existing PR before creating one, and pass repository, base, and head explicitly:

```bash
gh pr list --repo OWNER/REPO --head <branch> --json number,url,state,title,headRefName,baseRefName
gh pr view <number> --repo OWNER/REPO --json url,state,isDraft,mergeable,statusCheckRollup
```

Follow the repository's PR template, including review and merge order, issue link, exact command results, and native desktop verification status. Update the existing PR for an existing branch; do not create a duplicate. Do not merge unless the user or repository workflow explicitly authorizes it.

## HTTP API fallback

Use the bundled [`gh_tooling.py`](scripts/gh_tooling.py) only when both the connector and CLI are unavailable or lack the required operation. It reads `GITHUB_TOKEN` from the current process only. Follow the repository's non-printing credential-loading rules; never print, commit, log, or transmit the value. If an operation is missing, consult the official GitHub REST schema or documentation before extending the helper. Keep helper tests offline; they do not prove GitHub-side behavior.
