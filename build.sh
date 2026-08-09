#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
VERSION_FILE="$SCRIPT_DIR/VERSION"
PLUGIN_SOURCE="$SCRIPT_DIR/plugin/zenfm.koplugin"
DIST_DIR="$SCRIPT_DIR/dist"
BUILD_DIR="$SCRIPT_DIR/.build"
PACKAGE_ONLY=
BINARY_DIR=
APK_PATH=
DEV_UNSIGNED=0
WEBUI_DIR="$SCRIPT_DIR/internal/webui/dist"
WEBUI_BACKUP="$BUILD_DIR/webui-fallback"
WEBUI_PREPARED=0

usage() {
    echo "usage: $0 [--package-only BINARY_DIR --apk APK] [--dev-unsigned]" >&2
    exit 2
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --package-only) [ "$#" -ge 2 ] || usage; PACKAGE_ONLY=1; BINARY_DIR=$2; shift 2 ;;
        --apk) [ "$#" -ge 2 ] || usage; APK_PATH=$2; shift 2 ;;
        --dev-unsigned) DEV_UNSIGNED=1; shift ;;
        *) usage ;;
    esac
done

VERSION=$(sed -n '1{s/^[[:space:]]*//;s/[[:space:]]*$//;p;}' "$VERSION_FILE")
printf '%s\n' "$VERSION" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$' || {
    echo "VERSION must contain valid SemVer" >&2
    exit 2
}

PUBLIC_KEY=${ZENFM_RELEASE_PUBLIC_KEY_HEX:-}
if [ "$DEV_UNSIGNED" -ne 1 ]; then
    case "$PUBLIC_KEY" in *[!0-9a-fA-F]*|'') echo "Set ZENFM_RELEASE_PUBLIC_KEY_HEX to the pinned 32-byte Ed25519 public key" >&2; exit 2 ;; esac
    [ "${#PUBLIC_KEY}" -eq 64 ] || { echo "ZENFM_RELEASE_PUBLIC_KEY_HEX must be 64 hexadecimal characters" >&2; exit 2; }
fi

cleanup() {
    if [ "$WEBUI_PREPARED" -eq 1 ] && [ -d "$WEBUI_BACKUP" ]; then
        rm -rf "$WEBUI_DIR"
        mkdir -p "$WEBUI_DIR"
        cp -R "$WEBUI_BACKUP/." "$WEBUI_DIR/"
    fi
    rm -rf "$BUILD_DIR"
}
trap cleanup EXIT INT TERM
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR/bin" "$BUILD_DIR/stage" "$DIST_DIR"

