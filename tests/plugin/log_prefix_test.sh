#!/bin/sh
set -eu

ROOT=$1
TMP=$(mktemp -d "${TMPDIR:-/tmp}/zenfm-log-prefix-test.XXXXXX")
trap 'rm -rf "$TMP"' EXIT INT TERM
mkdir "$TMP/bin"
printf '%s\n' '#!/bin/sh' \
    '[ "$#" -eq 1 ] && [ "$1" = "+%m/%d/%y-%H:%M:%S" ] || exit 1' \
    'printf "08/14/26-12:48:18\\n"' >"$TMP/bin/date"
chmod +x "$TMP/bin/date"

if PATH="$TMP/bin:$PATH" sh "$ROOT/plugin/zenfm.koplugin/log-prefix.sh" "$TMP/zenfm.log" \
    sh -c 'printf "started\n"; printf "failed without newline" >&2; exit 7'; then
    echo "log prefix wrapper lost the command failure" >&2
    exit 1
else
    status=$?
fi

[ "$status" -eq 7 ]
expected=$(printf '08/14/26-12:48:18 ZenFM: started\n08/14/26-12:48:18 ZenFM: failed without newline')
actual=$(cat "$TMP/zenfm.log")
[ "$actual" = "$expected" ]

echo "ok - log timestamps and prefixes preserve output and exit status"
