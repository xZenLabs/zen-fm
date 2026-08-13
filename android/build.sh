#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
OUTPUT_DIR="$SCRIPT_DIR/app/build/outputs"
NATIVE_DIR="$SCRIPT_DIR/app/src/main/jniLibs"

rm -rf "$OUTPUT_DIR" "$NATIVE_DIR"

if [ -z "${JAVA_HOME:-}" ] && command -v java >/dev/null 2>&1; then
    JAVA_HOME=$(java -XshowSettings:properties -version 2>&1 |
        sed -n 's/^[[:space:]]*java\.home = //p' | sed -n '1p')
fi
if [ -n "${JAVA_HOME:-}" ]; then
    [ -x "$JAVA_HOME/bin/java" ] || { echo "JAVA_HOME does not contain Java 17: $JAVA_HOME" >&2; exit 2; }
    PATH="$JAVA_HOME/bin:$PATH"
    export JAVA_HOME PATH
fi
command -v java >/dev/null 2>&1 || { echo "Java 17 is required" >&2; exit 2; }
java_version=$(java -version 2>&1 | sed -n '1{s/.*version "\([^"]*\)".*/\1/p;}')
case "$java_version" in 17|17.*) ;; *) echo "Java 17 is required; found ${java_version:-unknown}" >&2; exit 2 ;; esac

if [ -z "${GRADLE_BIN:-}" ]; then
    if [ -n "${HOME:-}" ] && [ -x "$HOME/.sdkman/candidates/gradle/8.6/bin/gradle" ]; then
        GRADLE_BIN="$HOME/.sdkman/candidates/gradle/8.6/bin/gradle"
    elif [ -x /opt/homebrew/opt/gradle@8.6/bin/gradle ]; then
        GRADLE_BIN=/opt/homebrew/opt/gradle@8.6/bin/gradle
    elif [ -x /usr/local/opt/gradle@8.6/bin/gradle ]; then
        GRADLE_BIN=/usr/local/opt/gradle@8.6/bin/gradle
    else
        GRADLE_BIN=gradle
    fi
fi
export GRADLE_BIN
command -v "$GRADLE_BIN" >/dev/null 2>&1 || { echo "Gradle 8.6 is required" >&2; exit 2; }
gradle_version=$("$GRADLE_BIN" --version 2>/dev/null | sed -n 's/^Gradle \([0-9][0-9.]*\)$/\1/p')
[ "$gradle_version" = "8.6" ] || { echo "Gradle 8.6 is required; found ${gradle_version:-unknown}" >&2; exit 2; }

android_home=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}
if [ -z "$android_home" ] && [ -n "${HOME:-}" ]; then
    for candidate in "$HOME/Library/Android/sdk" "$HOME/Android/Sdk"; do
        if [ -d "$candidate" ]; then
            android_home=$candidate
            break
        fi
    done
fi
if [ -z "$android_home" ]; then
    for candidate in /opt/homebrew/share/android-commandlinetools /usr/local/share/android-sdk; do
        if [ -d "$candidate" ]; then
            android_home=$candidate
            break
        fi
    done
fi
[ -n "$android_home" ] && [ -d "$android_home" ] || { echo "Set ANDROID_HOME to Android SDK 34" >&2; exit 2; }
ANDROID_HOME=$android_home
ANDROID_SDK_ROOT=$android_home
export ANDROID_HOME ANDROID_SDK_ROOT
if [ -z "${ANDROID_NDK_HOME:-}" ] && [ -n "${ANDROID_NDK_ROOT:-}" ]; then
    ANDROID_NDK_HOME=$ANDROID_NDK_ROOT
elif [ -z "${ANDROID_NDK_HOME:-}" ]; then
    ANDROID_NDK_HOME="$ANDROID_HOME/ndk/25.2.9519653"
fi
[ -d "$ANDROID_NDK_HOME" ] || { echo "Set ANDROID_NDK_HOME to Android NDK 25.2.9519653" >&2; exit 2; }
ANDROID_NDK_ROOT=$ANDROID_NDK_HOME
export ANDROID_NDK_HOME ANDROID_NDK_ROOT
exec "$GRADLE_BIN" -p "$SCRIPT_DIR" assembleRelease "$@"
