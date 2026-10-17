# Linear operations

Prefer the connected Linear app. Discover exact callable names from the active tool list; this host commonly exposes:

| Intent | Connector operation |
| --- | --- |
| Read issue and relations | `mcp__codex_apps__linear_get_issue({ id, includeRelations: true })` |
| Read comments | `mcp__codex_apps__linear_list_comments({ issueId })` |
| Search issues/projects | `mcp__codex_apps__linear_search({ query, type })` |
| List issues | `mcp__codex_apps__linear_list_issues({ team, project, state, assignee })` |
| Read team workflow states | `mcp__codex_apps__linear_list_issue_statuses({ team })` |
| Create/update issue | `mcp__codex_apps__linear_save_issue({ id, description, state, ... })` |
| Create/update comment | `mcp__codex_apps__linear_save_comment({ issueId, body })` |
| Read team/project | `mcp__codex_apps__linear_get_team({ query })`, `mcp__codex_apps__linear_get_project({ query })` |

The connector may require reauthentication. After an authorization error, stop connector attempts and use the documented API fallback. Never use a browser, browser automation, or screen capture to inspect an issue or its comments.

## API fallback

The helper is [`linear_tooling.py`](scripts/linear_tooling.py). It sends GraphQL requests to `https://api.linear.app/graphql`, reads `LINEAR_API_KEY` from the current process only, and does not log the key. Load only that allowlisted key with the non-printing loader described in `AGENTS.md`.

| Intent | Helper command |
| --- | --- |
| Read issue, project/state, and relations | `py .agents/skills/linear/scripts/linear_tooling.py read-issue --id TEAM-123` |
| Read issue comments | `py .agents/skills/linear/scripts/linear_tooling.py read-comments --issue-id TEAM-123` |
| Search issues | `py .agents/skills/linear/scripts/linear_tooling.py search --query "search terms"` |
| List/filter issues | `py .agents/skills/linear/scripts/linear_tooling.py list-issues --team-id TEAM_UUID --first 50` |
| List a team's valid workflow states | `py .agents/skills/linear/scripts/linear_tooling.py list-statuses --team-id TEAM_UUID` |
| Create an issue | `py .agents/skills/linear/scripts/linear_tooling.py save-issue --title "Title" --team-id TEAM_UUID` |
| Update an issue | `py .agents/skills/linear/scripts/linear_tooling.py save-issue --id ISSUE_UUID --description "Updated description"` |
| Create/update a comment | `py .agents/skills/linear/scripts/linear_tooling.py save-comment --issue-id ISSUE_UUID --body "Comment text"` |
| List projects and teams | `list-projects`, `list-teams` |
| List a team's cycles | `list-cycles --team-id TEAM_UUID` |
| Create/update a project or document | `save-project` or `save-document` with the exact options shown by `--help` |

Values such as `TEAM-123` and `TEAM_UUID` are examples; resolve real IDs from the issue or workspace. Never hard-code a team's key or a repository into this reusable tooling. Use `--help` before invoking a mutation.

Before adding or changing a fallback query, inspect the official Linear GraphQL schema or documentation. Do not discover field names by trial and error. Keep helper tests offline; they do not replace an actual tracker read or mutation.
