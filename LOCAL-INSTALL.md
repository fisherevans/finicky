# Build and install locally

This fork avoids a Chrome startup delay when Finicky opens a link in an explicit
Chrome profile. It is based on Finicky 4.2.2. The change was tested on Apple Silicon
with macOS 26.7 and Chrome 155. Intel and older macOS versions are not validated.

## Prerequisites

- Xcode Command Line Tools: run `xcode-select --install` if they are missing.
- Node.js 22.12+, 24.x, or 26+, and Go 1.23.4 or newer.
  With Homebrew: `brew install node go`.
- Review the source and scripts before building. Dependency installation runs code
  from the packages pinned in the npm and Go lockfiles.

## Build

From the source directory:

```sh
bash scripts/build-local.sh
```

The helper installs locked dependencies, builds the UI and configuration API, runs
tests, builds Finicky for the host architecture, and ad-hoc signs and verifies the
bundle. It writes `build/local/Finicky.app`. It does not replace the installed app,
change the routing configuration, open a browser, or require `sudo`.

Ad-hoc signing requires no Apple Developer account. It seals the locally built
bundle; it does not notarize it or establish that the source or dependencies are
trustworthy. Managed-device policy can still prevent execution.

Do not run `scripts/build.sh` directly for this workflow: without a staging flag,
that upstream script replaces `/Applications/Finicky.app`.

## Install and undo

1. Quit Finicky from its menu bar icon. In Finder, compress the current
   `/Applications/Finicky.app` and keep the ZIP as your rollback copy.
2. Copy `build/local/Finicky.app` into `/Applications`, replacing the existing app.
3. Open Finicky. Keep it as the default browser and keep your existing routing
   configuration. Chrome does not need to restart.

To undo, quit Finicky, restore the app from your backup, and reopen it. Do not remove
your routing configuration.

This uses an internal Chrome handoff protocol. Chrome updates may require changes.
Other browsers, background opens, custom arguments, and opens without an explicit
profile keep the original launch path. If Chrome is closed, Finicky starts it normally.

Local builds disable the upstream update check. Rebuild intentionally when adopting
changes; a Homebrew reinstall can replace this fork with the official app.

The fork keeps bounded local timing metadata, including hostnames, app/profile names,
and durations, without URL paths or queries. These diagnostics do not upload records.
See [CHROME-HANDOFF.md](CHROME-HANDOFF.md) and [PASSIVE-TIMING.md](PASSIVE-TIMING.md).
