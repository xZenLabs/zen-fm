#!/bin/sh
set -eu

: "${ANDROID_NDK_HOME:?Set ANDROID_NDK_HOME to an Android NDK installation}"
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ROOT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
VERSION=$(sed -n '1p' "$ROOT_DIR/VERSION")
PREBUILT="$ANDROID_NDK_HOME/toolchains/llvm/prebuilt"
case "$(uname -s)-$(uname -m)" in
    Darwin-arm64)
        if [ -d "$PREBUILT/darwin-arm64" ]; then HOST=darwin-arm64; else HOST=darwin-x86_64; fi
        ;;
    Darwin-*) HOST=darwin-x86_64 ;;
    Linux-x86_64) HOST=linux-x86_64 ;;
    *) echo "Unsupported Android build host" >&2; exit 1 ;;
esac

build() {
    abi=$1
    goarch=$2
    goarm=$3
    compiler=$4
    output="$SCRIPT_DIR/app/src/main/jniLibs/$abi/libzenfm_exec.so"
    [ -x "$compiler" ] || { echo "Android NDK compiler not found: $compiler" >&2; exit 1; }
    mkdir -p "$(dirname "$output")"
    if [ -n "$goarm" ]; then
        (cd "$ROOT_DIR" && GOOS=android GOARCH="$goarch" GOARM="$goarm" CGO_ENABLED=1 CC="$compiler" \
            go build -buildmode=pie -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$output" ./cmd/zenfm)
    else
        (cd "$ROOT_DIR" && GOOS=android GOARCH="$goarch" CGO_ENABLED=1 CC="$compiler" \
            go build -buildmode=pie -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$output" ./cmd/zenfm)
    fi
}

build armeabi-v7a arm 7 "$PREBUILT/$HOST/bin/armv7a-linux-androideabi19-clang"
build arm64-v8a arm64 "" "$PREBUILT/$HOST/bin/aarch64-linux-android21-clang"
