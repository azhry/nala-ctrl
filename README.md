# Nala Ctrl

Nala Ctrl is a desktop app for running and monitoring configured projects and their logs. It starts each configured run command, captures output, and shows recent log lines. When a configured port is already in use, the app identifies its owner and asks before stopping it.

## Project configuration

Add project folders that contain `nala.yml`. Each file represents one project, named from its folder basename, and can declare multiple independently managed services. You can add multiple folders and switch between their services while processes keep running. If a file is missing, Nala Ctrl can create a config with an initial service using `cwd` and `run`; `port` is optional and can be inferred from the run command.

```yaml
projects:
  backend:
    cwd: backend
    run: go run . --port 8080
    port: 8080
    log: logs/backend.log
  frontend:
    cwd: frontend
    run: npm run dev -- --port 5173
    port: 5173
    log: logs/frontend.log
```

- Each entry under `projects` is a service and requires `cwd` and `run`. Relative working directories resolve from the selected folder.
- `port` is optional per service. Nala Ctrl detects common explicit port flags, Go `--addr host:port` listener addresses, and known defaults for Vite, Next.js, Python's HTTP server, and webpack. Set it explicitly when your command uses a different port. Without a detected port, the app can still run and capture logs, but cannot check for a port conflict first.
- `log` is optional per service and sets its log file path. By default, process output is appended to `logs/<service-name>.log` under that service's `cwd`.

Each folder you add contributes one project group to the list. Its services have separate status, Run/Stop controls, and log streams. Switching between projects or services does not stop running commands. Closing the app stops every process it started.

## Local agent API

The desktop app starts a local JSON API at `127.0.0.1:47654` during Wails startup. It uses the same manager as the desktop UI, so its workspace, service state, run, stop, logs, and branch routes refer to that app instance. The desktop API reports `mode=desktop` from `GET /v1/health` and does not require a token or authentication header.

The console binary can also run a separate headless API without opening the Wails desktop UI. From a source checkout, build the embedded frontend assets and then the console binary:

```bash
npm --prefix frontend ci
npm --prefix frontend run build
go build -o nala-ctrl .
```

Start this mode only when a headless manager is explicitly needed:

```bash
./nala-ctrl api serve --listen 127.0.0.1:47653
```

On Windows, use `nala-ctrl.exe` (or `./nala-ctrl.exe` in Git Bash). This command reports `mode=headless` at `127.0.0.1:47653` and owns a separate manager from the desktop app. The desktop app uses `127.0.0.1:47654`, so both modes can run at the same time. Do not use the headless command as a fallback for desktop service operations.

```bash
curl --silent --show-error --fail http://127.0.0.1:47653/v1/health
```

Both APIs accept requests only from loopback clients and reject browser-origin requests. Health returns `{"ok":true,"mode":"desktop"}` from the desktop API at `127.0.0.1:47654` or `{"ok":true,"mode":"headless"}` from the standalone API at `127.0.0.1:47653`. They restore explicitly registered `nala.yml` folders from the user's Nala Ctrl config directory. `POST /v1/projects/load` can register a folder; when using desktop mode, add projects through the UI so its project list refreshes.

| Request | Purpose |
| --- | --- |
| `GET /v1/health` | Confirm that the API is responding and identify its mode. |
| `GET /v1/workspace` | List projects and configured services in the manager serving this API. |
| `POST /v1/projects/load` with `{"folder":"/path/to/project"}` | Register a project folder containing `nala.yml`. |
| `GET /v1/services/{serviceID}` | Read service configuration and manager-owned run state. |
| `GET /v1/services/{serviceID}/logs?limit=N` | Read the latest log lines; the default is 100 and the maximum is 250. |
| `POST /v1/services/{serviceID}/run` | Start that configured service through the manager serving this API. |
| `POST /v1/services/{serviceID}/stop` | Stop that service only when the manager serving this API started it. |
| `GET /v1/projects/git-branch?projectId=<url-encoded-path>` | Read the registered project root's current branch and local branch names. |

Workspace and service responses omit configured `run` commands and environment values. The workspace response's `hasWarnings` flag signals that saved project folders could not all be read or persisted; it omits diagnostic details that might contain private configuration. A service's `running` value reflects processes started by the manager serving this API; in desktop mode, that is the same manager used by the UI. `portInUse` reports whether a configured port is occupied but does not identify or control its owner. The branch route describes the current branch of the registered project root and does not identify the branch or worktree used when a process started. Logs may contain secrets, so request them only for diagnosis and redact sensitive values before reporting. The API has no route to switch branches or terminate a port owner. Stopping the standalone API with Ctrl-C stops processes that its manager started.

## Development

Requirements: Go 1.26 or newer, Node.js 20.19+ or 22.12+, npm, GNU Make, and the platform dependencies required by Wails v2. See the [Wails installation guide](https://wails.io/docs/gettingstarted/installation) for the native desktop dependencies for your operating system.

Install the Wails CLI, add its Go install directory to this shell's `PATH`, and install the frontend dependencies:

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0
go_bin_dir="$(go env GOBIN)"
if [ -z "$go_bin_dir" ]; then
  go_bin_dir="$(go env GOPATH)/bin"
fi
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) go_bin_dir="$(cygpath -u "$go_bin_dir")" ;;
esac
export PATH="$go_bin_dir:$PATH"
hash -r
wails version

cd frontend
npm install
cd ..
```

The Wails CLI is installed under `GOBIN`, or under `GOPATH/bin` when `GOBIN` is empty. The `PATH` change above applies to the current terminal; repeat it in a new terminal if `wails` is not found. On Windows Git Bash, `cygpath` converts Go's Windows path to a Git Bash path.

Start the desktop app and the headless API from this checkout with:

```bash
make dev
```

This target runs `wails dev` and sets the development flag that starts the headless API on `127.0.0.1:47653` inside that same app process. Health reports `mode=headless`; the API owns a separate Manager from the desktop API at `127.0.0.1:47654`. Use the app's headless API control to stop or restart it. Stopping the API stops only services its Manager started. If the port is already occupied, the app reports the bind error and leaves that owner alone. Running `wails dev` directly starts the desktop API but does not auto-start the headless API. The standalone `nala-ctrl api serve` command remains available when only a headless API is needed.

The desktop app includes a one-click **Fix Docker** action for local Docker Desktop recovery. Before restarting Docker Desktop, it checks the current boot logs for the known secrets-engine socket rename failure. If that exact error is present and the matching zero-length Windows reparse point still exists, it confirms no local Desktop engine is ready, then moves the socket aside through WSL and preserves it under a timestamped backup name. It then uses the supported Docker Desktop restart/start commands and waits for a local engine to respond. Restarting interrupts running containers. The known socket repair requires WSL; the action does not reset images, containers, or volumes.

Build a native application package:

```bash
wails build
```

The Go manager tests and standalone frontend production build can be run separately:

```bash
go test ./...
```

```bash
cd frontend
npm run build
```
