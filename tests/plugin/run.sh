#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
lua "$ROOT/tests/plugin/daemon_test.lua" "$ROOT"
python3 "$ROOT/tests/translation_utils_test.py"
"$ROOT/tests/plugin/supervisor_test.sh" "$ROOT"
"$ROOT/tests/plugin/package_test.sh" "$ROOT"
"$ROOT/tests/android_build_test.sh" "$ROOT"
"$ROOT/tests/android_dev_deploy_test.sh" "$ROOT"
"$ROOT/tests/release_notes_test.sh" "$ROOT"
"$ROOT/tests/release_evidence_test.sh" "$ROOT"
