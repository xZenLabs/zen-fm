#!/bin/sh
set -u

log_file=${1:-}
[ -n "$log_file" ] || { echo "ZenFM: missing log file" >&2; exit 2; }
shift
[ "$#" -gt 0 ] || { echo "ZenFM: missing command" >&2; exit 2; }

work_dir=${TMPDIR:-/tmp}/zenfm-log-prefix.$$
pipe=$work_dir/output
child=
prefixer=

cleanup() {
    trap - EXIT HUP INT TERM
    rm -f -- "$pipe"
    rmdir "$work_dir" 2>/dev/null || true
}

stop() {
    status=$1
    [ -z "$child" ] || kill "$child" 2>/dev/null || true
    [ -z "$prefixer" ] || kill "$prefixer" 2>/dev/null || true
    cleanup
    exit "$status"
}

trap cleanup EXIT
trap 'stop 129' HUP
trap 'stop 130' INT
trap 'stop 143' TERM

umask 077
mkdir "$work_dir" 2>/dev/null || { echo "ZenFM: could not create log-prefix directory" >&2; exit 1; }
mkfifo "$pipe" 2>/dev/null || { echo "ZenFM: could not create log-prefix pipe" >&2; exit 1; }

(
    line=
    while IFS= read -r line || [ -n "$line" ]; do
        timestamp=$(date '+%m/%d/%y-%H:%M:%S')
        if ! printf '%s ZenFM: %s\n' "$timestamp" "$line" >>"$log_file" 2>/dev/null; then
            echo "ZenFM: could not append backend output" >&2
            exit 1
        fi
        line=
    done <"$pipe"
) &
prefixer=$!

"$@" >"$pipe" 2>&1 &
child=$!
if wait "$child"; then command_status=0; else command_status=$?; fi
child=
if wait "$prefixer"; then prefix_status=0; else prefix_status=$?; fi
prefixer=

cleanup
if [ "$command_status" -ne 0 ]; then exit "$command_status"; fi
exit "$prefix_status"
