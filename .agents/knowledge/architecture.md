# Nala Ctrl runtime architecture

Nala Ctrl is a local desktop process manager. The Go application and process manager are in the repository root. Wails v2 embeds the React, TypeScript, and Vite frontend from `frontend/` into the native app.

## Workspace and configuration

- The user adds project folders with the native folder dialog. Nala Ctrl reads only folders explicitly added during the app session; each folder's nala.yml defines exactly one project. The project ID is its normalized absolute folder path, so same-basename folders remain distinct.
- Configuration uses top-level cwd and run; port and log are optional. Relative working directories resolve from the project folder. Relative log paths resolve from the working directory.
- If nala.yml is absent, the UI gathers cwd, run, and optional port, derives the project name from the folder basename, and creates the file without overwriting an existing file. Existing projects and processes remain available while setup is shown.
- Unknown YAML fields and invalid port values are rejected. A legacy projects map is rejected with guidance to use top-level fields.
- All explicitly added projects appear in the UI. Selecting or adding another folder changes the monitored view and never stops already-running projects. There is no system-wide process discovery feature.

## Process lifecycle and port safety

- `Manager` starts each configured `run` command in its resolved `cwd`, captures stdout and stderr, and tracks each process by project folder ID.
- Closing the app stops all processes started by Nala Ctrl. Windows uses `cmd.exe` and terminates each managed process tree; Unix-like systems use a shell process group. Switching the selected project does not stop a process.
- When `port` is omitted, explicit `--port`, `-port`, `-p`, and `PORT=` values are recognized. Common defaults include Vite 5173, Next.js dev 3000, Python `http.server` 8000, and webpack serve 8080. Other commands need an explicit `port` for preflight checks.
- Port ownership is discovered from listening TCP sockets. The UI presents the owner and asks before termination. On confirmation, Go rechecks PID and process creation time, refuses unsafe or unverifiable owners, stops only that process, waits for the port to release, then starts the configured command.

## UI updates

- The frontend loads project details and the bounded log tail whenever a project is selected or reselected.
- Go streams complete log lines through the Wails `project:log` event. Each event includes the stable project ID, display name, and file offset so the UI can ignore lines already included in its initial log snapshot.
- Go sends `project:state` events with the stable project ID when a managed command starts or exits. The active view refreshes its running state after matching events; Run, Stop, and confirmed takeover also refresh details after the action completes.
- Project detail snapshots contain at most 250 recent lines and read at most the latest 512 KiB of the log file.

## Key files

| Path | Responsibility |
| --- | --- |
| `config.go` | One-project-per-file YAML schema, validation, stable folder identity, path resolution, port inference from common flags, `--addr host:port`, and known defaults, plus first config creation. |
| `manager.go` | Added-project registry, process lifecycle by folder identity, exact-owner confirmation, and log tail reads. |
| `ports.go`, `process_windows.go`, `process_unix.go` | Listening socket ownership and platform process control. |
| `log_writer.go` | Append output and emit line/state event types. |
| `app.go` | Wails-bound app methods and runtime event wiring. |
| `frontend/src/App.tsx` | Workspace, project status, run/stop, conflict confirmation, and log display. |

## Verification

Use Go tests for configuration, process lifecycle, output capture, port-owner safety, and takeover. Build the frontend from `frontend/` with `npm run build`. Package the native application with `wails build` on the target platform.
