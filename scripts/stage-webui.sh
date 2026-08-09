#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
SOURCE=${1:-"$ROOT/frontend/dist"}
TARGET="$ROOT/internal/webui/dist"
PARENT=$(dirname "$TARGET")

[ -f "$SOURCE/index.html" ] || {
    echo "frontend build is missing $SOURCE/index.html" >&2
    exit 2
}

mkdir -p "$PARENT"
NEW=$(mktemp -d "$PARENT/.dist-new.XXXXXX")
OLD="$PARENT/.dist-old.$$"
cleanup() {
    rm -rf "$NEW" "$OLD"
}
trap cleanup EXIT INT TERM

cp -R "$SOURCE/." "$NEW/"
[ -f "$NEW/index.html" ] || { echo "staged frontend has no index.html" >&2; exit 2; }

if [ -e "$TARGET" ]; then
    [ ! -e "$OLD" ] || { echo "temporary frontend path already exists" >&2; exit 2; }
    mv "$TARGET" "$OLD"
fi
if ! mv "$NEW" "$TARGET"; then
    [ ! -e "$OLD" ] || mv "$OLD" "$TARGET"
    exit 1
fi
rm -rf "$OLD"
trap - EXIT INT TERM

