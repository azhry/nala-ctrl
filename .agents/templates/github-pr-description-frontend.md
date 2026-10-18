# Wails desktop UI PR description template

Use for the Nala Ctrl native window, React/TypeScript UI, user interaction, accessibility, or Wails event behavior. Replace every placeholder and remove a section only when it does not apply. Browser and mobile layouts are not the product surface; verify the native desktop window.

## Linked work

- Linear issue: [TEAM-123 and URL]
- Parent/source issue: [issue ID and URL, or `None`]
- Design/reference: [URL or attachment, or `None`]
- Depends on: [PR/commit and delivered behavior, or `None`]

## Review and merge order

- Delivery shape: [Single focused PR | Stacked PR | Parallel PR group]
- This PR's review position: [Standalone | PR 1 of N | PR N of N | Parallel member A/B]
- Base branch: [main or predecessor branch]
- Depends on: [PR/commit and exact behavior, or `None`]
- Review order: [Exact order]
- Merge order and conditions: [Exact sequence and required checks]
- Parallel group: [Group and independent members, or `None`]
- Human-verification focus: [One native desktop journey]

## Summary

- [Visible user outcome in the Wails window.]
- [Interaction, state, event, or error outcome.]

## Scope

### Included

- [Views, controls, states, and frontend paths changed.]

### Excluded

- [Nearby behavior intentionally not changed.]

## Native desktop journey

| Entry point | User action | Expected visible outcome | Verified |
| --- | --- | --- | --- |
| [App launch or workspace picker] | [Pointer/keyboard action] | [Window state, project, log, or message] | [Pass/fail/not run] |

List every changed interactive control and its observable outcome. Explain controls intentionally disabled or removed. Do not leave a visible no-op.

## Accessibility and visual behavior

- Keyboard navigation and activation: [Observed result]
- Labels, roles, focus, and error announcements: [Observed result]
- Window sizing and overflow: [Observed result in the native window]
- Design fidelity: [Compared reference and result, or `Not applicable`]
- Screenshots: [Temporary path/link outside the repository, or `Not captured`]

## Data and state behavior

- Wails-bound methods/events used: [Exact names, or `None`]
- Loading/empty/error states: [Observable results]
- Project/config mapping: [How selected `nala.yml` data appears]
- Log updates and process state: [Event and refresh behavior]

## Verification

| Command/action | Exit/result | Evidence |
| --- | ---: | --- |
| `cd frontend && npm run build` | [status] | [TypeScript and Vite result] |
| `go test ./...` | [status, when relevant] | [Relevant backend coverage] |
| `wails dev` native flow | [Pass/fail/not run] | [Window and actions observed] |

Use the exact commands and statuses. A browser view, screenshot, static build, or component render does not prove native interaction. If the Wails window was not opened, state which actions remain unverified.

## Known limitations and pre-existing failures

- [Exact command, status, affected path, and reason; or `None known`].

## Reviewer focus

- [Highest-risk control or state transition].
- [Specific component, event, or keyboard behavior].

## Completion self-audit

- [ ] Every issue requirement and changed control has an outcome or explicit blocker.
- [ ] Normal native-window navigation works and controls are not unexplained no-ops.
- [ ] Loading, empty, error, and state-changing behavior is recorded where applicable.
- [ ] Native desktop behavior is directly observed or marked unverified.
- [ ] Exact build/test commands and statuses are recorded.
- [ ] PR review/merge order and Linear handoff are explicit.
