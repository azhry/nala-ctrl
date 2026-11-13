# Nala Ctrl runtime architecture

Nala Ctrl is a local desktop process manager. The Go application and process manager are in the repository root. Wails v2 embeds the React, TypeScript, and Vite frontend from `frontend/` into the native app.

## Workspace and configuration

- The user adds project folders with the native folder dialog. Nala Ctrl remembers explicitly added folders in the user's application config directory and restores configured projects at startup; each folder's `nala.yml` defines one project group. Its `projects` map contains named services. The group ID is its normalized absolute folder path, so same-basename folders remain distinct; service IDs are stable per folder and service key. Restoring a project never starts its services.
- Each service config requires `cwd` and `run`; `port` and `log` are optional. Relative working directories resolve from the project folder. Relative log paths resolve from the service working directory. Each service has an independent process state, port check, and log stream.
- If `nala.yml` is absent, the UI gathers `cwd`, `run`, and optional `port`, derives an initial service key and project name from the folder basename, and creates a `projects` map without overwriting an existing file. Existing projects and processes remain available while setup is shown.
- While the desktop app is open, it rechecks loaded projects' `nala.yml` files every two seconds. A valid edit refreshes the workspace and emits `workspace:changed`, so added services appear without restarting other services. A service that is starting or running keeps the configuration and log path it started with until it stops; a removed running service stays listed so it can still be stopped, then disappears on a later refresh. An invalid or missing config leaves the last valid workspace view in place and is retried on the next refresh.
- Unknown YAML fields outside the recognized `deployment` extension, missing service fields, and invalid port values are rejected. A config with no services is rejected.
- Each service may include a mapping-valued `deployment` extension owned by Nala SVC. Nala Ctrl accepts it as an opaque YAML node; it does not validate nested fields or use them for local service behavior.
- All explicitly added project groups and their services appear in the UI. Selecting or adding another folder or service changes the monitored view and never stops already-running processes. There is no system-wide process discovery feature.

## Process lifecycle and port safety

- `Manager` starts each configured service `run` command in its resolved `cwd`, captures stdout and stderr, and tracks each process by stable service ID.
- Closing the app stops all processes started by Nala Ctrl. Windows uses `cmd.exe` and terminates each managed process tree; Unix-like systems use a shell process group. Switching the selected project does not stop a process.
- When a service's `port` is omitted, explicit `--port`, `-port`, `-p`, and `PORT=` values are recognized. Common defaults include Vite 5173, Next.js dev 3000, Python `http.server` 8000, and webpack serve 8080. Other commands need an explicit `port` for preflight checks.
- Port ownership is discovered only for a loaded service's configured port. The UI presents the owner and asks before termination. On confirmation, Go rechecks PID and process creation time, refuses unsafe or unverifiable owners, stops only that process, waits for the port to release, then starts the configured command. The loopback API treats `POST /run` as an explicit takeover request and performs the same identity check before replacing an occupied port owner and starting the service. `POST /stop` stops the manager-owned service, or the safely identified owner of that service's configured port when the manager does not own a running child.

## UI updates

- The frontend loads service details and the bounded log tail whenever a service is selected or reselected.
- Go streams complete log lines through the Wails `project:log` event. Each event includes project and service identities plus the file offset, so the UI can scope the stream and ignore lines already included in its initial log snapshot.
- Go sends `project:state` events with project and service identities when a managed command starts or exits. The active view refreshes its running state after matching events; Run, Stop, and confirmed takeover also refresh details after the action completes.
- Project detail snapshots contain at most 250 recent lines and read at most the latest 512 KiB of the log file.
- The desktop app's Fix Docker action controls only the local Docker Desktop installation. Before restarting, it checks the current boot logs for the known secrets-engine `engine.sock` rename failure. If that exact error is present and the corresponding zero-length Windows reparse point still exists, it checks whether either local Desktop context responds to `docker info`. Only while both are unavailable does it use WSL to move the socket to a unique `.codex-stale-*` backup path without deleting or replacing it. It then runs `docker desktop restart`, falls back to `docker desktop start --detach` if restart fails, and checks the `desktop-linux` and `desktop-windows` contexts until one responds to `docker info` or the bounded recovery timeout expires. The targeted repair requires WSL. The action does not reset Docker data, shut down WSL globally, or expose a Docker operation through the headless API. Restarting Docker Desktop interrupts running containers.

