# Nala Ctrl

Nala Ctrl is a local desktop app for running and monitoring the project declared in the selected `nala.yml`. It uses Wails v2 with a Go process manager and a React, TypeScript, and Vite frontend.

## Scope

- Manage the single project configured in the currently selected folder's `nala.yml`; do not discover or manage arbitrary machine processes.
- A project needs `cwd` and `run`. `port` is optional, and common command flags or development-server defaults are detected when possible. `log` optionally selects the output file.
- Show recent process output and stream new lines to the UI through Wails events.
- Starting a project checks its configured or detected port. If another process owns it, show the identity and require confirmation before rechecking and terminating that exact process.
- Stop processes started by Nala Ctrl when requested and when the app shuts down.

Read [architecture](knowledge/architecture.md) before changing process ownership, configuration, or UI event behavior. Read [logging](knowledge/logging.md) before changing log capture or diagnostics.

## Local commands

From the repository root:

```bash
go test ./...
```

```bash
cd frontend
npm run build
```

The native app uses the installed Wails v2 CLI: `wails dev` for development and `wails build` for packaging. Native packaging also needs the platform prerequisites documented by Wails.
