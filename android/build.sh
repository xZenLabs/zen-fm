#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
GRADLE_BIN=${GRADLE_BIN:-gradle}

command -v java >/dev/null 2>&1 || { echo "Java 17 is required" >&2; exit 2; }
java_version=$(java -version 2>&1 | sed -n '1{s/.*version "\([^"]*\)".*/\1/p;}')
case "$java_version" in 17|17.*) ;; *) echo "Java 17 is required; found ${java_version:-unknown}" >&2; exit 2 ;; esac
command -v "$GRADLE_BIN" >/dev/null 2>&1 || { echo "Gradle 8.6 is required" >&2; exit 2; }
gradle_version=$($GRADLE_BIN --version 2>/dev/null | sed -n 's/^Gradle \([0-9][0-9.]*\)$/\1/p')
[ "$gradle_version" = "8.6" ] || { echo "Gradle 8.6 is required; found ${gradle_version:-unknown}" >&2; exit 2; }
android_home=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}
[ -n "$android_home" ] && [ -d "$android_home" ] || { echo "Set ANDROID_HOME to Android SDK 34" >&2; exit 2; }
export ANDROID_HOME=$android_home
if [ -z "${ANDROID_NDK_HOME:-}" ]; then
    ANDROID_NDK_HOME="$ANDROID_HOME/ndk/25.2.9519653"
fi
[ -d "$ANDROID_NDK_HOME" ] || { echo "Set ANDROID_NDK_HOME to Android NDK 25.2.9519653" >&2; exit 2; }
export ANDROID_NDK_HOME
exec "$GRADLE_BIN" -p "$SCRIPT_DIR" assembleRelease "$@"