## Local agent API

- The Wails desktop app starts the JSON HTTP API at `127.0.0.1:47654` during startup, using the same `ProjectManager.manager` as the UI. Health reports `{"ok":true,"mode":"desktop"}`. The API closes before Wails stops the manager's services.
- The in-app headless API at `127.0.0.1:47653` also uses the desktop `Manager` and reports `mode=desktop`. Run, stop, project-load, state, and log behavior through either endpoint is therefore reflected in the desktop API and UI. Stopping the headless API listener does not stop services managed by the desktop app.
- `nala-ctrl api serve --listen 127.0.0.1:47653` starts a standalone API from a console build or `go run .`. While the desktop API is available, the standalone listener forwards requests to `127.0.0.1:47654` and health reports `mode=desktop`; otherwise it uses its own `Manager` and reports `mode=headless`. Client agents performing desktop service operations must require `mode=desktop`; they must not start the headless command as a fallback.
- `make dev` runs `wails dev` with the headless API enabled on `127.0.0.1:47653`, sharing the same `Manager` as the UI. The desktop UI can start and stop that API listener. Running `wails dev` directly does not auto-start the headless API.
- The desktop and headless APIs use separate loopback ports and can run at the same time. If another process already owns either port, report the bind conflict and do not terminate it.
- The API listener accepts only a loopback IP. Its middleware rejects browser `Origin` requests and non-loopback `Host` values, and sends no CORS headers.
- `GET /v1/workspace` refreshes loaded project configs before returning dedicated API DTOs that omit `run`, environment values, and log lines. In desktop mode, changed configs emit `workspace:changed` for the UI. The workspace response exposes only a `hasWarnings` boolean, not diagnostic text that could contain private configuration. `GET /v1/services/{serviceID}/logs?limit=N` explicitly reads a bounded tail (100 by default, at most 250 lines, backed by the existing 512 KiB read cap). Logs may contain secrets.
- `POST /v1/projects/load` explicitly adds a folder through the existing registry. In desktop mode, the app emits a workspace-change event so the UI reloads and selects the added project. Run and stop routes resolve only loaded service IDs and use the manager serving this API. They may control the current owner of that service's configured port after rechecking PID and process creation time; there is no API route for arbitrary PID operations or system-wide process discovery.
- In desktop mode, `running` covers child processes managed by the same `Manager` as the UI, including operations sent to the in-app headless API or forwarded from the standalone listener. In standalone headless mode, it covers child processes started by that headless process. `portInUse` reports whether any process occupies the configured port. API shutdown stops only processes owned by its manager.
- `GET /v1/projects/git-branch?projectId=<url-encoded-absolute-folder>` reports the current branch for the registered project folder's Git root. It does not report a process-start branch and cannot switch branches.
- The API returns generic operation errors and never includes a configured command in responses.

## Key files

| Path | Responsibility |
| --- | --- |
| `config.go` | One-project-per-file `projects` service map schema, validation, stable folder/service identities, path resolution, port inference from common flags, `--addr host:port`, and known defaults, plus first config creation. |
| `manager.go` | Added-project and service registry, process lifecycle by service identity, exact-owner confirmation, and log tail reads. |
| `docker_recovery.go`, `docker_recovery_windows.go` | Local Docker Desktop socket repair, restart/start recovery, and bounded local-engine readiness checks for the desktop app. |
| `ports.go`, `process_windows.go`, `process_unix.go` | Listening socket ownership and platform process control. |
| `log_writer.go` | Append output and emit line/state event types. |
| `app.go` | Wails-bound app methods and runtime event wiring. |
| `agent_api.go` | Loopback-only HTTP routes, safe agent DTOs, and headless CLI dispatch. |
| `frontend/src/App.tsx` | Workspace, project status, run/stop, conflict confirmation, and log display. |

## Verification

Use Go tests for configuration, process lifecycle, output capture, port-owner safety, and takeover. Build the frontend from `frontend/` with `npm run build`. Package the native application with `wails build` on the target platform.
