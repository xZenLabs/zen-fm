#!/bin/sh
set -eu

[ "$#" -eq 2 ] || { echo "usage: verify-qualified-binaries.sh EVIDENCE_DIR BINARY_DIR" >&2; exit 2; }
EVIDENCE_DIR=$1
BINARY_DIR=$2
QEMU="$EVIDENCE_DIR/old-kernel-qemu.json"

command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 2; }
[ -f "$QEMU" ] || { echo "old-kernel QEMU evidence is missing" >&2; exit 1; }
[ -f "$BINARY_DIR/zenfm-hf" ] || { echo "release hard-float binary is missing" >&2; exit 1; }
[ -f "$BINARY_DIR/zenfm-sf" ] || { echo "release soft-float binary is missing" >&2; exit 1; }

sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

expected_hard=$(jq -er '.builds.hardFloatSHA256 | select(test("^[0-9a-f]{64}$"))' "$QEMU")
expected_soft=$(jq -er '.builds.softFloatSHA256 | select(test("^[0-9a-f]{64}$"))' "$QEMU")
actual_hard=$(sha256_file "$BINARY_DIR/zenfm-hf")
actual_soft=$(sha256_file "$BINARY_DIR/zenfm-sf")

[ "$actual_hard" = "$expected_hard" ] || {
    echo "release hard-float binary differs from the qualified binary" >&2
    exit 1
}
[ "$actual_soft" = "$expected_soft" ] || {
    echo "release soft-float binary differs from the qualified binary" >&2
    exit 1
}

echo "ok - release legacy ARM binaries exactly match qualified artifacts"
