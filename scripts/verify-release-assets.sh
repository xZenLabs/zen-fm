#!/bin/sh
set -eu

[ "$#" -eq 2 ] || {
    echo "usage: verify-release-assets.sh DIST_DIR VERSION" >&2
    exit 2
}
DIST=$1
VERSION=$2

case "$VERSION" in
    ''|*[!0-9A-Za-z.+-]*) echo "invalid release version" >&2; exit 2 ;;
esac

EREADER="ZenFM-koreader-ereader-$VERSION.zip"
LINUX="ZenFM-koreader-linux-$VERSION.zip"
MACOS="ZenFM-koreader-macos-$VERSION.zip"
ANDROID="ZenFM-koreader-android-$VERSION.zip"
APK="ZenFM-android-$VERSION.apk"

for name in "$EREADER" "$LINUX" "$MACOS" "$ANDROID" "$APK"; do
    [ -s "$DIST/$name" ] || { echo "missing or empty release output: $name" >&2; exit 1; }
done

actual=$(find "$DIST" -maxdepth 1 -type f -print | sed 's!.*/!!' | sort)
expected=$(printf '%s\n' "$ANDROID" "$APK" "$EREADER" "$LINUX" "$MACOS" | sort)
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

echo "ok - exact four KOReader bundles, APK, and platform layouts"
