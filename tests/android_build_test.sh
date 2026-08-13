#!/bin/sh
set -eu

SOURCE_ROOT=$1
WORK=$(mktemp -d "${TMPDIR:-/tmp}/zenfm-android-build.XXXXXX")
trap 'rm -rf "$WORK"' EXIT INT TERM
unset JAVA_HOME ANDROID_HOME ANDROID_SDK_ROOT ANDROID_NDK_HOME ANDROID_NDK_ROOT GRADLE_BIN

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

printf '%s\n' '#!/bin/sh' \
    'if [ "${1:-}" = -XshowSettings:properties ]; then' \
    '    printf "    java.home = %s\n" "$TEST_JAVA_HOME" >&2' \
    '    exit 0' \
    'fi' \
    'printf '\''openjdk version "17.0.1"\n'\'' >&2' > "$BIN_DIR/java"
printf '%s\n' '#!/bin/sh' \
    'if [ "${1:-}" = --version ]; then printf "Gradle 8.6\n"; exit 0; fi' \
    'if [ -n "${EXPECTED_ANDROID_HOME:-}" ]; then' \
    '    [ "$ANDROID_HOME" = "$EXPECTED_ANDROID_HOME" ]' \
    '    [ "$ANDROID_SDK_ROOT" = "$EXPECTED_ANDROID_HOME" ]' \
    '    [ "$ANDROID_NDK_HOME" = "$EXPECTED_ANDROID_NDK_HOME" ]' \
    '    [ "$ANDROID_NDK_ROOT" = "$EXPECTED_ANDROID_NDK_HOME" ]' \
    '    [ "$JAVA_HOME" = "$TEST_JAVA_HOME" ]' \
    '    [ "$GRADLE_BIN" = "$EXPECTED_GRADLE_BIN" ]' \
    'fi' \
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
TEST_JAVA_HOME=$WORK
export TEST_JAVA_HOME

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
    GRADLE_BIN=gradle EXPECTED_ANDROID_HOME="$SDK_DIR" EXPECTED_ANDROID_NDK_HOME="$NDK_DIR" \
    EXPECTED_GRADLE_BIN=gradle sh "$ANDROID_ROOT/build.sh"
[ -f "$ANDROID_ROOT/app/build/outputs/apk/release/app-release.apk" ]

AUTO_HOME="$WORK/home"
AUTO_SDK="$AUTO_HOME/Library/Android/sdk"
AUTO_NDK="$AUTO_SDK/ndk/25.2.9519653"
AUTO_GRADLE="$AUTO_HOME/.sdkman/candidates/gradle/8.6/bin/gradle"
mkdir -p "$AUTO_NDK" "$(dirname "$AUTO_GRADLE")"
cp "$BIN_DIR/gradle" "$AUTO_GRADLE"
chmod 700 "$AUTO_GRADLE"
printf '%s\n' '#!/bin/sh' 'printf "Gradle 9.6.1\n"' > "$BIN_DIR/gradle"
chmod 700 "$BIN_DIR/gradle"
HOME="$AUTO_HOME" PATH="$BIN_DIR:$PATH" EXPECTED_ANDROID_HOME="$AUTO_SDK" \
    EXPECTED_ANDROID_NDK_HOME="$AUTO_NDK" EXPECTED_GRADLE_BIN="$AUTO_GRADLE" \
    sh "$ANDROID_ROOT/build.sh"
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
