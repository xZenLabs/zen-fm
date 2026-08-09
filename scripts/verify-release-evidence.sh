#!/bin/sh
set -eu

[ "$#" -eq 2 ] || { echo "usage: verify-release-evidence.sh COMMIT EVIDENCE_DIR" >&2; exit 2; }
COMMIT=$1
DIR=$2
command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 2; }

ACTUAL=$(find "$DIR" -maxdepth 1 -type f -print | sed 's!.*/!!' | sort)
EXPECTED=$(printf '%s\n' kindle4.json kindle5.json old-kernel-qemu.json paperwhite1.json | sort)
[ "$ACTUAL" = "$EXPECTED" ] || {
    echo "qualification evidence contains missing or unexpected files" >&2
    printf 'expected:\n%s\nactual:\n%s\n' "$EXPECTED" "$ACTUAL" >&2
    exit 1
}

QEMU="$DIR/old-kernel-qemu.json"
[ -f "$QEMU" ] || { echo "old-kernel QEMU evidence is missing" >&2; exit 1; }
jq -e --arg commit "$COMMIT" '
  .schema == "zenfm-old-kernel-qemu-v2" and .commit == $commit and .result == "pass" and
  .toolchain == "go1.26.5-kindle" and .patchLevel == "ZenFM Linux/ARM old-kernel patch 1" and
  (.kernelRelease | test("^2\\.6\\.")) and
  (.builds.hardFloatSHA256 | type == "string" and test("^[0-9a-f]{64}$")) and
  (.builds.softFloatSHA256 | type == "string" and test("^[0-9a-f]{64}$")) and
  .checks.hardFloat == "pass" and .checks.softFloat == "pass" and
  .checks.start == "pass" and .checks.health == "pass" and .checks.stop == "pass"
' "$QEMU" >/dev/null

hard_sha=$(jq -r '.builds.hardFloatSHA256' "$QEMU")
soft_sha=$(jq -r '.builds.softFloatSHA256' "$QEMU")

for device in kindle4 kindle5 paperwhite1; do
    evidence="$DIR/$device.json"
    [ -f "$evidence" ] || { echo "$device evidence is missing" >&2; exit 1; }
    jq -e --arg commit "$COMMIT" --arg device "$device" \
      --arg hard "$hard_sha" --arg soft "$soft_sha" '
      .schema == "zenfm-physical-kindle-v2" and .commit == $commit and .device == $device and
      .result == "pass" and (.serialHash | test("^[0-9a-f]{64}$")) and
      .builds.hardFloatSHA256 == $hard and .builds.softFloatSHA256 == $soft and
      (.binarySHA256 == $hard or .binarySHA256 == $soft) and (.kernelRelease | length > 0) and
      .checks.start == "pass" and .checks.login == "pass" and .checks.browse == "pass" and
      .checks.upload == "pass" and .checks.download == "pass" and
      .checks.tlsFingerprint == "pass" and .checks.stop == "pass"
    ' "$evidence" >/dev/null
done

echo "ok - QEMU, Kindle 4, Kindle 5, and Paperwhite 1 evidence matches $COMMIT"