if [ -z "$PACKAGE_ONLY" ]; then
    if [ -f "$SCRIPT_DIR/frontend/package.json" ]; then
        echo "Building React frontend..."
        (cd "$SCRIPT_DIR/frontend" && npm ci && npm run build)
        mkdir -p "$WEBUI_BACKUP"
        cp -R "$WEBUI_DIR/." "$WEBUI_BACKUP/"
        WEBUI_PREPARED=1
        rm -rf "$WEBUI_DIR"
        mkdir -p "$WEBUI_DIR"
        cp -R "$SCRIPT_DIR/frontend/dist/." "$WEBUI_DIR/"
    fi
    echo "Building KOReader backends..."
    GOFLAGS='-trimpath -buildvcs=false'
    LDFLAGS="-s -w -buildid= -X main.version=$VERSION"
    LEGACY_GO=${ZENFM_LEGACY_GO:-$SCRIPT_DIR/.toolchains/go1.26.5-kindle/bin/go}
    if [ ! -x "$LEGACY_GO" ] && [ -z "${ZENFM_LEGACY_GO:-}" ]; then
        "$SCRIPT_DIR/toolchains/legacy/bootstrap.sh"
    fi
    [ -x "$LEGACY_GO" ] || {
        echo "Patched supported legacy ARM compiler is unavailable." >&2
        echo "Bootstrap toolchains/legacy or set ZENFM_LEGACY_GO; unpatched/EOL Go is not accepted." >&2
        exit 2
    }
    GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 GOFLAGS="$GOFLAGS" "$LEGACY_GO" build -ldflags "$LDFLAGS" -o "$BUILD_DIR/bin/zenfm-hf" ./cmd/zenfm
    GOOS=linux GOARCH=arm GOARM=5 CGO_ENABLED=0 GOFLAGS="$GOFLAGS" "$LEGACY_GO" build -ldflags "$LDFLAGS" -o "$BUILD_DIR/bin/zenfm-sf" ./cmd/zenfm
    GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOFLAGS="$GOFLAGS" go build -ldflags "$LDFLAGS" -o "$BUILD_DIR/bin/zenfm-linux-arm64" ./cmd/zenfm
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOFLAGS="$GOFLAGS" go build -ldflags "$LDFLAGS" -o "$BUILD_DIR/bin/zenfm-linux-amd64" ./cmd/zenfm
    GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 GOFLAGS="$GOFLAGS" go build -ldflags "$LDFLAGS" -o "$BUILD_DIR/bin/zenfm-darwin-arm64" ./cmd/zenfm
    GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 GOFLAGS="$GOFLAGS" go build -ldflags "$LDFLAGS" -o "$BUILD_DIR/bin/zenfm-darwin-amd64" ./cmd/zenfm
    command -v lipo >/dev/null 2>&1 || { echo "lipo is required for the universal macOS bundle" >&2; exit 2; }
    lipo -create -output "$BUILD_DIR/bin/zenfm-darwin" \
        "$BUILD_DIR/bin/zenfm-darwin-amd64" "$BUILD_DIR/bin/zenfm-darwin-arm64"
    BINARY_DIR="$BUILD_DIR/bin"
    if [ "$DEV_UNSIGNED" -eq 1 ]; then
        ZENFM_ANDROID_DEV_UNSIGNED=1 "$SCRIPT_DIR/android/build.sh"
    else
        "$SCRIPT_DIR/android/build.sh"
    fi
    APK_PATH="$SCRIPT_DIR/android/app/build/outputs/apk/release/app-release.apk"
fi

for binary in zenfm-hf zenfm-sf zenfm-linux-arm64 zenfm-linux-amd64 zenfm-darwin; do
    [ -f "$BINARY_DIR/$binary" ] || { echo "missing package binary: $BINARY_DIR/$binary" >&2; exit 2; }
done
[ -f "$APK_PATH" ] || { echo "missing Android APK; pass --apk when using --package-only" >&2; exit 2; }

stage_plugin() {
    target=$1
    rm -rf "$target"
    mkdir -p "$target/zenfm.koplugin/backend"
    cp -R "$PLUGIN_SOURCE/." "$target/zenfm.koplugin/"
    rm -rf "$target/zenfm.koplugin/tests" "$target/zenfm.koplugin/data"
    cp "$VERSION_FILE" "$target/zenfm.koplugin/VERSION"
    cp "$SCRIPT_DIR/LICENSE" "$target/zenfm.koplugin/LICENSE"
    cp "$SCRIPT_DIR/THIRD_PARTY_NOTICES.md" "$target/zenfm.koplugin/THIRD_PARTY_NOTICES.md"
    sed -E "s/version = \"[^\"]*\"/version = \"$VERSION\"/" \
        "$target/zenfm.koplugin/_meta.lua" > "$target/zenfm.koplugin/_meta.lua.tmp"
    mv "$target/zenfm.koplugin/_meta.lua.tmp" "$target/zenfm.koplugin/_meta.lua"
    if [ "$DEV_UNSIGNED" -ne 1 ]; then
        printf 'return %s\n' "\"$(printf '%s' "$PUBLIC_KEY" | tr 'A-F' 'a-f')\"" \
            > "$target/zenfm.koplugin/release_public_key.lua"
    fi
    find "$target/zenfm.koplugin" -type f -name '*.sh' -exec chmod 700 {} +
}

EREADER_STAGE="$BUILD_DIR/stage/ereader"
LINUX_STAGE="$BUILD_DIR/stage/linux"
MACOS_STAGE="$BUILD_DIR/stage/macos"
ANDROID_STAGE="$BUILD_DIR/stage/android"
stage_plugin "$EREADER_STAGE"
stage_plugin "$LINUX_STAGE"
stage_plugin "$MACOS_STAGE"
stage_plugin "$ANDROID_STAGE"

