#!/bin/sh
set -eu

[ "$#" -eq 4 ] || {
    echo "usage: run-old-kernel-qemu-smoke.sh HARD_FLOAT SOFT_FLOAT COMMIT OUTPUT" >&2
    exit 2
}
: "${ZENFM_OLD_KERNEL_QEMU_HARNESS:?Set ZENFM_OLD_KERNEL_QEMU_HARNESS on the qualified runner}"

HARD_FLOAT=$1
SOFT_FLOAT=$2
COMMIT=$3
OUTPUT=$4

[ -x "$ZENFM_OLD_KERNEL_QEMU_HARNESS" ] || { echo "QEMU harness is not executable" >&2; exit 2; }
[ -x "$HARD_FLOAT" ] || { echo "hard-float backend is missing" >&2; exit 2; }
[ -x "$SOFT_FLOAT" ] || { echo "soft-float backend is missing" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "jq is required on the qualification runner" >&2; exit 2; }

sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

"$ZENFM_OLD_KERNEL_QEMU_HARNESS" \
    --hard-float "$HARD_FLOAT" \
    --soft-float "$SOFT_FLOAT" \
    --commit "$COMMIT" \
    --output "$OUTPUT"

jq -e --arg commit "$COMMIT" '
  .schema == "zenfm-old-kernel-qemu-v1" and
  .commit == $commit and
  .result == "pass" and
  .toolchain == "go1.26.6-kindle" and
  .patchLevel == "ZenFM Linux/ARM old-kernel patch 1" and
  (.kernelRelease | type == "string" and test("^2\\.6\\.")) and
  .checks.hardFloat == "pass" and
  .checks.softFloat == "pass" and
  .checks.start == "pass" and
  .checks.health == "pass" and
  .checks.stop == "pass"
' "$OUTPUT" >/dev/null

hard_sha=$(sha256_file "$HARD_FLOAT")
soft_sha=$(sha256_file "$SOFT_FLOAT")
tmp="$OUTPUT.qualified.$$"
trap 'rm -f "$tmp"' EXIT INT TERM
jq --arg hard "$hard_sha" --arg soft "$soft_sha" '
  .schema = "zenfm-old-kernel-qemu-v2" |
  .builds = {
    hardFloatSHA256: $hard,
    softFloatSHA256: $soft
  }
' "$OUTPUT" > "$tmp"
mv "$tmp" "$OUTPUT"
trap - EXIT INT TERM

jq -e --arg hard "$hard_sha" --arg soft "$soft_sha" '
  .schema == "zenfm-old-kernel-qemu-v2" and
  .builds.hardFloatSHA256 == $hard and
  .builds.softFloatSHA256 == $soft
' "$OUTPUT" >/dev/null
