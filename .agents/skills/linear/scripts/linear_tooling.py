#!/usr/bin/env python3
"""Linear GraphQL fallback for documented issue, project, and document operations."""

from __future__ import annotations

import argparse
import json
import os
import sys
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

LINEAR_API_URL = "https://api.linear.app/graphql"
SCRIPT_NAME = "linear_tooling.py"

ISSUE_FIELDS = """
  id identifier title description state { id name type } priority
  assignee { id name } project { id name status { id name } } cycle { id name startsAt endsAt }
  labels { nodes { id name } }
  relations(first: 100) { nodes { id type relatedIssue { id identifier title url } } }
  url createdAt updatedAt
"""


def get_api_key() -> str:
    key = os.environ.get("LINEAR_API_KEY", "")
    if not key:
        print("Error: LINEAR_API_KEY environment variable is not set.", file=sys.stderr)
        raise SystemExit(1)
    return key


def graphql_request(query: str, variables: dict[str, Any] | None = None) -> dict[str, Any]:
    payload: dict[str, Any] = {"query": query}
    if variables is not None:
        payload["variables"] = variables
    request = Request(
        LINEAR_API_URL,
        data=json.dumps(payload).encode("utf-8"),
        headers={
            "Content-Type": "application/json",
            "Authorization": get_api_key(),
        },
        method="POST",
    )
    try:
        with urlopen(request, timeout=30) as response:
            body = response.read().decode("utf-8")
            return json.loads(body)
    except HTTPError as error:
        body = error.read().decode("utf-8") if error.fp else ""
        print(f"HTTP error {error.code}: {body}", file=sys.stderr)
        raise SystemExit(1) from None
    except URLError as error:
        print(f"Connection error: {error.reason}", file=sys.stderr)
        raise SystemExit(1) from None


def extract_data(result: dict[str, Any]) -> Any:
    errors = result.get("errors") or []
    if errors:
        for error in errors:
            print(f"GraphQL error: {error}", file=sys.stderr)
        raise SystemExit(1)
    return result.get("data")


def print_result(result: dict[str, Any]) -> None:
    print(json.dumps(extract_data(result), indent=2))


def issue_selection() -> str:
    return """id identifier title description state { id name type } priority
      assignee { id name } project { id name status { id name } } url"""


def cmd_read_issue(args: argparse.Namespace) -> None:
    query = f"query ReadIssue($id: String!) {{ issue(id: $id) {{ {ISSUE_FIELDS} }} }}"
    print_result(graphql_request(query, {"id": args.id}))


def cmd_read_comments(args: argparse.Namespace) -> None:
    query = """
    query ReadComments($id: String!) {
      issue(id: $id) {
        id identifier title
        comments(first: 100) {
          nodes { id body url createdAt updatedAt user { id name } }
          pageInfo { endCursor hasNextPage }
        }
      }
    }
    """
    print_result(graphql_request(query, {"id": args.issue_id}))


def cmd_search(args: argparse.Namespace) -> None:
    query = f"""
    query SearchIssues($filter: IssueFilter!, $first: Int!) {{
      issues(filter: $filter, first: $first) {{
        nodes {{ {issue_selection()} }}
      }}
    }}
    """
    variables = {
        "filter": {"searchableContent": {"contains": args.query}},
        "first": args.first,
    }
    print_result(graphql_request(query, variables))


def cmd_list_issues(args: argparse.Namespace) -> None:
    filters: dict[str, Any] = {}
    for key, value in (
        ("team", args.team_id),
        ("project", args.project_id),
        ("state", args.state_id),
        ("assignee", args.assignee_id),
    ):
        if value:
            filters[key] = {"id": {"eq": value}}
    query = f"""
    query ListIssues($filter: IssueFilter!, $first: Int!, $after: String) {{
      issues(filter: $filter, first: $first, after: $after) {{
        nodes {{ {issue_selection()} }}
        pageInfo {{ endCursor hasNextPage }}
      }}
    }}
    """
    variables: dict[str, Any] = {"filter": filters, "first": args.first}
    if args.after:
        variables["after"] = args.after
    print_result(graphql_request(query, variables))


def cmd_list_statuses(args: argparse.Namespace) -> None:
    query = """
    query ListTeamStates($teamId: String!) {
      team(id: $teamId) {
        id key name
        states(first: 100) { nodes { id name type } }
      }
    }
    """
    print_result(graphql_request(query, {"teamId": args.team_id}))


