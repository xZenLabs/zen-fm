#!/bin/sh
set -eu

ROOT=$1
TMP=$(mktemp -d "${TMPDIR:-/tmp}/zenfm-release-notes.XXXXXX")
trap 'rm -rf "$TMP"' EXIT INT TERM

VERSION=$(sed -n '1p' "$ROOT/VERSION")
BASE_VERSION=$(printf '%s\n' "$VERSION" | sed 's/-beta[0-9][0-9]*$//')
STABLE="$TMP/stable.md"
BETA="$TMP/beta.md"

python3 "$ROOT/.github/scripts/build_release_notes.py" "$BASE_VERSION" > "$STABLE"
grep -Fxq "## What's Changed" "$STABLE"
grep -Eq '^- .+' "$STABLE"
if grep -Fq '_No changelog entries for this version._' "$STABLE"; then
    echo "current version is missing from the Lua changelog" >&2
    exit 1
fi

python3 "$ROOT/.github/scripts/build_release_notes.py" "$BASE_VERSION-beta99" > "$BETA"
cmp "$STABLE" "$BETA"

python3 "$ROOT/.github/scripts/build_release_notes.py" 99.99.99 > "$TMP/missing.md"
grep -Fxq '_No changelog entries for this version._' "$TMP/missing.md"

for workflow in release.yml release-beta.yml; do
    path="$ROOT/.github/workflows/$workflow"
    grep -Fq 'python3 .github/scripts/build_release_notes.py' "$path"
    grep -Fq -- '--notes-file "$RUNNER_TEMP/zenfm-release-notes.md"' "$path"
    if grep -Fq -- '--generate-notes' "$path"; then
        echo "$workflow must use notes rendered from the Lua changelog" >&2
        exit 1
    fi
done

echo "ok - Lua changelog renders and supplies stable and beta release notes"
