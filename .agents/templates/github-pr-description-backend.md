# Go backend PR description template

Use for Go configuration, process lifecycle, port ownership, log capture, and Wails-bound backend behavior. Replace every placeholder and remove a section only when it does not apply. Do not add remote-service, authentication, database, or deployment claims to this local desktop app.

## Linked work

- Linear issue: [TEAM-123 and URL]
- Parent/source issue: [issue ID and URL, or `None`]
- Depends on: [PR/commit and delivered behavior, or `None`]
- Unblocks: [issue IDs, or `None`]

## Review and merge order

- Delivery shape: [Single focused PR | Stacked PR | Parallel PR group]
- This PR's review position: [Standalone | PR 1 of N | PR N of N | Parallel member A/B]
- Base branch: [main or predecessor branch]
- Depends on: [PR/commit and exact behavior, or `None`]
- Review order: [Exact order]
- Merge order and conditions: [Exact sequence and required checks]
- Parallel group: [Group and independent members, or `None`]
- Human-verification focus: [One process, log, configuration, or port-safety behavior]

## Summary

- [Observable local process-manager outcome.]
- [Configuration, log, lifecycle, or port-safety behavior.]

## Scope

### Included

- [Behavior and affected Go package/path.]

### Excluded

- [Nearby behavior intentionally not changed.]

## Process and configuration contract

- Selected configuration: [How the selected `nala.yml` entry is read].
- Project fields: [`cwd`, `run`, optional `port` and `log` behavior].
- Ownership boundary: [Only configured projects and processes started by Nala Ctrl].
- Port conflict behavior: [Owner identification, confirmation, revalidation, and exact process targeted].
- Log behavior: [Default/override path, stdout/stderr capture, and UI event behavior].
- Failure behavior: [Visible error and recovery behavior].

## Verification

Record exact commands, immediate exit statuses, and meaningful results. Do not claim that a local fixture proves behavior beyond the paths it exercises.

| Command/action | Exit/result | Evidence |
| --- | ---: | --- |
| `go test ./...` | [status] | [Packages and assertions] |
| `[additional focused command, if run]` | [status] | [Result] |
| Native Wails flow | [Pass/fail/not run] | [Selected fixture, action, and visible outcome] |

When native verification is required, run `wails dev` from the repository root and record the actual desktop actions. Do not substitute a browser tab or a build result for native interaction. Record any platform prerequisite or unavailable UI boundary as unverified.

## Known limitations and pre-existing failures

- [Exact command, status, affected path, and reason; or `None known`].

## Reviewer focus

- [Highest-risk process ownership, port, log, or shutdown decision].
- [Files or state transition to inspect].

## Completion self-audit

- [ ] Every issue requirement maps to an observable outcome or explicit blocker.
- [ ] Configuration and path resolution match the selected `nala.yml`.
- [ ] Port takeover requires confirmation and a fresh process identity check.
- [ ] Stop and shutdown affect only processes started by Nala Ctrl.
- [ ] Logs show the configured output without adding unrelated process arguments or secrets.
- [ ] Exact commands and statuses are recorded; native checks are not inferred from builds.
- [ ] PR review/merge order and Linear handoff are explicit.
