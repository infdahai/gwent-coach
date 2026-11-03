# Windows resident control MVP

`coach control` keeps one host in the signed-in Windows desktop. Mac and Windows
clients use authenticated loopback HTTP; Mac reaches it through an SSH tunnel.
Capture, manual click and hold work with an ordinary foreground application. Existing
`coach` observation flags remain Gwent-only. The optional `run-session` command
uses the existing calibrated Gwent session runner and its journals/interlock.
This does not establish autonomous match acceptance.

## Build and install

Build on Mac from the repository root (Go version from `go.mod`):

```sh
mkdir -p dist
go build -o dist/coach ./cmd/coach
GOOS=windows GOARCH=amd64 go build -ldflags="-H=windowsgui" -o dist/coach.exe ./cmd/coach
```

The Windows GUI subsystem build avoids creating a console that steals the
foreground. The scheduled PowerShell launcher also uses `-WindowStyle Hidden`;
its piped stdout/stderr still go to the private host log. When invoking this GUI
build directly from an interactive PowerShell prompt, append `| Out-Host` to wait
for output. The Mac client remains a normal console binary.

Copy `coach.exe` and `scripts/control-host.ps1` to a private Windows tools folder
using your existing SSH connection. The examples use `$HOME\Tools\GwentCoach`;
replace it with your own folder. Do not put tokens, profiles, captured images, or
runtime state in the public repository. Sign into the Windows desktop as the
same user running the installer. An SSH service session itself cannot capture
or control the interactive desktop.

In Windows PowerShell, install once and start explicitly:

```powershell
$tools = Join-Path $HOME 'Tools\GwentCoach'
$state = Join-Path $env:LOCALAPPDATA 'GwentCoachControl'
$stop = Join-Path $env:LOCALAPPDATA 'GwentCoachControl.STOP'
& "$tools\control-host.ps1" -Action Install -Exe "$tools\coach.exe" -StateDir $state -StopFile $stop
& "$tools\control-host.ps1" -Action Start -StateDir $state
& "$tools\control-host.ps1" -Action Status -StateDir $state
```

The installer creates one `GwentCoachResident` task with `Interactive` logon,
limited privileges, no execution time limit, no logon trigger and no automatic
restart. It creates a random token with a private Windows ACL inherited by the
state, captures and logs. `Install` refuses an existing task or state directory;
`Start` reuses the installed configuration. No task is created per experiment.
A successful start request is not proof the listener started: verify `Status`.
Startup errors appear in `$state\host.log` and the task's `LastTaskResult`.

For a host with Gwent sessions enabled, include `-SessionConfig` at installation
and set `-StopFile` to the **same absolute path** as the pinned loop profile's
`StopFile`. Keep the existing calibrated profile, hash, stop-file and interlock
requirements; this feature does not migrate them. A generic desktop-only host
needs no game profile. The HTTP client cannot submit or replace session profiles.
Do not edit installed configuration or profiles while the host is running.

For local diagnosis without a scheduled task, the equivalent interactive command
is (omit `-session-config` for desktop-only use):

```powershell
& "$tools\coach.exe" control host -address 127.0.0.1:17391 -state "$state\state" -token-file "$state\token" -stop-file $stop -session-config "$tools\profiles\session.json"
```

Use the installed ACL-protected directory. A manually created directory must have
an equivalent private Windows ACL; Go's Unix permission bits do not set one.
Only explicit loopback IPs are accepted. Session 0, locked/secure desktops,
minimized windows and cross-session input are refused. Run ordinary applications
at the same integrity level as the host; this MVP does not elevate input or
bypass Windows UIPI restrictions.

## Connect from Mac

Copy the token file through your existing SSH/SCP setup into a private local
file; do not print the token, place it in command arguments, or paste it into a
URL. For example, after substituting your SSH alias and Windows user path:

