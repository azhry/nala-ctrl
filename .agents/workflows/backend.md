# Go and Wails backend workflow

Apply this workflow to Go application services, Wails bindings, configuration, process control, port inspection, log capture, and runtime events.

## Contract and analysis

- Read the named Linear issue and agent comment, `AGENTS.md`, and `.agents/knowledge/` before changing behavior.
- Keep the app local-only. Nala Ctrl manages only projects in the selected `nala.yml`; it does not discover arbitrary host processes.
- Trace configuration from YAML parsing through path resolution and the Wails/UI model. Preserve the `projects` map contract: required `cwd` and `run`, optional `port` and `log`.
- Trace process ownership from command start through exit and app shutdown. Distinguish processes started by Nala Ctrl from external owners of a configured port.
- Treat exact process identity and user confirmation as a safety boundary. Recheck PID and creation time immediately before terminating a port owner; refuse uncertain or changed identities.
- Keep stdout/stderr capture, log-file append behavior, bounded log snapshots, and Wails event offsets/state transitions consistent with the knowledge docs.

## Implementation

- Keep parsing/validation in `config.go`, process lifecycle in `manager.go`, port inspection in `ports.go`, OS-specific control in `process_windows.go` and `process_unix.go`, log handling in `log_writer.go`, and Wails bindings in `app.go` unless an issue justifies changing those seams.
- Validate untrusted paths, project names, YAML fields, run commands, and port values. Never overwrite an existing `nala.yml` during first-run setup.
- Start the configured command in its resolved `cwd`; capture both output streams; stop only app-managed process trees on Stop or app shutdown.
- Avoid shell interpolation of user-controlled values where an argument API is available. Preserve platform-specific command behavior and report unsupported cases explicitly.
- Use Wails events for incremental log and process-state updates. Remove listeners and goroutines when their owner exits.
- Do not add remote API, authentication, database, provider, or deployment behavior to this local desktop app without an explicit issue requirement.

## Verification

- Add regression coverage at the narrowest reliable Go seam. Use real child processes when validating process-tree or port-owner behavior; do not fake Codex/LLM responses or core agent-client methods.
- Run `go test ./...` and `go build ./...` from the repository root. For a platform package change, run on that target OS or record the platform as unverified.
- Run `wails build` when changing bindings, app startup, or packaging configuration. Run the frontend build when a Go binding or event contract changes.
- Keep local-process fixtures separate from native UI acceptance. A fixture HTTP response proves the fixture only; it does not prove the Nala Ctrl UI flow.
- Record exact commands, exit statuses, and any environment limitation. Preserve generated artifacts and unrelated user files.

## Handoff

Stage only the intended Go/Wails implementation, tests, and relevant project guidance. Update the linked Linear issue and existing PR according to the delivery workflow. The human reviewer should focus on process ownership, config/path resolution, event lifecycle, and port-confirmation behavior.
