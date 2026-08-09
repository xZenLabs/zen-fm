#!/bin/sh
set -eu

[ "$#" -ge 2 ] && [ "$#" -le 3 ] || {
    echo "usage: verify-release-assets.sh DIST_DIR VERSION [--allow-unsigned]" >&2
    exit 2
}
DIST=$1
VERSION=$2
ALLOW_UNSIGNED=0
if [ "$#" -eq 3 ]; then
    [ "$3" = --allow-unsigned ] || { echo "unknown verification option: $3" >&2; exit 2; }
    ALLOW_UNSIGNED=1
fi

case "$VERSION" in
    ''|*[!0-9A-Za-z.+-]*) echo "invalid release version" >&2; exit 2 ;;
esac

EREADER="ZenFM-koreader-ereader-$VERSION.zip"
LINUX="ZenFM-koreader-linux-$VERSION.zip"
MACOS="ZenFM-koreader-macos-$VERSION.zip"
ANDROID="ZenFM-koreader-android-$VERSION.zip"
APK="ZenFM-android-$VERSION.apk"
MANIFEST="ZenFM-release-manifest-$VERSION.txt"
SIGNATURE="ZenFM-release-manifest-$VERSION.sig"

for name in "$EREADER" "$LINUX" "$MACOS" "$ANDROID" "$APK" "$MANIFEST"; do
    [ -s "$DIST/$name" ] || { echo "missing or empty release output: $name" >&2; exit 1; }
done
if [ "$ALLOW_UNSIGNED" -eq 1 ]; then
    [ ! -e "$DIST/$SIGNATURE" ] || { echo "unsigned development build has a signature" >&2; exit 1; }
else
    [ -s "$DIST/$SIGNATURE" ] || { echo "missing or empty release output: $SIGNATURE" >&2; exit 1; }
    command -v openssl >/dev/null 2>&1 || { echo "openssl is required to validate the signature" >&2; exit 2; }
    RAW_SIGNATURE=$(mktemp "${TMPDIR:-/tmp}/zenfm-manifest-signature.XXXXXX")
    cleanup() { rm -f "$RAW_SIGNATURE"; }
    trap cleanup EXIT INT TERM
    openssl base64 -d -A -in "$DIST/$SIGNATURE" -out "$RAW_SIGNATURE"
    [ "$(wc -c < "$RAW_SIGNATURE" | tr -d ' ')" -eq 64 ] || {
        echo "release manifest does not contain an Ed25519-sized signature" >&2
        exit 1
    }
fi

actual=$(find "$DIST" -maxdepth 1 -type f -print | sed 's!.*/!!' | sort)
if [ "$ALLOW_UNSIGNED" -eq 1 ]; then
    expected=$(printf '%s\n' "$ANDROID" "$APK" "$EREADER" "$LINUX" "$MACOS" "$MANIFEST" | sort)
else
    expected=$(printf '%s\n' "$ANDROID" "$APK" "$EREADER" "$LINUX" "$MACOS" "$MANIFEST" "$SIGNATURE" | sort)
fi
[ "$actual" = "$expected" ] || {
    echo "release output contains missing or unexpected ZenFM artifacts" >&2
    printf 'expected:\n%s\nactual:\n%s\n' "$expected" "$actual" >&2
    exit 1
}

contains() { unzip -Z1 "$1" | grep -Eq "$2"; }
contains "$DIST/$EREADER" '^zenfm\.koplugin/backend/zenfm-hf$'
contains "$DIST/$EREADER" '^zenfm\.koplugin/backend/zenfm-sf$'
contains "$DIST/$LINUX" '^zenfm\.koplugin/backend/zenfm-linux-amd64$'
contains "$DIST/$LINUX" '^zenfm\.koplugin/backend/zenfm-linux-arm64$'
contains "$DIST/$LINUX" '^zenfm\.koplugin/backend/zenfm-linux$'
contains "$DIST/$MACOS" '^zenfm\.koplugin/backend/zenfm-darwin$'
if unzip -Z1 "$DIST/$ANDROID" | grep -Eq '^zenfm\.koplugin/backend/zenfm-'; then
    echo "Android KOReader bundle must delegate to the companion app" >&2
    exit 1
fi
for archive in "$EREADER" "$LINUX" "$MACOS" "$ANDROID"; do
    contains "$DIST/$archive" '^zenfm\.koplugin/main\.lua$'
done
contains "$DIST/$APK" '^lib/armeabi-v7a/libzenfm_exec\.so$'
contains "$DIST/$APK" '^lib/arm64-v8a/libzenfm_exec\.so$'
if unzip -Z1 "$DIST/$APK" | grep -Eq '^lib/(x86|x86_64)/'; then
    echo "Android APK contains an unsupported native ABI" >&2
    exit 1
fi

[ "$(sed -n '1p' "$DIST/$MANIFEST")" = zenfm-release-manifest-v1 ] || {
    echo "unsupported release manifest format" >&2
    exit 1
}

for name in "$APK" "$ANDROID" "$EREADER" "$LINUX" "$MACOS"; do
    size=$(wc -c < "$DIST/$name" | tr -d ' ')
    if command -v sha256sum >/dev/null 2>&1; then
        digest=$(sha256sum "$DIST/$name" | awk '{print $1}')
    else
        digest=$(shasum -a 256 "$DIST/$name" | awk '{print $1}')
    fi
    awk -F '\t' -v name="$name" -v size="$size" -v digest="$digest" '
      $1 == "asset" && $2 == name && $3 == size && $4 == digest { found++ }
      END { exit found == 1 ? 0 : 1 }
    ' "$DIST/$MANIFEST" || { echo "manifest entry mismatch for $name" >&2; exit 1; }
done
[ "$(awk -F '\t' '$1 == "asset" { count++ } END { print count + 0 }' "$DIST/$MANIFEST")" -eq 5 ] || {
    echo "manifest must describe exactly four KOReader bundles and one APK" >&2
    exit 1
}

if [ "$ALLOW_UNSIGNED" -eq 1 ]; then
    echo "ok - exact four KOReader development bundles, APK, manifest, and platform layouts"
else
    echo "ok - exact four KOReader bundles, APK, signed manifest, and platform layouts"
fi