```sh
mkdir -p "$HOME/.config/gwent-coach"
chmod 700 "$HOME/.config/gwent-coach"
# Replace the source with the installed token's actual Windows path.
scp 'windows-desktop:AppData/Local/GwentCoachControl/token' "$HOME/.config/gwent-coach/control-token"
chmod 600 "$HOME/.config/gwent-coach/control-token"
ssh -N -o ExitOnForwardFailure=yes -L 127.0.0.1:17391:127.0.0.1:17391 windows-desktop
```

Keep that tunnel open. In another terminal:

```sh
TOKEN="$HOME/.config/gwent-coach/control-token"
./dist/coach control status -token-file "$TOKEN"
./dist/coach control capture -token-file "$TOKEN" -image /tmp/foreground.png
```

Inspect the PNG. Capture prints a receipt containing a `reference` ID, followed
by the native client geometry and capture time. Coordinates are **client pixels**,
not screen pixels or resized image coordinates. Only the most recent successful
capture in this host lifetime is eligible for input, and only for 30 seconds.
With that app still foreground, issue one reviewed click:

```sh
./dist/coach control click -token-file "$TOKEN" -reference CAPTURE_ID -x 120 -y 80
./dist/coach control hold -token-file "$TOKEN" -reference CAPTURE_ID -x 120 -y 80
```

Replace `CAPTURE_ID` with the printed 32-character hex ID. `hold` performs a fixed
1.5-second left-button press using the same click guards and fresh-reference rule.
Guards run every 50 ms during the hold; if the foreground, cursor, window or stop
latch changes, cleanup releases the button and returns `partial_or_unknown`. A
cancel or guard failure after mouse-down still attempts one cleanup release and
never repeats the down event. `delivered_unconfirmed` means both events were
accepted by `SendInput`, and the final guard passed, but the application may have
rejected or ignored the gesture. A new capture
supersedes the previous reference. Capture references are claimed durably before
cursor movement and can never be reused, including after an error. A changed
HWND, PID, session, desktop, DPI, client rectangle, minimized state, covered
point, moved cursor, out-of-bounds coordinate, Ctrl/F8 press, stop latch, or
cancellation refuses delivery. No application-specific window search is used.
Windows cannot atomically lock foreground state across `SendInput`; guards run
immediately before delivery and refuse observed changes. OS delivery still
requires visual confirmation of the result.

Windows uses the same client commands with `coach.exe` and `-token-file
"$state\token"`. For a different forwarded port, supply
`-url http://127.0.0.1:PORT`. The client disables proxies and redirects.
Browser origins/fetch metadata and non-loopback Host headers are refused; there
is no CORS, cookie authentication, arbitrary shell command, or public file server.

## Sessions, stop, receipts and logs

```sh
./dist/coach control run-session -token-file "$TOKEN"
./dist/coach control status -token-file "$TOKEN"
./dist/coach control receipt -token-file "$TOKEN" -id REQUEST_ID
./dist/coach control logs -token-file "$TOKEN" -id REQUEST_ID
./dist/coach control stop -token-file "$TOKEN"
```

`run-session` returns an accepted request ID promptly. Poll its receipt and
status to learn whether the session actually completed. While any operation is
active, other capture/click/session commands are rejected. In particular, no
manual action or capture can interrupt autonomous match acceptance. Status,
receipt, logs, stop and shutdown remain available.

Every mutation prints `request_id` to stderr **before** contacting the server.
You can also supply `-id` with a new 32-character lowercase hex value. The host
persists that ID before execution. A duplicate returns HTTP 409 with the original
receipt and never executes, even with a different payload or after restart.

An accepted `click` or `hold` claim also persists an optional `intent` object
containing the submitted capture `reference` and client `x`/`y`; zero coordinates
are retained. It is written before backend execution and is preserved in
completed, failed, rejected, duplicate and post-restart receipts. This records
requested input only, not proof that Windows delivered it or the application
acted on it. Receipts written by older hosts omit the field.

