# Linear agent comment template

Post this comment after the human-readable issue description is saved and before implementation begins. Read the issue, relations, comments, project, team workflow states, relevant code, and tests first. Preserve user-supplied references verbatim. Re-read the saved issue and comment through the Linear connector or documented API fallback. Never use a browser or screen capture to inspect Linear issue content.

## Category

**[Bug | Feature | Refactor | Chore | Research] — [short qualifier].**

## Problem analysis

### Source requirements

- [Every requirement from the user request and preserved reference, stated as a checkable outcome.]
- Parent/source issue: [ID and inherited requirements, or `None`].

### Confirmed findings

1. **[Current behavior or gap]** — [code-backed evidence and exact paths].
2. **[Relevant existing behavior]** — [configuration, process, event, or UI evidence and exact paths].

### Discovery evidence

- Source review: [finding and paths].
- Runtime/native UI: [observed action and result, or `Not performed` with reason].
- Discovery classification: [source review | runtime validation | both].

### Affected user flows

- [Choose workspace → select configured project → expected result].
- [Run/stop/log/port-conflict path, when relevant].

## Scope boundary

- In scope: [specific behavior and affected files/components].
- Out of scope: [nearby behavior excluded by the issue or project architecture].
- Assumptions/open questions: [only unresolved decisions; label assumptions].
- Process safety: manage only the project configured by the selected `nala.yml`; any port-owner termination requires confirmation, a fresh identity check, and exact-process targeting.

## Implementation plan

1. [Concrete Go/configuration/process-manager change and expected outcome].
2. [Concrete Wails event or React UI change, when relevant].
3. Add or update regression coverage at [Go package, frontend build, or other relevant seam].
4. Update user-facing documentation only when the product behavior or setup changes.

## Definition of Done

- [Every source requirement maps to an observable implemented outcome.]
- Only the project declared in the selected `nala.yml` is shown and managed.
- [Configuration validation, logs, process lifecycle, port safety, and UI states relevant to scope are correct.]
- Regression checks pass; failures are recorded with exact commands and affected paths.
- Native Wails behavior is manually exercised when required, or clearly recorded as unverified.
- The change is committed, pushed to its authorized branch, linked from the existing or new PR, and reflected in Linear.
- Tests never fake Codex responses, LLM completions, or core agent-client methods.

## Correctness checks

- Go: `[exact command, expected result, and relevant package]`.
- Frontend: `[exact command from frontend/, expected result]`.
- Native desktop: `[specific user actions and visible outcomes, or Not run with reason]`.
- Process safety: [configured port owner, confirmation behavior, and process identity recheck when relevant].
- Logs: [configured/default log path and streamed output behavior when relevant].
- Known limitations or baseline failures: [exact command, status, path, and reason, or `None known`].

## Required execution protocol

1. Follow repository `AGENTS.md` and the routing workflow before changing source or agent guidance.
2. Treat every listed requirement, flow, state, and safety check as in scope; update this contract if new evidence changes it.
3. Preserve unrelated work. Stage only paths that belong to this issue and inspect the staged diff before commit.
4. Record exact verification commands and statuses. Do not describe a build or native action as passed unless it was actually run and observed.
5. Before handoff, update the Linear comment with commit, PR, verification results, and limitations; leave issue state consistent with the team's workflow.

## Completion self-audit

- [ ] Every source requirement is implemented or documented as a blocker.
- [ ] Process management remains limited to the one project in the selected `nala.yml`.
- [ ] Port conflict handling cannot terminate a process without confirmation and revalidation.
- [ ] Go/frontend commands and native actions have exact results or are marked unrun.
- [ ] No unrelated files, secrets, generated diagnostics, or local session data are included.
- [ ] Commit, PR, issue state, and delivery evidence agree.
