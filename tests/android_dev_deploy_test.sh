#!/bin/sh
set -eu

SOURCE_ROOT=$1
WORK=$(mktemp -d "${TMPDIR:-/tmp}/zenfm-android-deploy.XXXXXX")
WORK=$(CDPATH= cd -- "$WORK" && pwd)
trap 'rm -rf "$WORK"' EXIT INT TERM

ROOT="$WORK/source"
BIN_DIR="$WORK/bin"
ADB_LOG="$WORK/adb.log"
mkdir -p "$ROOT/android/app/build/outputs/apk/release" "$ROOT/plugin" "$BIN_DIR"
cp "$SOURCE_ROOT/build.sh" "$SOURCE_ROOT/VERSION" "$SOURCE_ROOT/LICENSE" \
    "$SOURCE_ROOT/THIRD_PARTY_NOTICES.md" "$ROOT/"
cp -R "$SOURCE_ROOT/plugin/zenfm.koplugin" "$ROOT/plugin/"

cat > "$ROOT/android/build.sh" <<'EOF'
#!/bin/sh
set -eu
[ "${ZENFM_ANDROID_DEV_UNSIGNED:-}" = 1 ]
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
printf 'development apk\n' > "$root/android/app/build/outputs/apk/release/app-release.apk"
EOF

cat > "$BIN_DIR/adb" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$TEST_ADB_LOG"
case "$1" in
    get-state) printf 'device\n' ;;
    install) [ "$2" = -r ] && [ -f "$3" ] ;;
    shell) [ "$2" = mkdir ] && [ "$3" = -p ] && [ "$4" = /sdcard/koreader/plugins ] ;;
    push)
        [ -f "$2/main.lua" ]
        [ "$3" = /sdcard/koreader/plugins/ ]
        ;;
    *) exit 1 ;;
esac
EOF
chmod 700 "$ROOT/android/build.sh" "$BIN_DIR/adb"

if PATH="$BIN_DIR:$PATH" ADB_BIN=adb TEST_ADB_LOG="$ADB_LOG" \
    "$ROOT/build.sh" --android > "$WORK/missing-dev.log" 2>&1; then
    echo "--android was accepted without --dev" >&2
    exit 1
fi
grep -q '^usage:' "$WORK/missing-dev.log"

PATH="$BIN_DIR:$PATH" ADB_BIN=adb TEST_ADB_LOG="$ADB_LOG" \
    "$ROOT/build.sh" --dev --android

VERSION=$(sed -n '1p' "$ROOT/VERSION")
APK="$ROOT/dist/ZenFM-android-$VERSION.apk"
PLUGIN="$ROOT/dist/ZenFM-koreader-android-$VERSION.zip"
[ -f "$APK" ]
[ -f "$PLUGIN" ]
unzip -Z1 "$PLUGIN" | grep -q '^zenfm\.koplugin/main\.lua$'
if unzip -Z1 "$PLUGIN" | grep -q 'zenfm\.koplugin/backend/zenfm-'; then
    echo "Android development plugin unexpectedly contains a native server" >&2
    exit 1
fi
grep -Fq "install -r $APK" "$ADB_LOG"
grep -Fxq 'shell mkdir -p /sdcard/koreader/plugins' "$ADB_LOG"
grep -Fq 'push ' "$ADB_LOG"
grep -Fq ' /sdcard/koreader/plugins/' "$ADB_LOG"

echo "ok - Android development build installs APK and KOReader plugin"
