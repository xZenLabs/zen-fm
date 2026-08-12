# ZenFM

ZenFM is a single-owner web file manager built for KOReader devices. KOReader
starts a small Go service on the device; a phone or computer connects to its
embedded React interface to manage files.

The project is a clean-break successor to File Browser. It keeps the useful
file-management model while replacing Vue and self-contained JWTs with a
TypeScript/React/MUI frontend and revocable server-side sessions.

## Features

- Browse, search, upload, download, create, edit, move, copy, rename, and delete
  files from a responsive list or grid.
- Resumable uploads, checksums, previews, archives, public shares, themes, and
  localization.
- One owner account, opaque browser sessions, CSRF protection, and separate
  expiring personal API tokens.
- HTTPS by default with a per-device certificate.
- Optional 30-minute server auto-stop and an explicit HTTP fallback.
- Advanced `/` mode for owners who intentionally need the entire device
  filesystem, including ZenFM's own state and certificates.

## First start

1. Install the KOReader bundle matching the device and restart KOReader.
2. Open **Tools > Network > ZenFM** and start the server.
3. Open the URL displayed by the plugin from another device.
4. Sign in with `koreader` / `koreader123456789` and immediately choose a new
   password. File and token APIs remain disabled until that change succeeds.

HTTPS uses a locally generated certificate, so a browser may require the owner
to confirm it. HTTP can be selected explicitly for incompatible setups, but it
exposes credentials and files to the local network.

## Runtime defaults

| Policy | Default |
| --- | --- |
| Browser idle / absolute lifetime | 2 hours / 12 hours |
| Personal API token | 30 days; configurable up to 1 year |
| Password-protected share session | 30 minutes, capped by share expiry |
| Ordinary browser request | 30 seconds; the owner may set `0` to disable the client timer |
| Search request | 2 minutes |
| Upload and download | No total limit; 30 seconds without progress |
| Server auto-stop | Off; KOReader offers a 30-minute preset |
| HTTPS / explicit HTTP ports | 8443 / 8080 |

The `serve` command exposes `--session-idle` and `--session-absolute` for
controlled deployments and qualification tests; KOReader uses the defaults.

## Release artifacts

| Environment | Artifact |
| --- | --- |
| 32-bit Kindle/Kobo/other e-reader | `ZenFM-koreader-ereader-<version>.zip` |
| Linux KOReader (amd64/arm64) | `ZenFM-koreader-linux-<version>.zip` |
| macOS KOReader | `ZenFM-koreader-macos-<version>.zip` |
| Android KOReader plugin | `ZenFM-koreader-android-<version>.zip` |
| Android backend companion | `ZenFM-android-<version>.apk` |

Android requires both the plugin ZIP and companion APK. Other bundles contain
their matching backend directly.

On the first Android start, approve the native **Start ZenFM?** prompt only
when you just selected **Start ZenFM** in KOReader. That first approval links
the companion to KOReader. Sensitive start, reset, and update
requests remain confirmation-gated because KOReader settings may be on shared
storage. ZenFM requests storage access before launching the server; denying it
leaves the server stopped.

On BOOX devices, **ZenFM Backend** appears under Apps. Turn off **App Freeze**
for it under **Apps > App Management**, because a frozen companion cannot
receive KOReader's start request. Opening the entry goes to Android's app-info
screen; ZenFM itself is still started from KOReader.

KOReader plugin updates activate atomically and roll back automatically after a
failed health check. Companion APK updates are signature/hash checked,
journaled across Android's Package Installer, and health-gated after
replacement. Android does not grant an ordinary sideloaded app silent downgrade
authority, so a broken APK that cannot launch requires an owner-approved newer
signed APK or manual reinstall of the previous trusted APK.

## Development

For UI work without Go or a device:

```sh
cd frontend
npm ci
npm run dev:mock
```

Open <http://localhost:5173> and use
`koreader` / `zenfm-demo-password`. The mock API and its sample filesystem live
only in memory and reset when Vite restarts.

See [docs/development.md](docs/development.md) for local full-stack work,
device builds, KOReader installation paths, Android deployment, and device
logs.

Backend checks:

```sh
go test ./...
go test -race ./...
```

Frontend checks:

```sh
cd frontend
npm ci
npm run typecheck
npm test
npm run build
```

Build an unsigned e-reader development bundle with:

```sh
sh build.sh --dev
```

This builds only the e-reader backends and does not require the Android or
macOS build toolchains. To build and install the Android companion and its
KOReader plugin on an authorized ADB device, run:

```sh
sh build.sh --dev --android
```

Run `sh build.sh` for all local packages.
CI supplies the persistent Android signing credentials when producing release
packages.

The API contract is under `docs/api/`; architecture and security decisions are
documented under `docs/` and in [SECURITY.md](SECURITY.md).

## License

AGPL-3.0. See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for reused
project attribution.
