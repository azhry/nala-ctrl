# Nala Ctrl runtime architecture

Nala Ctrl is a local desktop process manager. The Go application and process manager are in the repository root. Wails v2 embeds the React, TypeScript, and Vite frontend from `frontend/` into the native app.

## Workspace and configuration

- The user chooses one project folder with the native folder dialog. Nala Ctrl reads only that folder's `nala.yml`.
- Configuration has a `projects` map. Each entry requires `cwd` and `run`; `port` and `log` are optional. Relative working directories resolve from the selected folder. Relative log paths resolve from the project's working directory.
- If `nala.yml` is absent, the UI gathers one initial project entry and creates the file without overwriting an existing file.
- Project names are limited to letters, numbers, underscores, and hyphens; unknown YAML fields and invalid port values are rejected.
- Only configured projects appear in the UI. There is no system-wide process discovery feature.

## Process lifecycle and port safety

- `Manager` starts each configured `run` command in its resolved `cwd`, captures stdout and stderr, and tracks the process it started.
- Closing the app stops processes started by Nala Ctrl. Windows uses `cmd.exe` and terminates the managed process tree; Unix-like systems use a shell process group.
- When `port` is omitted, explicit `--port`, `-port`, `-p`, and `PORT=` values are recognized. Common defaults include Vite 5173, Next.js dev 3000, Python `http.server` 8000, and webpack serve 8080. Other commands need an explicit `port` for preflight checks.
- Port ownership is discovered from listening TCP sockets. The UI presents the owner and asks before termination. On confirmation, Go rechecks PID and process creation time, refuses unsafe or unverifiable owners, stops only that process, waits for the port to release, then starts the configured command.

## UI updates

- The frontend loads project details once when a project is selected.
- Go streams complete log lines through the Wails `project:log` event. Each event includes the project name and file offset so the UI can ignore lines already included in its initial log snapshot.
- Go sends `project:state` events when a managed command starts or exits. The selected view refreshes its running state after those events; Run, Stop, and confirmed takeover also refresh details after the action completes.
- Project detail snapshots contain at most 250 recent lines and read at most the latest 512 KiB of the log file.

## Key files

| Path | Responsibility |
| --- | --- |
| `config.go` | YAML schema, validation, path resolution, port inference from common flags, `--addr host:port`, and known defaults, plus first config creation. |
| `manager.go` | Project selection, process lifecycle, exact-owner confirmation, and log tail reads. |
| `ports.go`, `process_windows.go`, `process_unix.go` | Listening socket ownership and platform process control. |
| `log_writer.go` | Append output and emit line/state event types. |
| `app.go` | Wails-bound app methods and runtime event wiring. |
| `frontend/src/App.tsx` | Workspace, project status, run/stop, conflict confirmation, and log display. |

## Verification

Use Go tests for configuration, process lifecycle, output capture, port-owner safety, and takeover. Build the frontend from `frontend/` with `npm run build`. Package the native application with `wails build` on the target platform.
