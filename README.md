# Nala Ctrl

Nala Ctrl is a desktop app for running and monitoring configured projects and their logs. It starts each configured run command, captures output, and shows recent log lines. When a configured port is already in use, the app identifies its owner and asks before stopping it.

## Project configuration

Add project folders that contain `nala.yml`. Each file defines exactly one project, named from its folder basename. You can add multiple folders and switch between their logs while their processes keep running. If a file is missing, Nala Ctrl can create a config with `cwd` and `run`; `port` is optional and can be inferred from the run command.

```yaml
cwd: .
run: go run .
port: 8080
log: logs/application.log
```

- cwd and run are required. Relative working directories resolve from the selected folder.
- port is optional. Nala Ctrl detects common explicit port flags, Go --addr host:port listener addresses, and known defaults for Vite, Next.js, Python's HTTP server, and webpack. Set it explicitly when your command uses a different port. Without a detected port, the app can still run and capture logs, but cannot check for a port conflict first.
- log is optional and sets the log file path. By default, process output is appended to logs/<folder-name>.log under cwd.

Each folder you add contributes one project to the list. Select any project to monitor its status and logs; switching projects does not stop running commands. Run and Stop manage that project's command. Closing the app stops every process it started.

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
