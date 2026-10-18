from __future__ import annotations

import importlib.util
import io
import json
import os
import sys
import unittest
from pathlib import Path
from unittest.mock import patch
from urllib.error import HTTPError, URLError


MODULE_PATH = Path(__file__).parents[1] / "scripts" / "linear_tooling.py"
SPEC = importlib.util.spec_from_file_location("linear_tooling", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
linear_tooling = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = linear_tooling
SPEC.loader.exec_module(linear_tooling)


class Response:
    def __init__(self, body: bytes) -> None:
        self.body = body

    def __enter__(self) -> "Response":
        return self

    def __exit__(self, *_args: object) -> None:
        return None

    def read(self) -> bytes:
        return self.body


class LinearRequestTests(unittest.TestCase):
    @patch.dict(os.environ, {"LINEAR_API_KEY": "test-key"}, clear=True)
    def test_graphql_request_posts_query_variables_and_authorization(self) -> None:
        captured: dict[str, object] = {}

        def fake_urlopen(request, timeout):
            captured["request"] = request
            captured["timeout"] = timeout
            return Response(b'{"data":{"issue":{"identifier":"TEAM-123"}}}')

        variables = {"id": "issue-id"}
        with patch.object(linear_tooling, "urlopen", side_effect=fake_urlopen):
            result = linear_tooling.graphql_request("query ReadIssue($id: String!) { issue(id: $id) { identifier } }", variables)

        request = captured["request"]
        self.assertEqual(request.full_url, "https://api.linear.app/graphql")
        self.assertEqual(request.get_method(), "POST")
        self.assertEqual(request.get_header("Content-type"), "application/json")
        self.assertEqual(request.get_header("Authorization"), "test-key")
        self.assertEqual(json.loads(request.data), {
            "query": "query ReadIssue($id: String!) { issue(id: $id) { identifier } }",
            "variables": variables,
        })
        self.assertEqual(captured["timeout"], 30)
        self.assertEqual(linear_tooling.extract_data(result), {"issue": {"identifier": "TEAM-123"}})

    @patch.dict(os.environ, {"LINEAR_API_KEY": "test-key"}, clear=True)
    def test_graphql_request_omits_variables_when_not_supplied(self) -> None:
        with patch.object(linear_tooling, "urlopen", return_value=Response(b'{"data":{"teams":{"nodes":[]}}}')) as mocked:
            result = linear_tooling.graphql_request("query { teams { nodes { id } } }")

        request = mocked.call_args.args[0]
        self.assertEqual(json.loads(request.data), {"query": "query { teams { nodes { id } } }"})
        self.assertEqual(linear_tooling.extract_data(result), {"teams": {"nodes": []}})

    @patch.dict(os.environ, {"LINEAR_API_KEY": "test-key"}, clear=True)
    def test_http_error_exits_nonzero_without_printing_authorization(self) -> None:
        error = HTTPError(
            "https://api.linear.app/graphql",
            401,
            "Unauthorized",
            {},
            io.BytesIO(b'{"errors":[{"message":"Unauthorized"}]}'),
        )
        stderr = io.StringIO()
        with patch.object(linear_tooling, "urlopen", side_effect=error), patch("sys.stderr", stderr):
            with self.assertRaisesRegex(SystemExit, "1"):
                linear_tooling.graphql_request("query { viewer { id } }")

        self.assertIn("HTTP error 401", stderr.getvalue())
        self.assertNotIn("test-key", stderr.getvalue())

    @patch.dict(os.environ, {"LINEAR_API_KEY": "test-key"}, clear=True)
    def test_network_error_exits_nonzero(self) -> None:
        stderr = io.StringIO()
        with patch.object(linear_tooling, "urlopen", side_effect=URLError("offline")), patch("sys.stderr", stderr):
            with self.assertRaisesRegex(SystemExit, "1"):
                linear_tooling.graphql_request("query { viewer { id } }")

        self.assertIn("Connection error: offline", stderr.getvalue())

    def test_missing_key_exits_without_network_request(self) -> None:
        stderr = io.StringIO()
        with patch.dict(os.environ, {}, clear=True), patch("sys.stderr", stderr):
            with patch.object(linear_tooling, "urlopen") as urlopen:
                with self.assertRaisesRegex(SystemExit, "1"):
                    linear_tooling.graphql_request("query { viewer { id } }")

        urlopen.assert_not_called()
        self.assertIn("LINEAR_API_KEY", stderr.getvalue())

    def test_graphql_error_response_exits_nonzero(self) -> None:
        stderr = io.StringIO()
        with patch("sys.stderr", stderr):
            with self.assertRaisesRegex(SystemExit, "1"):
                linear_tooling.extract_data({"errors": [{"message": "Invalid query"}]})
        self.assertIn("GraphQL error", stderr.getvalue())


class LinearCommandTests(unittest.TestCase):
    def test_update_issue_uses_current_update_input(self) -> None:
        args = linear_tooling.build_parser().parse_args([
            "save-issue", "--id", "issue-id", "--description", "Updated description"
        ])
        with patch.object(linear_tooling, "graphql_request", return_value={"data": {}}) as request, patch("builtins.print"):
            linear_tooling.cmd_save_issue(args)

        query, variables = request.call_args.args
        self.assertIn("IssueUpdateInput!", query)
        self.assertIn("issueUpdate", query)
        self.assertEqual(variables, {"id": "issue-id", "input": {"description": "Updated description"}})

    def test_create_issue_requires_team_id(self) -> None:
        args = linear_tooling.build_parser().parse_args(["save-issue", "--title", "A title"])
        with self.assertRaisesRegex(SystemExit, "--team-id is required"):
            linear_tooling.cmd_save_issue(args)

    def test_create_project_requires_team_id(self) -> None:
        args = linear_tooling.build_parser().parse_args(["save-project", "--name", "A project"])
        with self.assertRaisesRegex(SystemExit, "--team-id is required"):
            linear_tooling.cmd_save_project(args)

    def test_create_project_maps_team_and_status_ids(self) -> None:
        args = linear_tooling.build_parser().parse_args([
            "save-project", "--name", "A project", "--team-id", "team-id", "--status-id", "status-id"
        ])
        with patch.object(linear_tooling, "graphql_request", return_value={"data": {}}) as request, patch("builtins.print"):
            linear_tooling.cmd_save_project(args)

        query, variables = request.call_args.args
        self.assertIn("ProjectCreateInput!", query)
        self.assertIn("status { id name }", query)
        self.assertEqual(variables, {
            "input": {"name": "A project", "teamIds": ["team-id"], "statusId": "status-id"}
        })

    def test_list_projects_selects_project_status_fields(self) -> None:
        with patch.object(linear_tooling, "graphql_request", return_value={"data": {}}) as request, patch("builtins.print"):
            linear_tooling.cmd_list_projects(None)

        query = request.call_args.args[0]
        self.assertIn("status { id name }", query)

    def test_list_issues_builds_issue_filter(self) -> None:
        args = linear_tooling.build_parser().parse_args([
            "list-issues", "--team-id", "team-id", "--state-id", "state-id", "--first", "10"
        ])
        with patch.object(linear_tooling, "graphql_request", return_value={"data": {}}) as request, patch("builtins.print"):
            linear_tooling.cmd_list_issues(args)

        _, variables = request.call_args.args
        self.assertEqual(variables, {
            "filter": {
                "team": {"id": {"eq": "team-id"}},
                "state": {"id": {"eq": "state-id"}},
            },
            "first": 10,
        })


if __name__ == "__main__":
    unittest.main()