def issue_input(args: argparse.Namespace, *, creating: bool) -> dict[str, Any]:
    values = {
        "title": args.title,
        "description": args.description,
        "teamId": args.team_id,
        "projectId": args.project_id,
        "stateId": args.state_id,
        "priority": args.priority,
        "assigneeId": args.assignee_id,
        "labelIds": [value.strip() for value in (args.labels or "").split(",") if value.strip()]
        if args.labels is not None
        else None,
    }
    if creating and args.team_id is None:
        raise SystemExit("Error: --team-id is required when creating an issue.")
    return {key: value for key, value in values.items() if value is not None}


def cmd_save_issue(args: argparse.Namespace) -> None:
    creating = args.id is None
    if creating and args.title is None:
        raise SystemExit("Error: --title is required when creating an issue.")
    input_data = issue_input(args, creating=creating)
    if creating:
        query = f"""
        mutation CreateIssue($input: IssueCreateInput!) {{
          issueCreate(input: $input) {{ success issue {{ {ISSUE_FIELDS} }} }}
        }}
        """
    else:
        query = f"""
        mutation UpdateIssue($id: String!, $input: IssueUpdateInput!) {{
          issueUpdate(id: $id, input: $input) {{ success issue {{ {ISSUE_FIELDS} }} }}
        }}
        """
    variables: dict[str, Any] = {"input": input_data}
    if not creating:
        variables["id"] = args.id
    print_result(graphql_request(query, variables))


def cmd_save_comment(args: argparse.Namespace) -> None:
    if args.id:
        query = """
        mutation UpdateComment($id: String!, $input: CommentUpdateInput!) {
          commentUpdate(id: $id, input: $input) {
            success comment { id body url issue { id identifier title } }
          }
        }
        """
        variables = {"id": args.id, "input": {"body": args.body}}
    else:
        query = """
        mutation CreateComment($input: CommentCreateInput!) {
          commentCreate(input: $input) {
            success comment { id body url issue { id identifier title } }
          }
        }
        """
        variables = {"input": {"issueId": args.issue_id, "body": args.body}}
    print_result(graphql_request(query, variables))


def cmd_list_projects(_: argparse.Namespace) -> None:
    query = """
    query ListProjects {
      projects {
        nodes { id name icon status { id name } startDate targetDate }
      }
    }
    """
    print_result(graphql_request(query))


def cmd_list_teams(_: argparse.Namespace) -> None:
    query = "query ListTeams { teams { nodes { id name key } } }"
    print_result(graphql_request(query))


def cmd_list_cycles(args: argparse.Namespace) -> None:
    query = """
    query ListCycles($teamId: String!) {
      cycles(filter: { team: { id: { eq: $teamId } } }, first: 100) {
        nodes { id name startsAt endsAt }
      }
    }
    """
    print_result(graphql_request(query, {"teamId": args.team_id}))


def project_input(args: argparse.Namespace) -> dict[str, Any]:
    values: dict[str, Any] = {
        "name": args.name,
        "description": args.description,
        "icon": args.icon,
        "statusId": args.status_id,
        "startDate": args.start_date,
        "targetDate": args.target_date,
    }
    if args.team_id:
        values["teamIds"] = [args.team_id]
    return {key: value for key, value in values.items() if value is not None}


def cmd_save_project(args: argparse.Namespace) -> None:
    creating = args.id is None
    if creating and args.name is None:
        raise SystemExit("Error: --name is required when creating a project.")
    if creating and args.team_id is None:
        raise SystemExit("Error: --team-id is required when creating a project.")
    input_data = project_input(args)
    if creating:
        query = """
        mutation CreateProject($input: ProjectCreateInput!) {
          projectCreate(input: $input) {
            success project { id name icon status { id name } startDate targetDate }
          }
        }
        """
    else:
        query = """
        mutation UpdateProject($id: String!, $input: ProjectUpdateInput!) {
          projectUpdate(id: $id, input: $input) {
            success project { id name icon status { id name } startDate targetDate }
          }
        }
        """
    variables: dict[str, Any] = {"input": input_data}
    if not creating:
        variables["id"] = args.id
    print_result(graphql_request(query, variables))


