# Architecture

ZenFM consists of a single-owner Go service, an embedded React application, a
KOReader launcher, and an Android companion used where KOReader cannot execute
the bundled backend directly.

## Runtime

The Go service owns the bbolt database and exposes a versioned same-origin HTTP
API. The React application is built by Vite, embedded in the binary, and uses
an HttpOnly session cookie plus a session-bound CSRF token. Personal API tokens
are bearer credentials for file automation only.

KOReader starts a detached backend selected for the device ABI. Persistent
plugin state lives below KOReader's settings directory and survives updates.
The Unix control socket instead lives in a short mode-0700 runtime directory
under `/tmp`, with the socket itself mode 0600; this works when persistent
storage is FAT and stays below Unix socket path limits. The LAN listener serves
the browser API and static application.

Android uses the same Go service as a native executable inside a foreground
service. The KOReader plugin sends in-process, explicit-component authenticated
intents to the companion. Native, overlay-resistant confirmation protects sensitive
commands because KOReader state may be shared storage; the APK contains no
WebView UI. The persistent, owner-requested local server declares Android's
`specialUse` foreground-service type rather than the time-limited `dataSync`
type. Android may still stop it under OS resource or background policy.

Plugin-tree updates use application-level atomic replacement and rollback. APK
updates instead use a durable journal around Android's Package Installer,
revalidate package identity, signing certificate, version, size, and SHA-256,
then health-check the embedded backend after replacement. An ordinary
sideloaded app cannot silently downgrade itself, so an APK that cannot launch
requires owner-approved manual recovery with a trusted signed APK.

## Storage and paths

The application database contains the owner record, settings, hashed sessions,
hashed API tokens, shares, and resumable-upload metadata. Served files remain
on the device filesystem.

Normal mode roots the file API at platform user storage. Advanced mode roots it
at `/`, exposes ZenFM's own regular files, and lists pseudo-filesystems and
special nodes. Content operations accept regular files and directories only;
recursive search/archive work skips pseudo-filesystems and special files.

## Source boundaries

- `cmd/` and `internal/`: Go executable and service packages.
- `frontend/`: React/MUI/Vite application and browser tests.
- `plugin/zenfm.koplugin/`: KOReader launcher and Lua tests.
- `android/`: headless companion APK.
- `docs/api/`: OpenAPI contract.
