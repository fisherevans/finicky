# Passive local timing

This branch starts from Finicky v4.2.2, commit 53414af0c1caba2a606e512848486366baa9c366.
It adds local diagnostics. The Chrome handoff changes eligible browser launches; see CHROME-HANDOFF.md.
The installed bundle uses version 4.2.2, build 4.2.2.2, with FinickyInstrumentation=passive-timing-2.

## Data and bounds

Metrics live in `~/Library/Logs/Finicky/timing/events.jsonl`.
The recorder keeps four rotated files, at most 2 MiB each, plus the active file (10 MiB total).
Files use mode 0600. A newly created directory uses mode 0700.
A queue holds 256 events. Producers drop events instead of waiting for the writer.
The next written event reports accumulated drops. Disk failure can prevent that count from being persisted.
An abrupt exit can lose queued events. Metrics have no network destination.
The existing update check is separate; local builds with an empty API_HOST do not check for updates.

Events record the originating app bundle ID, hostnames, configured app and profile labels, fixed error categories, timings, and the interval since the preceding URL receipt.
They exclude URL paths, queries, fragments, user information, command arguments, and raw error text.
Unexpected characters in app and profile labels produce `redacted`.
The existing request logger is separate. Keep `options.logRequests: false`; it can expose full URLs.

Stages cover URL queue wait, short URL resolution, routing, profile lookup, command start, command wait, and total browser dispatch.
Dispatch completion means Chrome acknowledged the handoff or the macOS `open` command returned. It does not mean the browser displayed the page.
The recorder also saves the foreground app at receipt and passive macOS foreground events for 15 seconds after the most recent receipt.
Foreground events have no request ID because overlapping requests prevent causal attribution.
The summary correlates matching apps by time; it cannot identify the correct Chrome profile, tab, or page readiness.
An already foreground browser may emit no activation event. Background opens need not activate a browser.
A busy main run loop can delay delivery of macOS notifications.
Time before the Go URL handler, including the original click and app startup before URL delivery, is outside these timings.
The idle interval resets on process restart.

## Read the metrics

Run `python3 scripts/timing-summary.py` for median, p95, maxima, destination groups, idle groups, and requests taking at least one second.
Use `--json` for recent requests, individual stages, fixed error categories, foreground correlations, and incomplete requests.
Use `--slow-ms 500` to change the threshold. The script reads only the timing directory, never ordinary request logs.
Rotation can split a request across retained history. Do not treat incomplete data as evidence of a launch failure.

## Build and verify

Use `bash scripts/build-local.sh` to build and ad-hoc sign without modifying the installed app.
See LOCAL-INSTALL.md for prerequisites and installation.

Run `go test -C apps/finicky/src ./...` and `npm --prefix packages/config-api test -- --run`.
Run `go test -C apps/finicky/src -race ./timing` for concurrent recorder validation.
The routing regression test uses a synthetic configuration.
It compares representative destinations against a frozen copy of v4.2.2 evaluation code without opening any URL.
Browser argument tests replace `open` with a local stub and compare against the v4.2.2 launcher.
Keep the frozen comparison functions unchanged when editing instrumentation.

The local app must be signed after the build. An ad-hoc signature verifies local integrity but cannot preserve the upstream developer identity or notarization.
Keep an unmodified copy of the original signed app outside `/Applications` before replacing it.
Stop the existing Finicky process, copy the staged app into `/Applications/Finicky.app`, verify its signature, and launch it.
Restore the original app from that backup to roll back. Do not replace the user's config.
