## TL;DR

[In 1–3 sentences, state the user-visible change in Nala Ctrl and who benefits.]

## Process Flow

When the issue has a process or UI flow, use a UML sequence diagram with actors and lifelines, directional messages, and activation or return markers. Highlight the implementation change and label it `IMPLEMENTATION CHANGE`. Do not replace a requested sequence diagram with a generic flowchart.

![Rendered Process Flow](<Linear attachment URL>)

Keep the exact SVG source paired with the inline image in a collapsed native Linear source block:

~~~xml
<svg>
[exact SVG source used for the inline image]
</svg>
~~~

If the issue has no meaningful process flow, state `Not applicable` and why.

## Before-After

### Before

![Rendered Before diagram](<Linear attachment URL>)

~~~xml
<svg>
[exact before SVG source used for the inline image]
</svg>
~~~

### After

![Rendered After diagram](<Linear attachment URL>)

~~~xml
<svg>
[exact after SVG source used for the inline image]
</svg>
~~~

If no visual/process reference applies, describe the observable behavior before and after in text and state why diagrams do not apply. Preserve user-supplied screenshots, HTML, SVG, and examples verbatim. Verify saved issue content and asset/source pairing through Linear's connected integration or documented API; never use a browser or screen capture to inspect the issue.

## Implementation Manual Test and Verification

Record the exact task-specific steps and expected results. For Go checks, run from the repository root. For the frontend build, run from `frontend/`. Use Bash blocks that are independently pasteable. Capture each target command's immediate exit status before follow-up work. Do not use `set -e`, `set -Eeuo pipefail`, `set -o pipefail`, bulk runners, or generated verification scripts. Do not present a placeholder as observed evidence.

### Step 0 — Confirm the local project and toolchain

~~~bash
pwd
target_status=$?
printf 'target command exit status: %s\n' "$target_status"
~~~

Expected result: repository root is the checked-out Nala Ctrl repository; report the observed path and status.

### Step 1 — Run Go checks

~~~bash
go test ./...
target_status=$?
printf 'target command exit status: %s\n' "$target_status"
~~~

Expected result: exit 0, or record the exact failure and affected package.

### Step 2 — Build the frontend

~~~bash
cd frontend
npm run build
target_status=$?
printf 'target command exit status: %s\n' "$target_status"
~~~

Expected result: TypeScript compilation and Vite production build exit 0.

### Step 3 — Verify the native desktop flow

~~~bash
wails dev
target_status=$?
printf 'target command exit status: %s\n' "$target_status"
~~~

Expected result: record whether the Wails window opened, then describe the configured project selected and the visible result for this issue. Exercise run/stop, log updates, or confirmed port takeover only when relevant. A build or browser observation does not prove native interaction.

### Step 4 — Record evidence and limitations

For every step, record the exact command/action, immediate status, relevant output or visible result, expected outcome, classification, and configured project fixture. Mark native behavior `not run` when the Wails window or required action was not exercised. Keep credentials and unrelated process arguments out of issue and PR text. Unit tests and local fixtures prove only exercised paths; do not claim arbitrary process discovery or remote service behavior.
