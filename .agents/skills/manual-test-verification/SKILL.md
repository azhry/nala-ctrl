---
name: manual-test-verification
description: Verify task and pull-request manual steps for Nala Ctrl's Go and Wails desktop app, capturing each command's immediate status and clearly separating automated evidence from native UI checks. Use when a task or pull request asks for runnable verification evidence or a report of commands and results.
---

# Manual Test Verification

Use this skill to produce reviewable evidence without overstating what automated checks or local process fixtures prove.

## Quick start

1. Read the task or pull-request manual steps, `AGENTS.md`, and the relevant `.agents/knowledge/` files before choosing commands or assumptions.
2. Verify the repository root and use the documented Go, npm, and Wails commands. Resolve project fixture paths, ports, and log locations from the selected `nala.yml` and project knowledge.
3. Test only configured projects. Do not inspect or manage arbitrary host processes. Make each numbered command independently pasteable and capture its immediate status. Keep setup, assertions, and cleanup separate; do not create or hand off a helper script or bulk runner.
4. For native UI verification, state the action and visible result. Browser-only observation does not prove interaction in the Wails desktop window.
5. Compare observed status and output with the stated expected result. Classify each command as pass, expected result, or fail; preserve unexpected nonzero statuses.
6. Report exact commands, immediate statuses, relevant output, expected outcomes, fixture identity, and any unverified native UI actions.

## Readiness checks

- For Linear visual references, confirm required UML sequence semantics and the exact human-description heading schema. Attachment counts alone are insufficient.
- If the tracker template requires Steps 0–4, confirm separate Bash blocks, per-step statuses, no unresolved placeholders, and explicit limitations for unrun behavior.
- Read the active repository's `AGENTS.md`; do not assume another repository's guardrails apply.
- Capture each target command's immediate status before assertions or other commands, even when a nonzero result is expected.

## Guardrails

- Keep Bash commands independently pasteable and leave the terminal open after failures. Capture a target command's status before follow-up work.
- Do not include credentials or unrelated process arguments in output, logs, screenshots, or reports.
- Go tests and controlled local project fixtures are regression evidence. They do not prove a native desktop interaction that was not exercised.
- If the Wails UI was not opened or a native action was not exercised, identify the exact action that remains unverified.
- Preserve documented Go, npm, and Wails commands and the configured project's ports and paths. Do not silently substitute a process, port, or `nala.yml` entry.

For the reporting contract, read [manual-test-contract.md](references/manual-test-contract.md).
