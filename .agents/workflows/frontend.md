# Nala Ctrl desktop UI workflow

Apply this workflow to React/TypeScript UI changes under `frontend/`. The frontend runs inside the native Wails window.

## Delegation

For UI implementation, follow the delegation requirement in `AGENTS.md`: use a dedicated frontend implementation agent when the environment supports the required shared worktree and tools. The primary agent may review and deliver delegated work, but should not implement UI changes unless the user explicitly waives delegation. For changes limited to agent documentation or templates, this UI delegation rule does not apply.

## Implementation

- Read `.agents/knowledge/architecture.md` and the issue's behavior contract before changing the UI.
- Preserve the configured-project boundary: each explicitly added folder contributes exactly one project from its nala.yml. List all added projects, identify them by folder path, and let selection change the monitored view without stopping running projects.
- Use the existing Wails bindings and runtime events; do not invent a remote API or browser-only product flow.
- Keep native folder addition, one-project-per-file missing-config setup, project selection, Run/Stop, port conflict confirmation, log following, loading, empty, and error states observable. Adding a folder without nala.yml must retain existing projects and their running states.
- Keep `project:log` and `project:state` subscriptions scoped to the active view using stable project IDs. Unsubscribe on unmount or project change, and avoid displaying stale state after Run, Stop, or a silent process exit.
- Do not leave a clickable-looking control as a no-op. Implement it, remove it, or disable it with a clear explanation.
- Use semantic buttons, labels, headings, and status/log roles. Support keyboard activation, visible focus, Escape to dismiss a non-busy dialog, and useful error announcements.
- Treat project names, paths, commands, log lines, process names, and port-owner details as data; render them safely without HTML injection.

## Acceptance

- Build the frontend with `npm --prefix frontend run build` and run relevant Go tests when a Wails binding/event contract changes.
- Exercise the native Wails app at its default window size and a narrower usable size. Verify folder selection, config creation, project navigation, Run/Stop, live logs/state, and both Cancel and Confirm port-dialog paths when the change affects them.
- Use keyboard-only navigation for the affected controls and check focus order, dialog dismissal, disabled states, and error/status announcements.
- This is a desktop app: mobile browser layout checks do not apply. Do not use browser automation as a substitute for native-window interaction. If native interaction is unavailable, record the exact blocker and leave those checks unverified.
- Keep screenshots and traces outside Git unless the issue asks for a maintained fixture. Record exact commands and distinguish static code inspection, automated tests, and observed UI behavior.

## Handoff

Stage only intended frontend source, bindings, tests, and relevant project guidance. Update the existing task branch and PR when one exists. Include the normal user journey, controls exercised, viewport/window sizes, keyboard result, exact verification statuses, and any unrun native flow.
