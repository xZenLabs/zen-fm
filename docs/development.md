# Development guide

Run commands from the ZenFM repository root unless a section says otherwise.
Use Node 24 and Go 1.26.5 to match CI.

## Frontend only, with mock data

The mock development server runs the real React application and an in-memory
API inside Vite. It does not start Go, read local files, or write device data.

```sh
cd frontend
npm ci
npm run dev:mock
```

Open the URL printed by Vite, normally <http://localhost:5173>, and sign in
with:

- Username: `koreader`
- Password: `zenfm-demo-password`

The mock contains folders, text/Markdown/CSV/image fixtures, a hidden file,
shares, settings, and token examples. Common file mutations and direct uploads
under 8 MiB update memory, so browser refreshes keep the current state.
Resumable TUS uploads and generated archives deliberately return `501` in mock
mode; use a real backend for those flows. Restarting Vite restores the original
fixtures. The mock credentials and data are only a UI aid; the mock server is
not a security boundary.

To inspect the responsive UI from another device on the same trusted network:

```sh
npm run dev:mock -- --host 0.0.0.0
```

Use the LAN URL printed by Vite. Do not expose the Vite server to the internet.

## Local full-stack development

Run the backend and Vite in separate terminals. Vite proxies `/api` to the
backend and keeps frontend hot reload enabled.

Terminal 1:

```sh
ZENFM_DEV_DIR="$(mktemp -d "${TMPDIR:-/tmp}/zenfm-dev.XXXXXX")"
mkdir -p "$ZENFM_DEV_DIR/root"
go build -trimpath -ldflags '-X main.version=dev' -o "$ZENFM_DEV_DIR/zenfm" ./cmd/zenfm
"$ZENFM_DEV_DIR/zenfm" serve \
  --root "$ZENFM_DEV_DIR/root" \
  --data-dir "$ZENFM_DEV_DIR/state" \
  --listen 127.0.0.1:8080 \
  --control-socket "$ZENFM_DEV_DIR/control.sock" \
  --insecure-http
```

Terminal 2:

```sh
cd frontend
npm ci
npm run dev
```

Open <http://localhost:5173>. First run uses
`koreader` / `koreader123456789` and requires an immediate password change.
The explicit HTTP flag is appropriate only for this loopback development
setup.

`ZENFM_API_PROXY` changes the normal Vite proxy target. This is useful when the
backend runs elsewhere:

```sh
cd frontend
ZENFM_API_PROXY=http://192.0.2.10:8080 npm run dev
```

When proxying a physical device, enable ZenFM's HTTP setting only on an
isolated development network and open Vite on the development machine at
`localhost`. For normal device use, keep ZenFM on HTTPS and use the embedded
frontend.

## Building development bundles

Build only the unsigned e-reader bundle with:

```sh
sh build.sh --dev
```

This produces `dist/ZenFM-koreader-ereader-<version>.zip` without requiring
the Android SDK, Gradle, or `lipo`.

The release-like development build produces the four KOReader ZIPs and the
Android companion APK. It is unsigned, so in-plugin updates deliberately stay
disabled.

The all-platform builder currently runs on macOS because it creates a universal
macOS binary with `lipo`. It requires:

- Go 1.26.5 and Node 24;
- JDK 17 and Gradle 8.6;
- Android SDK platform/build-tools 34;
- Android NDK 25.2.9519653;
- `lipo`, `zip`, `unzip`, `patch`, `tar`, and `curl` or `wget`.

Configure Android and build:

```sh
export ANDROID_HOME=/absolute/path/to/android-sdk
export ANDROID_NDK_HOME="$ANDROID_HOME/ndk/25.2.9519653"
export GRADLE_BIN=gradle

sh build.sh
version="$(sed -n '1p' VERSION)"
sh scripts/verify-release-assets.sh dist "$version" --allow-unsigned
```

Without `ZENFM_RELEASE_PUBLIC_KEY_HEX`, `build.sh` automatically creates an
unsigned development build. CI supplies the public key and signing credentials
for signed release builds.

The first e-reader build bootstraps the pinned, patched Go 1.26.5 compiler for
old ARM kernels. Set `ZENFM_GO_SOURCE_ARCHIVE` to an already downloaded
`go1.26.5.src.tar.gz` when developing offline.

