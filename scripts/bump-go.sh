#!/bin/sh
# Bump the pinned Go toolchain to the newest stable patch release of the
# current minor line (e.g. 1.26.x). go.mod is the single source of truth: this
# updates go.mod, frontend/go.mod, and the legacy ARM toolchain pin
# (VERSION + SHA256), after verifying the source archive checksum and that the
# old-kernel patch still applies.
#
# Usage: scripts/bump-go.sh [goX.Y.Z]
# Prints the new version on stdout when changed; prints nothing when current.
# Requires curl, jq, tar, patch, and sha256sum or shasum.
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
LEGACY="$ROOT/toolchains/legacy"

CURRENT=$(sed -n 's/^go \([0-9][0-9.]*\)$/\1/p' "$ROOT/go.mod")
MINOR=$(printf '%s\n' "$CURRENT" | cut -d. -f1-2)
case "$MINOR" in
    [0-9]*.[0-9]*) ;;
    *) echo "cannot parse go directive in go.mod: '$CURRENT'" >&2; exit 1 ;;
esac

RELEASES=$(curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
    'https://go.dev/dl/?mode=json&include=all')

TARGET=${1:-}
if [ -z "$TARGET" ]; then
    TARGET=$(printf '%s' "$RELEASES" | jq -r --arg p "go$MINOR." \
        '.[] | select(.stable and (.version | startswith($p))) | .version' |
        sort -V | tail -n 1)
fi
case "$TARGET" in
    go[0-9]*.[0-9]*.[0-9]*) ;;
    *) echo "no stable go$MINOR.x release found (got '$TARGET')" >&2; exit 1 ;;
esac

if [ "$TARGET" = "go$CURRENT" ]; then
    echo "already on latest $TARGET" >&2
    exit 0
fi
LATEST=$(printf '%s\n%s\n' "go$CURRENT" "$TARGET" | sort -V | tail -n 1)
[ "$LATEST" = "$TARGET" ] || { echo "refusing to downgrade go$CURRENT -> $TARGET" >&2; exit 1; }

ARCHIVE="$TARGET.src.tar.gz"
EXPECTED=$(printf '%s' "$RELEASES" | jq -r --arg v "$TARGET" --arg f "$ARCHIVE" \
    '.[] | select(.version == $v) | .files[] | select(.filename == $f and .kind == "source") | .sha256')
[ "${#EXPECTED}" -eq 64 ] || { echo "no published SHA-256 for $ARCHIVE" >&2; exit 1; }
case "$EXPECTED" in *[!0-9a-f]*) echo "malformed SHA-256 for $ARCHIVE" >&2; exit 1 ;; esac

WORK=$(mktemp -d "${TMPDIR:-/tmp}/zenfm-go-bump.XXXXXX")
trap 'rm -rf "$WORK"' EXIT INT TERM

curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
    --output "$WORK/$ARCHIVE" "https://go.dev/dl/$ARCHIVE"
if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL=$(sha256sum "$WORK/$ARCHIVE" | awk '{print $1}')
else
    ACTUAL=$(shasum -a 256 "$WORK/$ARCHIVE" | awk '{print $1}')
fi
[ "$ACTUAL" = "$EXPECTED" ] || {
    echo "checksum mismatch for $ARCHIVE: expected $EXPECTED, got $ACTUAL" >&2
    exit 1
}

tar -xzf "$WORK/$ARCHIVE" -C "$WORK"
patch --dry-run --silent -d "$WORK/go" -p1 < "$LEGACY/0001-linux-arm-use-epoll-wait.patch" || {
    echo "old-kernel patch no longer applies to $TARGET; update it manually" >&2
    exit 1
}

NEW=${TARGET#go}
for mod in "$ROOT/go.mod" "$ROOT/frontend/go.mod"; do
    sed "s/^go $CURRENT\$/go $NEW/" "$mod" > "$WORK/go.mod"
    mv "$WORK/go.mod" "$mod"
done
printf '%s\n' "$TARGET" > "$LEGACY/VERSION"
printf '%s  %s\n' "$EXPECTED" "$ARCHIVE" > "$LEGACY/SHA256"

sh "$ROOT/scripts/verify-legacy-toolchain.sh" >&2
printf '%s\n' "$TARGET"
