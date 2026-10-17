# Manual Test Contract

Use this contract when a task or pull request asks for command or native desktop evidence.

## Before execution

- Treat the issue or PR's verification steps as the contract. Preserve command text and expected results unless the task explicitly changes them.
- Read `AGENTS.md`, relevant `.agents/knowledge/` files, and project run instructions. Resolve the repository root and exact Go, npm, or Wails command from those sources.
- Use project folders explicitly added to the app. Each folder's `nala.yml` is one project and may define several services under `projects`; resolve each selected service's `cwd`, `run`, `port`, and `log` from its entry. Do not infer arbitrary host processes.
- Use documented fixture projects. Do not introduce fake agent responses or mock/stub Codex completions or core agent-client methods in tests.

## Bash execution

Use Bash by default, including on Windows when Git Bash or WSL is available. Keep setup separate from verification. Make each verification command independently pasteable. Do not use a fail-fast wrapper, helper script, full-flow script, loop, function, or bulk runner that hides which command failed.

Capture each target command's immediate status before assertions or follow-up commands:

```bash
go test ./...
target_status=$?
printf 'target command exit status: %s\n' "$target_status"
```

For frontend build verification, run from `frontend/`:

```bash
npm run build
target_status=$?
printf 'target command exit status: %s\n' "$target_status"
```

For the native app, use the documented Wails command from the repository root. If opening or interacting with the native window is required, record the actual UI action and visible result separately from command output. Do not treat a static screenshot, a browser tab, or a successful build as proof that a control works.

## Classification

Report one record per step:

| Field | Required value |
| --- | --- |
| Command/action | Exact command that ran, or the native action taken |
| Immediate status | The command's captured exit status; use `not run` for UI-only actions |
| Observed result | Relevant command output or visible UI result |
| Expected outcome | The task or PR contract |
| Classification | `pass`, `expected result`, `fail`, or `not run` |
| Project fixture | Configured project name and non-sensitive fixture description |
| Limitation | Exact unverified native action or `none` |

Use `pass` only when the observed status and result match the contract. Use `expected result` for an intentionally nonzero result that matches the contract. Use `fail` for an unexpected status or behavior. A command or action that was not performed is `not run`.

## Evidence boundaries

- Go unit tests and controlled local project fixtures prove only the exercised code paths.
- A frontend production build proves compilation and bundling, not native interaction.
- `wails dev` proves the app can launch only when it exits successfully and the native window is observed. Record user-visible actions separately.
- A local port-conflict fixture proves only the exercised configured-port flow. Record the process identity checks and confirmation result without exposing unrelated process arguments.
- Do not claim arbitrary process discovery, remote service behavior, or native UI acceptance unless that behavior was in scope and directly exercised.

## Failure handling

Preserve diagnostic output only in temporary or ignored storage and keep secrets and unrelated process arguments out of it. Leave the terminal open after a failure. Report the exact command, immediate status, affected path or behavior, and whether it is pre-existing or task-related. Never call a partial compile or unrun native flow a pass.

## Tracker readiness artifacts

When issue creation or visual references are in scope, also follow `.agents/workflows/delivery.md` and the relevant Linear templates. Verify the saved tracker description and comments through the connected Linear integration or documented API fallback. Never use a browser or screen capture to inspect a Linear issue. Preserve user-supplied reference material and verify inline diagram source pairing through the API.
