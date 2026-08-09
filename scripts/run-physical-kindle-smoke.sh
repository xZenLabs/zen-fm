#!/bin/sh
set -eu

[ "$#" -eq 5 ] || {
    echo "usage: run-physical-kindle-smoke.sh DEVICE HARD_FLOAT SOFT_FLOAT COMMIT OUTPUT" >&2
    exit 2
}
: "${ZENFM_PHYSICAL_KINDLE_HARNESS:?Set ZENFM_PHYSICAL_KINDLE_HARNESS on the hardware runner}"

DEVICE=$1
HARD_FLOAT=$2
SOFT_FLOAT=$3
COMMIT=$4
OUTPUT=$5

case "$DEVICE" in kindle4|kindle5|paperwhite1) ;; *) echo "unsupported attestation device: $DEVICE" >&2; exit 2 ;; esac
[ -x "$ZENFM_PHYSICAL_KINDLE_HARNESS" ] || { echo "physical Kindle harness is not executable" >&2; exit 2; }
[ -x "$HARD_FLOAT" ] || { echo "hard-float backend is missing" >&2; exit 2; }
[ -x "$SOFT_FLOAT" ] || { echo "soft-float backend is missing" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "jq is required on the hardware runner" >&2; exit 2; }

sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

"$ZENFM_PHYSICAL_KINDLE_HARNESS" \
    --device "$DEVICE" \
    --hard-float "$HARD_FLOAT" \
    --soft-float "$SOFT_FLOAT" \
    --commit "$COMMIT" \
    --output "$OUTPUT"

hard_sha=$(sha256_file "$HARD_FLOAT")
soft_sha=$(sha256_file "$SOFT_FLOAT")

jq -e --arg device "$DEVICE" --arg commit "$COMMIT" \
  --arg hard "$hard_sha" --arg soft "$soft_sha" '
  .schema == "zenfm-physical-kindle-v1" and
  .device == $device and
  .commit == $commit and
  .result == "pass" and
  (.serialHash | type == "string" and test("^[0-9a-f]{64}$")) and
  (.binarySHA256 == $hard or .binarySHA256 == $soft) and
  (.kernelRelease | type == "string" and length > 0) and
  .checks.start == "pass" and
  .checks.login == "pass" and
  .checks.browse == "pass" and
  .checks.upload == "pass" and
  .checks.download == "pass" and
  .checks.tlsFingerprint == "pass" and
  .checks.stop == "pass"
' "$OUTPUT" >/dev/null

tmp="$OUTPUT.qualified.$$"
trap 'rm -f "$tmp"' EXIT INT TERM
jq --arg hard "$hard_sha" --arg soft "$soft_sha" '
  .schema = "zenfm-physical-kindle-v2" |
  .builds = {
    hardFloatSHA256: $hard,
    softFloatSHA256: $soft
  }
' "$OUTPUT" > "$tmp"
mv "$tmp" "$OUTPUT"
trap - EXIT INT TERM

jq -e --arg hard "$hard_sha" --arg soft "$soft_sha" '
  .schema == "zenfm-physical-kindle-v2" and
  .builds.hardFloatSHA256 == $hard and
  .builds.softFloatSHA256 == $soft and
  (.binarySHA256 == $hard or .binarySHA256 == $soft)
' "$OUTPUT" >/dev/null
