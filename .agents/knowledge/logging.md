# Nala Ctrl project logs

## Location and capture

- The default log path for a service is `logs/<service-name>.log` under that service's configured working directory.
- A service's optional `log` field overrides that path. Relative log paths resolve from the service working directory.
- Nala Ctrl appends both stdout and stderr to the service's file when it starts the configured command. It creates the log directory as needed.
- New complete lines are sent to the selected service's UI through Wails `project:log` events, tagged with both the project and service IDs. A final partial line is emitted when the process output writer closes.
- The UI starts with a snapshot of at most 250 recent lines and appends later events. The snapshot reads at most the latest 512 KiB of the file.

## Diagnosing a project

Check the selected service's resolved `cwd`, `run` command, and `log` path in its `nala.yml` entry under `projects`. Read that service's log and compare it with the status shown by Nala Ctrl. Refresh service details to recheck the process and configured port.

Child process output is shown as written; Nala Ctrl does not redact application log contents. Do not add credentials or unrelated environment values to diagnostic messages, screenshots, or tracker updates.