Do not install the contents of `dist/` after running
`sh tests/plugin/run.sh`: its package-layout test intentionally replaces that
directory with tiny fake fixtures. Always run the real `build.sh` command
after plugin tests and before installing on a device.

### Targeted plugin build

For a faster non-Android build, stage the frontend in a temporary source copy.
This avoids changing the checked-in fallback under `internal/webui/dist` and
does not require Gradle, the Android SDK, or `lipo`:

```sh
(cd frontend && npm ci && npm run build)

ZENFM_DEV_DIR="$(mktemp -d "${TMPDIR:-/tmp}/zenfm-device.XXXXXX")"
mkdir -p "$ZENFM_DEV_DIR/src" "$ZENFM_DEV_DIR/zenfm.koplugin/backend"
rsync -a \
  --exclude '.git/' --exclude '.build/' --exclude '.toolchains/' \
  --exclude 'dist/' --exclude 'frontend/node_modules/' --exclude 'frontend/dist/' \
  --exclude 'android/.gradle/' --exclude 'android/app/build/' \
  --exclude 'android/app/src/main/jniLibs/' \
  ./ "$ZENFM_DEV_DIR/src/"
sh "$ZENFM_DEV_DIR/src/scripts/stage-webui.sh" "$PWD/frontend/dist"
cp -R plugin/zenfm.koplugin/. "$ZENFM_DEV_DIR/zenfm.koplugin/"
cp VERSION "$ZENFM_DEV_DIR/zenfm.koplugin/VERSION"

ZENFM_VERSION="$(sed -n '1p' VERSION)"
ZENFM_LDFLAGS="-s -w -buildid= -X main.version=$ZENFM_VERSION"
```

Build the one backend needed by a 64-bit development device. The output name
is part of the plugin ABI and must stay unchanged:

```sh
# Linux amd64 desktop KOReader
(cd "$ZENFM_DEV_DIR/src" && env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -buildvcs=false -ldflags "$ZENFM_LDFLAGS" \
  -o "$ZENFM_DEV_DIR/zenfm.koplugin/backend/zenfm-linux-amd64" ./cmd/zenfm)

# Linux ARM64 Kobo or ARM64 desktop KOReader
(cd "$ZENFM_DEV_DIR/src" && env GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go build -trimpath -buildvcs=false -ldflags "$ZENFM_LDFLAGS" \
  -o "$ZENFM_DEV_DIR/zenfm.koplugin/backend/zenfm-linux-arm64" ./cmd/zenfm)

# Native Apple Silicon macOS KOReader; use GOARCH=amd64 on Intel
(cd "$ZENFM_DEV_DIR/src" && env GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 \
  go build -trimpath -buildvcs=false -ldflags "$ZENFM_LDFLAGS" \
  -o "$ZENFM_DEV_DIR/zenfm.koplugin/backend/zenfm-darwin" ./cmd/zenfm)
```

For 32-bit Kindle/Kobo development, retain the reviewed old-kernel toolchain
and build both float variants:

```sh
sh scripts/verify-legacy-toolchain.sh
sh toolchains/legacy/bootstrap.sh
ZENFM_LEGACY_GO="$PWD/.toolchains/go1.26.5-kindle/bin/go"

(cd "$ZENFM_DEV_DIR/src" && env GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 \
  "$ZENFM_LEGACY_GO" build -trimpath -buildvcs=false -ldflags "$ZENFM_LDFLAGS" \
  -o "$ZENFM_DEV_DIR/zenfm.koplugin/backend/zenfm-hf" ./cmd/zenfm)
(cd "$ZENFM_DEV_DIR/src" && env GOOS=linux GOARCH=arm GOARM=5 CGO_ENABLED=0 \
  "$ZENFM_LEGACY_GO" build -trimpath -buildvcs=false -ldflags "$ZENFM_LDFLAGS" \
  -o "$ZENFM_DEV_DIR/zenfm.koplugin/backend/zenfm-sf" ./cmd/zenfm)
```

Finish the stage and copy `$ZENFM_DEV_DIR/zenfm.koplugin` to KOReader's
`plugins/` directory:

```sh
chmod 700 "$ZENFM_DEV_DIR/zenfm.koplugin/supervisor.sh" \
  "$ZENFM_DEV_DIR/zenfm.koplugin/backend/"*
```

The source development key remains `UNCONFIGURED`, so verified updates fail
closed. The copied `VERSION` file is required: the launcher uses it and the
backend signature to decide when to refresh its private installed copy.