cp "$BINARY_DIR/zenfm-hf" "$EREADER_STAGE/zenfm.koplugin/backend/zenfm-hf"
cp "$BINARY_DIR/zenfm-sf" "$EREADER_STAGE/zenfm.koplugin/backend/zenfm-sf"
cp "$BINARY_DIR/zenfm-linux-arm64" "$LINUX_STAGE/zenfm.koplugin/backend/zenfm-linux-arm64"
cp "$BINARY_DIR/zenfm-linux-amd64" "$LINUX_STAGE/zenfm.koplugin/backend/zenfm-linux-amd64"
cp "$BINARY_DIR/zenfm-darwin" "$MACOS_STAGE/zenfm.koplugin/backend/zenfm-darwin"
chmod 700 "$EREADER_STAGE"/zenfm.koplugin/backend/* "$LINUX_STAGE"/zenfm.koplugin/backend/* "$MACOS_STAGE"/zenfm.koplugin/backend/*

cat > "$LINUX_STAGE/zenfm.koplugin/backend/zenfm-linux" <<'EOF'
#!/bin/sh
set -eu
DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
case "$(uname -m 2>/dev/null || echo unknown)" in
    x86_64|amd64) exec "$DIR/zenfm-linux-amd64" "$@" ;;
    arm64|aarch64) exec "$DIR/zenfm-linux-arm64" "$@" ;;
    *) echo "Unsupported Linux architecture" >&2; exit 1 ;;
esac
EOF
chmod 700 "$LINUX_STAGE/zenfm.koplugin/backend/zenfm-linux"

EREADER_ZIP="$DIST_DIR/ZenFM-koreader-ereader-$VERSION.zip"
LINUX_ZIP="$DIST_DIR/ZenFM-koreader-linux-$VERSION.zip"
MACOS_ZIP="$DIST_DIR/ZenFM-koreader-macos-$VERSION.zip"
ANDROID_ZIP="$DIST_DIR/ZenFM-koreader-android-$VERSION.zip"
APK_OUT="$DIST_DIR/ZenFM-android-$VERSION.apk"
rm -f "$EREADER_ZIP" "$LINUX_ZIP" "$MACOS_ZIP" "$ANDROID_ZIP" "$APK_OUT"
(cd "$EREADER_STAGE" && zip -qr "$EREADER_ZIP" zenfm.koplugin)
(cd "$LINUX_STAGE" && zip -qr "$LINUX_ZIP" zenfm.koplugin)
(cd "$MACOS_STAGE" && zip -qr "$MACOS_ZIP" zenfm.koplugin)
(cd "$ANDROID_STAGE" && zip -qr "$ANDROID_ZIP" zenfm.koplugin)
cp "$APK_PATH" "$APK_OUT"

MANIFEST="$DIST_DIR/ZenFM-release-manifest-$VERSION.txt"
SIGNATURE="$DIST_DIR/ZenFM-release-manifest-$VERSION.sig"
{
    printf 'zenfm-release-manifest-v1\nversion\t%s\n' "$VERSION"
    for asset in "$APK_OUT" "$ANDROID_ZIP" "$EREADER_ZIP" "$LINUX_ZIP" "$MACOS_ZIP"; do
        size=$(wc -c < "$asset" | tr -d ' ')
        digest=$(shasum -a 256 "$asset" | sed 's/[[:space:]].*//')
        printf 'asset\t%s\t%s\t%s\n' "$(basename "$asset")" "$size" "$digest"
    done
} > "$MANIFEST"

if [ "$DEV_UNSIGNED" -eq 1 ]; then
    rm -f "$SIGNATURE"
    echo "Built unsigned development packages; in-plugin updates remain disabled."
else
    "$SCRIPT_DIR/scripts/sign-manifest.sh" "$MANIFEST" "$SIGNATURE" "$PUBLIC_KEY"
fi

echo "Built ZenFM $VERSION KOReader bundles and Android companion in $DIST_DIR"
