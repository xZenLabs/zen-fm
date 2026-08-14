#!/bin/sh
set -eu

ROOT=${1:-$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)}
command -v jq >/dev/null 2>&1 || { echo "jq is required for release evidence tests" >&2; exit 2; }

work=$(mktemp -d "${TMPDIR:-/tmp}/zenfm-evidence.XXXXXX")
cleanup() { rm -rf "$work"; }
trap cleanup EXIT INT TERM

printf 'qualified hard float\n' > "$work/zenfm-hf"
printf 'qualified soft float\n' > "$work/zenfm-sf"
chmod 700 "$work/zenfm-hf" "$work/zenfm-sf"

printf '%s\n' \
  '#!/bin/sh' \
  'set -eu' \
  'while [ "$#" -gt 0 ]; do' \
  '  case "$1" in' \
  '    --commit) commit=$2; shift 2 ;;' \
  '    --output) output=$2; shift 2 ;;' \
  '    *) shift 2 ;;' \
  '  esac' \
  'done' \
  'jq -n --arg commit "$commit" '\''{' \
  '  schema:"zenfm-old-kernel-qemu-v1", commit:$commit, result:"pass",' \
  '  toolchain:"go1.26.6-kindle", patchLevel:"ZenFM Linux/ARM old-kernel patch 1",' \
  '  kernelRelease:"2.6.35", checks:{hardFloat:"pass",softFloat:"pass",start:"pass",health:"pass",stop:"pass"}' \
  '}'\'' > "$output"' > "$work/qemu-harness"
chmod 700 "$work/qemu-harness"

commit=0123456789abcdef0123456789abcdef01234567
mkdir "$work/evidence"
ZENFM_OLD_KERNEL_QEMU_HARNESS="$work/qemu-harness" \
  sh "$ROOT/scripts/run-old-kernel-qemu-smoke.sh" \
  "$work/zenfm-hf" "$work/zenfm-sf" "$commit" "$work/evidence/old-kernel-qemu.json"

printf '%s\n' \
  '#!/bin/sh' \
  'set -eu' \
  'while [ "$#" -gt 0 ]; do' \
  '  case "$1" in' \
  '    --device) device=$2; shift 2 ;;' \
  '    --hard-float) hard=$2; shift 2 ;;' \
  '    --commit) commit=$2; shift 2 ;;' \
  '    --output) output=$2; shift 2 ;;' \
  '    *) shift 2 ;;' \
  '  esac' \
  'done' \
  'if command -v sha256sum >/dev/null 2>&1; then hash=$(sha256sum "$hard" | awk '\''{print $1}'\''); else hash=$(shasum -a 256 "$hard" | awk '\''{print $1}'\''); fi' \
  'jq -n --arg device "$device" --arg commit "$commit" --arg hash "$hash" '\''{' \
  '  schema:"zenfm-physical-kindle-v1", device:$device, commit:$commit, result:"pass",' \
  '  serialHash:("a"*64), binarySHA256:$hash, kernelRelease:"2.6.35",' \
  '  checks:{start:"pass",login:"pass",browse:"pass",upload:"pass",download:"pass",tlsFingerprint:"pass",stop:"pass"}' \
  '}'\'' > "$output"' > "$work/physical-harness"
chmod 700 "$work/physical-harness"

for device in kindle4 kindle5 paperwhite1; do
  ZENFM_PHYSICAL_KINDLE_HARNESS="$work/physical-harness" \
    sh "$ROOT/scripts/run-physical-kindle-smoke.sh" "$device" \
    "$work/zenfm-hf" "$work/zenfm-sf" "$commit" "$work/evidence/$device.json"
done

sh "$ROOT/scripts/verify-release-evidence.sh" "$commit" "$work/evidence"
sh "$ROOT/scripts/verify-qualified-binaries.sh" "$work/evidence" "$work"

printf 'tampered\n' >> "$work/zenfm-hf"
if sh "$ROOT/scripts/verify-qualified-binaries.sh" "$work/evidence" "$work" >/dev/null 2>&1; then
  echo "tampered release binary unexpectedly passed qualification binding" >&2
  exit 1
fi

echo "ok - qualification evidence is bound to exact release binaries"
