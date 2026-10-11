# Test-only PR description template

Use for regression coverage or an intentional test handoff. Replace every placeholder. Do not introduce faked Codex replies, LLM completions, or core agent-client methods in tests. Local project fixtures may prove only the exercised Nala Ctrl behavior.

## Linked work

- Linear issue: [TEAM-123 and URL]
- Paired implementation issue: [ID and URL, or `None`]
- Outcome: [Green verification | Intentional red handoff]
- Handoff commit/branch: [Commit and branch, or `Not applicable`]
- Intentional-red merge authorization: [Issue requirement, or `Not applicable`]

## Review and merge order

- Delivery shape: [Single focused PR | Stacked PR | Parallel PR group]
- This PR's review position: [Standalone | PR 1 of N | PR N of N | Parallel member A/B]
- Base branch: [main or predecessor branch]
- Depends on: [PR/commit and exact behavior, or `None`]
- Review order: [Exact order]
- Merge order and conditions: [Exact sequence and required checks]
- Parallel group: [Group and independent members, or `None`]
- Human-verification focus: [Fixture boundary or observable process/UI behavior]

## Test contract

- Behavior under test: [Observable contract].
- Confirmed current behavior: [Exact package or UI flow and evidence].
- Expected outcome: [Passing behavior or exact intentional assertion].
- Production code changed: [No, or list paths and reason].

## Coverage matrix

| Scenario | Test seam | Fixture | Expected assertion |
| --- | --- | --- | --- |
| Configuration valid/invalid | [Go package] | [Temporary `nala.yml`] | [Parsed result or validation error] |
| Process start/stop/output | [Go package] | [Temporary command/project] | [Lifecycle/log result] |
| Port ownership/takeover | [Go package] | [Local listener] | [Confirmation and exact owner result] |
| Wails UI flow | [Native window] | [Configured project] | [Visible result, or not applicable] |

## Isolation and boundaries

- Runtime/dependencies: [Go, Node, Wails/platform requirements].
- Fixture creation and cleanup: [Temporary paths and process cleanup].
- Network usage: [None or exact required boundary].
- Host processes: [Only a process created by the test may be targeted].
- Mocking/fakes: [What is controlled and why].
- Repeatability: [Evidence the test can run again cleanly].

## Results

| Exact command | Expected result | Actual status | Evidence |
| --- | --- | ---: | --- |
| `go test ./...` | [0 or designated assertion] | [status] | [Packages/assertion] |
| `[additional exact command]` | [Expected result] | [status/not run] | [Evidence] |
| Native Wails action | [Visible result] | [Pass/fail/not run] | [Observed action] |

For an intentional-red handoff, identify the exact expected assertion and show that compile/setup completed first. All unrelated checks must be green, and the issue must explicitly authorize the handoff.

## Known limitations and pre-existing failures

- [Exact command, status, affected path, and reason; or `None known`].

## Reviewer focus

- [Assertion, fixture realism, lifecycle cleanup, or process-safety boundary].

## Completion self-audit

- [ ] The outcome mode is explicit and the result matches it.
- [ ] Fixtures are isolated, cleaned up, and do not terminate unrelated host processes.
- [ ] Tests do not fake Codex responses, LLM completions, or core agent-client methods.
- [ ] Exact commands, statuses, and native limitations are recorded.
- [ ] No live behavior is claimed from a mock, unit test, or fixture alone.
- [ ] PR review/merge order and Linear handoff are explicit.
