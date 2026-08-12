#!/bin/sh
set -eu

SOURCE_ROOT=$1
WORK=$(mktemp -d "${TMPDIR:-/tmp}/zenfm-android-build.XXXXXX")
trap 'rm -rf "$WORK"' EXIT INT TERM

ANDROID_ROOT="$WORK/android"
BIN_DIR="$WORK/bin"
SDK_DIR="$WORK/sdk"
NDK_DIR="$SDK_DIR/ndk/25.2.9519653"
mkdir -p "$ANDROID_ROOT/app/build/outputs/stale-directory" \
    "$ANDROID_ROOT/app/src/main/jniLibs/stale-directory" "$BIN_DIR" "$NDK_DIR"
cp "$SOURCE_ROOT/android/build.sh" "$ANDROID_ROOT/build.sh"
cp "$SOURCE_ROOT/android/build-native.sh" "$ANDROID_ROOT/build-native.sh"
cp "$SOURCE_ROOT/VERSION" "$WORK/VERSION"
printf 'stale\n' > "$ANDROID_ROOT/app/build/outputs/.stale"
printf 'stale\n' > "$ANDROID_ROOT/app/src/main/jniLibs/.stale"

printf '%s\n' '#!/bin/sh' 'printf '\''openjdk version "17.0.1"\n'\'' >&2' > "$BIN_DIR/java"
printf '%s\n' '#!/bin/sh' \
    'if [ "${1:-}" = --version ]; then printf "Gradle 8.6\n"; exit 0; fi' \
    'project=' \
    'while [ "$#" -gt 0 ]; do' \
    '    if [ "$1" = -p ]; then project=$2; shift 2; else shift; fi' \
    'done' \
    '[ ! -e "$project/app/build/outputs/.stale" ]' \
    '[ ! -e "$project/app/build/outputs/stale-directory" ]' \
    '[ ! -e "$project/app/src/main/jniLibs/.stale" ]' \
    '[ ! -e "$project/app/src/main/jniLibs/stale-directory" ]' \
    'mkdir -p "$project/app/build/outputs/apk/release"' \
    ': > "$project/app/build/outputs/apk/release/app-release.apk"' > "$BIN_DIR/gradle"
chmod 700 "$BIN_DIR/java" "$BIN_DIR/gradle"

if PATH="$BIN_DIR:$PATH" ANDROID_HOME="$WORK/missing-sdk" ANDROID_NDK_HOME="$NDK_DIR" \
    GRADLE_BIN=gradle sh "$ANDROID_ROOT/build.sh" >"$WORK/preflight.log" 2>&1; then
    echo "Android build accepted a missing SDK" >&2
    exit 1
fi
[ ! -e "$ANDROID_ROOT/app/build/outputs/.stale" ]
[ ! -e "$ANDROID_ROOT/app/src/main/jniLibs/.stale" ]

mkdir -p "$ANDROID_ROOT/app/build/outputs/stale-directory" \
    "$ANDROID_ROOT/app/src/main/jniLibs/stale-directory"
printf 'stale\n' > "$ANDROID_ROOT/app/build/outputs/.stale"
printf 'stale\n' > "$ANDROID_ROOT/app/src/main/jniLibs/.stale"

PATH="$BIN_DIR:$PATH" ANDROID_HOME="$SDK_DIR" ANDROID_NDK_HOME="$NDK_DIR" \
    GRADLE_BIN=gradle sh "$ANDROID_ROOT/build.sh"
[ -f "$ANDROID_ROOT/app/build/outputs/apk/release/app-release.apk" ]

for host in darwin-arm64 darwin-x86_64 linux-x86_64; do
    mkdir -p "$NDK_DIR/toolchains/llvm/prebuilt/$host/bin"
    for compiler in armv7a-linux-androideabi19-clang aarch64-linux-android21-clang; do
        printf '#!/bin/sh\nexit 0\n' > "$NDK_DIR/toolchains/llvm/prebuilt/$host/bin/$compiler"
        chmod 700 "$NDK_DIR/toolchains/llvm/prebuilt/$host/bin/$compiler"
    done
done
printf '%s\n' '#!/bin/sh' \
    '[ ! -e "$EXPECTED_ANDROID_ROOT/app/src/main/jniLibs/.direct-stale" ]' \
    'output=' \
    'while [ "$#" -gt 0 ]; do' \
    '    if [ "$1" = -o ]; then output=$2; shift 2; else shift; fi' \
    'done' \
    '[ -n "$output" ]' \
    ': > "$output"' > "$BIN_DIR/go"
chmod 700 "$BIN_DIR/go"
mkdir -p "$ANDROID_ROOT/app/src/main/jniLibs/stale-directory"
printf 'stale\n' > "$ANDROID_ROOT/app/src/main/jniLibs/.direct-stale"
EXPECTED_ANDROID_ROOT="$ANDROID_ROOT" PATH="$BIN_DIR:$PATH" ANDROID_NDK_HOME="$NDK_DIR" \
    sh "$ANDROID_ROOT/build-native.sh"
[ -f "$ANDROID_ROOT/app/src/main/jniLibs/armeabi-v7a/libzenfm_exec.so" ]
[ -f "$ANDROID_ROOT/app/src/main/jniLibs/arm64-v8a/libzenfm_exec.so" ]
echo "ok - Android builds clear prior Gradle and native outputs"