An HTTP response confirms transport, not application behavior. A receipt's
`accepted` state means pending; `completed` means the backend returned without
error; `failed` preserves its error and any input delivery detail. Click/hold input
`delivered_unconfirmed` means Windows accepted the events, not that the app did
what you intended. `partial_or_unknown`, cursor motion, and failed receipts must
be reconciled using the screen and logs. The CLI exits nonzero for failed/rejected
capture or click. Session semantic results remain in the native session journals.

Persisted input `intent` and `delivered_unconfirmed` receipts are not semantic
postcondition confirmation. Pre-input captures or phase histograms are
pre-action observations; they do not establish reconciliation or authorize
releasing a game interlock. Historical evidence of an unresolved action remains
unresolved until separately reconciled.

**On timeout, disconnect, or unreadable response, do not submit another click or
session.** Query the original ID with `receipt`, then `status` and `logs`. The
client never retries mutations; capture/click waiting polls receipts only. If a
receipt is absent or remains accepted after a crash, delivery is unresolved.
Never infer that an absent response means no input occurred.

`stop` writes and syncs the durable stop latch and cancels the active context.
Its own completed receipt acknowledges the stop request. Host state remains
`stopping` and retains the active ID until the task returns, then becomes `idle`
with `stop_signaled: true`. Input already delivered is not undone; releasing a
held button is allowed cleanup. Cancellation now reaches native session
preflight, navigation, recovery and final action guards. Capture can be used for
inspection after stopping, but clicks and sessions remain latched off.

An operator may remove **only the stop latch** after checking status, the screen,
receipts and native journals. Nothing automatically removes the stop latch,
reference claims, request claims, game action journals or autoplay interlock.
Unresolved game journals/interlocks require the existing recovery procedure;
changing request IDs does not resolve them.

Per-session output lives under `state/sessions/REQUEST_ID/`; HTTP logs contain
the first 256 KiB plus a truncation marker. Native observations and game journals
remain server-side. Capture retrieval accepts only generated IDs, never a path.
The host retains captures, receipts and session files for diagnosis; disk
retention/archiving is manual and must preserve unresolved evidence.

## Host lifecycle and recovery

Gracefully stop the resident process from Windows:

```powershell
& "$tools\control-host.ps1" -Action Stop -StateDir $state
Get-ScheduledTask -TaskName GwentCoachResident | Select-Object TaskName, State
```

Or use `coach control shutdown -token-file ...` from either client. Shutdown
signals durable stop, closes admission, waits for the active task, and then closes
the listener and releases `state/host.lock`. Its receipt acknowledges the
request; verify the task exited. A foreground console-subsystem diagnostic build
also supports Ctrl+C; use `shutdown` for the GUI build.
Restart with `Start`; the stop latch remains until explicitly reconciled.

Do not routinely kill the scheduled task or delete a lock to restart it.
After process termination/crash, `host.lock` intentionally remains and startup
refuses the state directory. Check its PID against the actual process, confirm
no previous host or session is running, and inspect persisted request receipts,
capture claims and native journals. Only after reconciliation may the abandoned
**host** lock be removed. This never authorizes deleting a game interlock or
journal. An accepted claim without a result is unresolved and is never replayed.

## Validation boundaries

Local fake-backend tests cover loopback/address restrictions, authentication,
browser rejection, concurrent input exclusion, duplicate/restart claims, stale
references and bounds, partial-delivery receipts, durable stop with delayed task
return, stop persistence failure, bounded logs, path confinement, graceful
shutdown admission, and non-replay after an ambiguous HTTP failure.

Windows amd64 builds and test compilation check platform integration. Actual
capture, click, Ctrl/F8, foreground/DPI/desktop transitions, scheduled-task
installation and SSH forwarding require the controller's Windows smoke test.
Do not run manual actions during autonomous match acceptance. This MVP adds no
UIAutomation, app launcher, shell endpoint, strategy changes or packaging system.
