#!/bin/sh
set -eu

SOURCE_ROOT=$1
TMP=$(mktemp -d "${TMPDIR:-/tmp}/zenfm-package.XXXXXX")
trap 'rm -rf "$TMP"' EXIT INT TERM
ROOT="$TMP/source"
mkdir -p "$TMP/bin" "$ROOT/plugin"
cp "$SOURCE_ROOT/build.sh" "$SOURCE_ROOT/VERSION" "$SOURCE_ROOT/LICENSE" \
    "$SOURCE_ROOT/THIRD_PARTY_NOTICES.md" "$ROOT/"
cp -R "$SOURCE_ROOT/plugin/zenfm.koplugin" "$ROOT/plugin/"
for binary in zenfm-hf zenfm-sf zenfm-linux-arm64 zenfm-linux-amd64 zenfm-darwin; do
    printf '#!/bin/sh\nexit 0\n' > "$TMP/bin/$binary"
    chmod +x "$TMP/bin/$binary"
done
printf 'fake apk\n' > "$TMP/ZenFM.apk"

if "$ROOT/build.sh" --package-only --dev >"$TMP/missing-package-dir.log" 2>&1; then
    echo "package-only accepted a missing binary directory" >&2
    exit 1
fi
grep -q '^usage:' "$TMP/missing-package-dir.log"

mkdir -p "$ROOT/dist/stale-directory"
printf 'stale\n' > "$ROOT/dist/.stale"
printf 'stale\n' > "$ROOT/dist/stale-directory/old-package"
"$ROOT/build.sh" --package-only "$TMP/bin" --apk "$TMP/ZenFM.apk"
VERSION=$(sed -n '1p' "$ROOT/VERSION")
[ ! -e "$ROOT/dist/.stale" ]
[ ! -e "$ROOT/dist/stale-directory" ]

for platform in ereader linux macos android; do
    archive="$ROOT/dist/ZenFM-koreader-$platform-$VERSION.zip"
    [ -f "$archive" ]
    unzip -Z1 "$archive" | grep -q '^zenfm\.koplugin/main\.lua$'
    unzip -Z1 "$archive" | grep -q '^zenfm\.koplugin/LICENSE$'
    unzip -Z1 "$archive" | grep -q '^zenfm\.koplugin/THIRD_PARTY_NOTICES\.md$'
    if unzip -Z1 "$archive" | grep -Eq '(^|/)(tests?|\.git)(/|$)|\.py$'; then
        echo "source-only file leaked into $archive" >&2
        exit 1
    fi
done

unzip -Z1 "$ROOT/dist/ZenFM-koreader-ereader-$VERSION.zip" | grep -q 'backend/zenfm-hf$'
unzip -Z1 "$ROOT/dist/ZenFM-koreader-ereader-$VERSION.zip" | grep -q 'backend/zenfm-sf$'
for module in android_intent control daemon settings updater util; do
    unzip -Z1 "$ROOT/dist/ZenFM-koreader-ereader-$VERSION.zip" | grep -q "zenfm.koplugin/zenfm_$module.lua$"
done
if unzip -Z1 "$ROOT/dist/ZenFM-koreader-ereader-$VERSION.zip" \
    | grep -Eq 'zenfm\.koplugin/(android_intent|control|daemon|settings|updater|util)\.lua$'; then
    echo "generic Lua module name leaked into the e-reader bundle" >&2
    exit 1
fi
unzip -Z1 "$ROOT/dist/ZenFM-koreader-linux-$VERSION.zip" | grep -q 'backend/zenfm-linux-arm64$'
unzip -Z1 "$ROOT/dist/ZenFM-koreader-linux-$VERSION.zip" | grep -q 'backend/zenfm-linux-amd64$'
unzip -Z1 "$ROOT/dist/ZenFM-koreader-macos-$VERSION.zip" | grep -q 'backend/zenfm-darwin$'
if unzip -Z1 "$ROOT/dist/ZenFM-koreader-android-$VERSION.zip" | grep -q 'backend/zenfm-'; then
    echo "Android plugin unexpectedly contains a native server" >&2
    exit 1
fi
android_daemon=$(unzip -p "$ROOT/dist/ZenFM-koreader-android-$VERSION.zip" zenfm.koplugin/zenfm_daemon.lua)
android_intent=$(unzip -p "$ROOT/dist/ZenFM-koreader-android-$VERSION.zip" zenfm.koplugin/zenfm_android_intent.lua)
if printf '%s\n%s\n' "$android_daemon" "$android_intent" | grep -Eq '/system/bin/am|am start|ExceptionDescribe|openLink'; then
    echo "Android plugin contains an unsafe companion launch path" >&2
    exit 1
fi
printf '%s\n' "$android_intent" | grep -q 'org.zenlabs.zenfm.ZenFMActivity'
[ -f "$ROOT/dist/ZenFM-android-$VERSION.apk" ]
[ "$(find "$ROOT/dist" -mindepth 1 -maxdepth 1 | wc -l | tr -d ' ')" -eq 5 ]

mkdir -p "$TMP/ereader-bin"
cp "$TMP/bin/zenfm-hf" "$TMP/bin/zenfm-sf" "$TMP/ereader-bin/"
"$ROOT/build.sh" --package-only "$TMP/ereader-bin" --dev
unzip -Z1 "$ROOT/dist/ZenFM-koreader-ereader-$VERSION.zip" | grep -q 'backend/zenfm-hf$'
unzip -Z1 "$ROOT/dist/ZenFM-koreader-ereader-$VERSION.zip" | grep -q 'backend/zenfm-sf$'
[ "$(find "$ROOT/dist" -mindepth 1 -maxdepth 1 | wc -l | tr -d ' ')" -eq 1 ]

echo "ok - four KOReader bundles and Android package layout"
