# Local Chrome handoff

This fork forwards foreground, explicit-profile links to running Google Chrome through
Chromium's process-singleton socket. It avoids starting another Chrome executable for
each link. Local tests on October 16, 2026 measured page arrival at 212 ms through the
prototype and 4231 ms through the existing profile launch. The latter process waited
in the macOS dynamic loader during a one-second stack sample. The responsible system
service remains unconfirmed.

The implementation preserves the resolved profile-directory argument and URL. It
requires the current user's private socket directory, matching session cookies and
lock, matching peer PID and user, and Google's signed stable Chrome identity. Signed
Chrome update clones are accepted. Signature checks have a bounded caller wait and
at most one native check can remain outstanding.

This uses an internal Chromium protocol, not a public supported API. A Chrome update
can change it. The normal launch path remains in use for background opens, custom
arguments, other browsers, and opens without an explicit profile. Set the Finicky
process environment variable `FINICKY_CHROME_HANDOFF=0` to disable the handoff.

Before sending, an absent or unverifiable session falls back to the normal launch.
An explicit `SHUTDOWN` response also permits fallback. After a write, a missing or
invalid acknowledgement is an unknown delivery outcome: Finicky reports an error
and does not resend. The handoff never terminates Chrome or removes profile locks.

## Timing evidence

The existing bounded metadata log adds these events, correlated by request ID:

- `chrome_handoff`: total connection, identity verification, send, and acknowledgement time.
- `chrome_handoff_acknowledged`: Chrome accepted the command; this is not a page-load measurement.
- `chrome_handoff_not_sent`, `chrome_handoff_rejected`, or `chrome_handoff_unknown`: delivery outcome.
- `chrome_handoff_fallback_<reason>`: why Finicky used the normal launch path.

The new events contain no URL, cookies, executable path, or error text. Existing
dispatch metadata identifies the selected profile. Existing activation observations
identify the app only; they do not establish which profile or tab became visible.

## Offline validation

`go test -C apps/finicky/src -race ./...` uses temporary mock Unix sockets and stub
launch commands. It does not operate a browser. The route comparison uses a synthetic configuration. The stock argument
comparison explicitly disables the live handoff.

Live browser tests require the user’s confirmation. Verify page arrival,
focus, profile selection, and the handoff outcome after installation. Cold Chrome
fallback is covered with an absent mock session; do not terminate the user's Chrome
to test it without authorization.

## References

- [Chromium singleton protocol](https://chromium.googlesource.com/chromium/src/+/HEAD/chrome/browser/process_singleton_posix.cc)
- [Chrome signed update clones](https://chromium.googlesource.com/chromium/src/+/main/chrome/browser/mac/code_sign_clone_manager.h)
- [Apple dynamic code identity](https://developer.apple.com/documentation/security/seccodecopyguestwithattributes(_:_:_:_:))
