# Nala Ctrl

Nala Ctrl is a desktop app for running and monitoring the projects declared in a selected `nala.yml`. It starts each configured run command, captures its output, and shows the latest log lines. When a configured port is already in use, the app shows the owning process and asks before stopping it.

## Project configuration

Choose a folder that contains `nala.yml`. If the file is missing, Nala Ctrl can create one project entry for you. A configuration can contain multiple projects:

```yaml
projects:
  backend:
    cwd: ./backend
    run: go run .
    port: 8080
  frontend:
    cwd: ./frontend
    run: npm run dev -- --port 5173
```

- `cwd` and `run` are required. Relative working directories are resolved from the selected folder.
- `port` is optional. Nala Ctrl detects common explicit port flags, Go `--addr host:port` listener addresses, and known defaults for Vite, Next.js, Python's HTTP server, and webpack. Set it explicitly when your command uses a different port. Without a detected port, the app can still run and capture logs, but cannot check for a port conflict first.
- `log` is optional and sets the log file path. By default, process output is appended to `logs/<project>.log` under that project's working directory.

Only entries in the selected `nala.yml` appear in the app. The Run and Stop controls manage the command started by Nala Ctrl. Closing the app stops processes it started.

## Development

Requirements: Go 1.26 or newer, Node.js 20.19+ or 22.12+, npm, and the platform dependencies required by Wails v2. See the [Wails installation guide](https://wails.io/docs/gettingstarted/installation) for the native desktop dependencies for your operating system.

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

Run the desktop app in development mode from the repository root with `wails dev` (Wails v2 does not provide a `wails run` command):

```bash
wails dev
```

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