## Installing on KOReader devices

Stop ZenFM before replacing an installed plugin, then restart KOReader after
copying it. Each ZIP already contains the required top-level
`zenfm.koplugin` directory.

Typical writable KOReader plugin locations are:

| Device | Plugin directory |
| --- | --- |
| Kindle | `/mnt/us/koreader/plugins/zenfm.koplugin` |
| Kobo | `/mnt/onboard/.adds/koreader/plugins/zenfm.koplugin` |
| Android | `/storage/emulated/0/koreader/plugins/zenfm.koplugin` |
| Source-checkout desktop KOReader | `$KOREADER_DIR/plugins/zenfm.koplugin` |

The location can differ when KOReader was installed to another storage volume.
Use KOReader's writable data directory rather than its read-only application
directory.

### Kindle and 32-bit Kobo

Mount the reader over USB, set the path to its KOReader directory, and extract
the e-reader bundle:

```sh
version="$(sed -n '1p' VERSION)"
DEVICE_KOREADER=/Volumes/Kindle/koreader
unzip -oq "dist/ZenFM-koreader-ereader-$version.zip" -d "$DEVICE_KOREADER/plugins"
```

The bundle contains both ARM soft-float and hard-float backends; the plugin
chooses at runtime. For a 64-bit ARM Kobo, use the Linux bundle instead.

### Desktop KOReader

```sh
version="$(sed -n '1p' VERSION)"
KOREADER_DIR=/absolute/path/to/koreader
unzip -oq "dist/ZenFM-koreader-linux-$version.zip" -d "$KOREADER_DIR/plugins"
(cd "$KOREADER_DIR" && ./kodev run)
```

Use `ZenFM-koreader-macos-$version.zip` on macOS. A packaged KOReader build can
be started normally instead of through `kodev`.

### Android KOReader

Install the companion, expand the Android plugin on the host, and push it to
KOReader's shared plugin directory:

```sh
version="$(sed -n '1p' VERSION)"
ANDROID_STAGE="$(mktemp -d "${TMPDIR:-/tmp}/zenfm-android-plugin.XXXXXX")"

adb install -r "dist/ZenFM-android-$version.apk"
unzip -q "dist/ZenFM-koreader-android-$version.zip" -d "$ANDROID_STAGE"
adb push "$ANDROID_STAGE/zenfm.koplugin" /sdcard/koreader/plugins/
```

Restart KOReader, select **ZenFM > Start ZenFM**, then approve the companion's
pairing and all-files-access prompts. The APK is headless and has no normal
launcher screen. A debug-signed development APK cannot replace a release-signed
installation; Android will require the differently signed app to be removed
first, which clears its app-private ZenFM database, certificate, control
socket, and pairing state. Shared KOReader plugin settings and logs remain on
shared storage.

## Running and diagnosing on a device

In KOReader, open **ZenFM > Start ZenFM**, followed by
**ZenFM > Status and address**. The status dialog shows the device's LAN IP and
the active ZenFM listening port.

The default server is HTTPS on port 8443. Initial login is
`koreader` / `koreader123456789`; file APIs remain locked until the password is
changed. The plugin supervisor keeps the server detached and handles the
Kindle firewall. Prefer the plugin to a hand-started process for device tests.

On non-Android devices, runtime files live under
`<KOReader settings directory>/ZenFM`:

- `zenfm.log` contains backend and supervisor output;
- `zenfm.db` contains owner, session, share, and upload state;
- `backend/zenfm` is the installed backend copy;
- generated TLS material is under `tls/`.

On Android, the companion keeps the database, generated certificate, and
control socket in app-private storage. The KOReader `ZenFM` settings directory
contains plugin configuration, the pairing token, status handshakes, and
`android-companion.log`.

On non-Android devices, the short-lived local control socket is under
`/tmp/zenfm-<installation-hash>/control.sock`. If startup fails, stop ZenFM,
inspect the applicable log, confirm that the bundled backend matches the
device ABI, and verify that the selected root exists and is writable.

For rapid backend debugging over SSH, run the copied backend in the foreground
with the same `serve` flags shown by `cmd/zenfm/main.go`. This bypasses the
supervisor and Kindle firewall setup, so it is useful for logs but is not a
complete plugin lifecycle test.