def cmd_save_document(args: argparse.Namespace) -> None:
    creating = args.id is None
    if creating and args.title is None:
        raise SystemExit("Error: --title is required when creating a document.")
    values = {
        "title": args.title,
        "content": args.content,
        "projectId": args.project_id,
        "parentDocumentId": args.parent_document_id,
    }
    input_data = {key: value for key, value in values.items() if value is not None}
    if creating:
        query = """
        mutation CreateDocument($input: DocumentCreateInput!) {
          documentCreate(input: $input) { success document { id title url } }
        }
        """
    else:
        query = """
        mutation UpdateDocument($id: String!, $input: DocumentUpdateInput!) {
          documentUpdate(id: $id, input: $input) { success document { id title url } }
        }
        """
    variables: dict[str, Any] = {"input": input_data}
    if not creating:
        variables["id"] = args.id
    print_result(graphql_request(query, variables))


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog=SCRIPT_NAME,
        description="Linear GraphQL fallback for documented issue, project, and document operations.",
    )
    subparsers = parser.add_subparsers(dest="command")

    read = subparsers.add_parser("read-issue", help="Read an issue, relations, and project/state details")
    read.add_argument("--id", required=True, help="Issue identifier or UUID")
    read.set_defaults(func=cmd_read_issue)

    comments = subparsers.add_parser("read-comments", help="Read comments on an issue")
    comments.add_argument("--issue-id", required=True, help="Issue identifier or UUID")
    comments.set_defaults(func=cmd_read_comments)

    search = subparsers.add_parser("search", help="Search issues by text")
    search.add_argument("--query", required=True, help="Search text")
    search.add_argument("--first", type=int, default=50, help="Maximum results (default: 50)")
    search.set_defaults(func=cmd_search)

    listing = subparsers.add_parser("list-issues", help="List issues with optional filters")
    listing.add_argument("--team-id", help="Team UUID")
    listing.add_argument("--project-id", help="Project UUID")
    listing.add_argument("--state-id", help="Workflow state UUID")
    listing.add_argument("--assignee-id", help="Assignee UUID")
    listing.add_argument("--first", type=int, default=50, help="Maximum results (default: 50)")
    listing.add_argument("--after", help="Pagination cursor")
    listing.set_defaults(func=cmd_list_issues)

    statuses = subparsers.add_parser("list-statuses", help="List valid workflow states for a team")
    statuses.add_argument("--team-id", required=True, help="Team UUID")
    statuses.set_defaults(func=cmd_list_statuses)

    save_issue = subparsers.add_parser("save-issue", help="Create or update a Linear issue")
    save_issue.add_argument("--id", help="Issue UUID; omit to create")
    save_issue.add_argument("--title", help="Issue title")
    save_issue.add_argument("--description", help="Issue description")
    save_issue.add_argument("--team-id", help="Team UUID (required when creating)")
    save_issue.add_argument("--project-id", help="Project UUID")
    save_issue.add_argument("--state-id", help="Workflow state UUID")
    save_issue.add_argument("--priority", type=int, help="Priority 0–4")
    save_issue.add_argument("--assignee-id", help="Assignee UUID")
    save_issue.add_argument("--labels", help="Comma-separated label UUIDs")
    save_issue.set_defaults(func=cmd_save_issue)

    comment = subparsers.add_parser("save-comment", help="Create or update an issue comment")
    comment.add_argument("--id", help="Comment UUID; omit to create")
    comment.add_argument("--issue-id", help="Issue UUID (required when creating)")
    comment.add_argument("--body", required=True, help="Comment body")
    comment.set_defaults(func=cmd_save_comment)

    projects = subparsers.add_parser("list-projects", help="List Linear projects")
    projects.set_defaults(func=cmd_list_projects)

    teams = subparsers.add_parser("list-teams", help="List Linear teams")
    teams.set_defaults(func=cmd_list_teams)

    cycles = subparsers.add_parser("list-cycles", help="List cycles for a team")
    cycles.add_argument("--team-id", required=True, help="Team UUID")
    cycles.set_defaults(func=cmd_list_cycles)

    save_project = subparsers.add_parser("save-project", help="Create or update a Linear project")
    save_project.add_argument("--id", help="Project UUID; omit to create")
    save_project.add_argument("--name", help="Project name (required when creating)")
    save_project.add_argument("--team-id", help="Team UUID (required when creating)")
    save_project.add_argument("--status-id", help="Project status UUID")
    save_project.add_argument("--description", help="Project description")
    save_project.add_argument("--icon", help="Project icon")
    save_project.add_argument("--start-date", help="Start date (ISO 8601)")
    save_project.add_argument("--target-date", help="Target date (ISO 8601)")
    save_project.set_defaults(func=cmd_save_project)

    save_document = subparsers.add_parser("save-document", help="Create or update a Linear document")
    save_document.add_argument("--id", help="Document UUID; omit to create")
    save_document.add_argument("--title", help="Document title (required when creating)")
    save_document.add_argument("--project-id", help="Project UUID")
    save_document.add_argument("--content", help="Document content")
    save_document.add_argument("--parent-document-id", help="Parent document UUID")
    save_document.set_defaults(func=cmd_save_document)

    return parser


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()
    if not args.command:
        parser.print_help()
        return 0
    if args.command == "save-comment" and not args.id and not args.issue_id:
        parser.error("--issue-id is required when creating a comment")
    args.func(args)
    return 0


if __name__ == "__main__":
    sys.exit(main())
