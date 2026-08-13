#!/bin/sh
set -eu

ROOT=$1
TMP=$(mktemp -d "${TMPDIR:-/tmp}/zenfm-supervisor.XXXXXX")
trap 'rm -rf "$TMP"' EXIT INT TERM

printf '%s\n' '#!/bin/sh' 'printf "%s\n" "$*" >> "$ZENFM_FIREWALL_LOG"' > "$TMP/iptables"
printf '%s\n' '#!/bin/sh' '[ -z "${ZENFM_BACKEND_LOG:-}" ] || printf "%s\\n" "$$" >> "$ZENFM_BACKEND_LOG"' 'exec /bin/sleep "${ZENFM_BACKEND_SLEEP:-0.05}"' > "$TMP/backend"
printf '%s\n' '#!/bin/sh' 'case "$1" in *.*) echo "sleep: invalid number '\''$1'\''" >&2; exit 1 ;; esac' 'exec /bin/sleep "$@"' > "$TMP/sleep"
printf '%s\n' '#!/bin/sh' 'printf "%s\\n" "$1" >> "$ZENFM_USLEEP_LOG"' > "$TMP/usleep"
chmod +x "$TMP/iptables" "$TMP/backend" "$ROOT/plugin/zenfm.koplugin/supervisor.sh"
chmod +x "$TMP/sleep" "$TMP/usleep"
runtime_id=$(basename "$TMP" | tr -cd '[:alnum:]' | awk '{ value=$0; if (length(value) > 12) value=substr(value, length(value)-11); print value }')
chain_8443=ZFM${runtime_id}8443
chain_9443=ZFM${runtime_id}9443

ZENFM_IPTABLES="$TMP/iptables" ZENFM_FIREWALL_LOG="$TMP/firewall.log" \
    ZENFM_USLEEP_LOG="$TMP/usleep.log" PATH="$TMP:$PATH" \
    "$ROOT/plugin/zenfm.koplugin/supervisor.sh" \
    --pid-file "$TMP/server.pid" --socket-file "$TMP/server.sock" --port 8443 --kindle -- "$TMP/backend"

[ ! -e "$TMP/server.pid" ]
[ ! -e "$TMP/server.sock" ]
grep -q -- "-N $chain_8443" "$TMP/firewall.log"
grep -q -- "-A $chain_8443 -p tcp --dport 8443 -j ACCEPT" "$TMP/firewall.log"
grep -q -- "-I INPUT -j $chain_8443" "$TMP/firewall.log"
grep -q -- "-D INPUT -j $chain_8443" "$TMP/firewall.log"
grep -q -- "-X $chain_8443" "$TMP/firewall.log"
grep -qx '10000' "$TMP/usleep.log"

mkdir "$TMP/install-one" "$TMP/install-two"
: > "$TMP/firewall-installs.log"
for install in install-one install-two; do
    ZENFM_IPTABLES="$TMP/iptables" ZENFM_FIREWALL_LOG="$TMP/firewall-installs.log" \
        "$ROOT/plugin/zenfm.koplugin/supervisor.sh" \
        --pid-file "$TMP/$install/server.pid" --socket-file "$TMP/$install/server.sock" \
        --port 8443 --kindle -- "$TMP/backend"
done
install_one_id=$(printf '%s' install-one | tr -cd '[:alnum:]' | awk '{ value=$0; if (length(value) > 12) value=substr(value, length(value)-11); print value }')
install_two_id=$(printf '%s' install-two | tr -cd '[:alnum:]' | awk '{ value=$0; if (length(value) > 12) value=substr(value, length(value)-11); print value }')
install_one_chain=ZFM${install_one_id}8443
install_two_chain=ZFM${install_two_id}8443
[ "$install_one_chain" != "$install_two_chain" ]
grep -q -- "-N $install_one_chain" "$TMP/firewall-installs.log"
grep -q -- "-N $install_two_chain" "$TMP/firewall-installs.log"

# An incomplete ownership record with a live owner represents the narrow
# mkdir-to-metadata initialization window. It must fail closed, not be removed.
mkdir "$TMP/server.pid.lock"
if ZENFM_USLEEP_LOG="$TMP/usleep.log" PATH="$TMP:$PATH" \
    "$ROOT/plugin/zenfm.koplugin/supervisor.sh" \
    --pid-file "$TMP/server.pid" --socket-file "$TMP/server.sock" --port 8443 -- "$TMP/backend" 2>/dev/null; then
    echo "supervisor recovered an ownerless incomplete lock" >&2
    exit 1
fi
[ -d "$TMP/server.pid.lock" ]
rmdir "$TMP/server.pid.lock"

sleep 5 &
initializer=$!
mkdir "$TMP/server.pid.lock"
printf '%s\n' "$initializer" > "$TMP/server.pid.lock/owner"
if ZENFM_USLEEP_LOG="$TMP/usleep.log" PATH="$TMP:$PATH" \
    "$ROOT/plugin/zenfm.koplugin/supervisor.sh" \
    --pid-file "$TMP/server.pid" --socket-file "$TMP/server.sock" --port 8443 -- "$TMP/backend" 2>/dev/null; then
    echo "supervisor recovered a live incomplete lock" >&2
    kill "$initializer" 2>/dev/null || true
    wait "$initializer" 2>/dev/null || true
    exit 1
fi
[ "$(sed -n '1p' "$TMP/server.pid.lock/owner")" = "$initializer" ]
kill "$initializer" 2>/dev/null || true
wait "$initializer" 2>/dev/null || true
rm -f "$TMP/server.pid.lock/owner"
rmdir "$TMP/server.pid.lock"

# Truly simultaneous starters must launch exactly one backend.
: > "$TMP/backend-race.log"
ZENFM_BACKEND_SLEEP=0.25 ZENFM_BACKEND_LOG="$TMP/backend-race.log" \
    "$ROOT/plugin/zenfm.koplugin/supervisor.sh" \
    --pid-file "$TMP/race.pid" --socket-file "$TMP/race.sock" --port 8443 -- "$TMP/backend" &
race_one=$!
ZENFM_BACKEND_SLEEP=0.25 ZENFM_BACKEND_LOG="$TMP/backend-race.log" \
    "$ROOT/plugin/zenfm.koplugin/supervisor.sh" \
    --pid-file "$TMP/race.pid" --socket-file "$TMP/race.sock" --port 8443 -- "$TMP/backend" &
race_two=$!
if wait "$race_one"; then race_one_status=0; else race_one_status=$?; fi
if wait "$race_two"; then race_two_status=0; else race_two_status=$?; fi
case "$race_one_status:$race_two_status" in 0:1|1:0) ;; *)
    echo "simultaneous supervisors returned unexpected statuses $race_one_status/$race_two_status" >&2
    exit 1
esac
[ "$(wc -l < "$TMP/backend-race.log" | tr -d ' ')" -eq 1 ]

ZENFM_BACKEND_SLEEP=1 "$ROOT/plugin/zenfm.koplugin/supervisor.sh" \
    --pid-file "$TMP/server.pid" --socket-file "$TMP/server.sock" --port 8443 -- "$TMP/backend" &
first=$!
attempt=0
while [ ! -s "$TMP/server.pid" ] && [ "$attempt" -lt 50 ]; do sleep 0.02; attempt=$((attempt + 1)); done
[ -s "$TMP/server.pid" ]
: > "$TMP/server.sock"
if "$ROOT/plugin/zenfm.koplugin/supervisor.sh" \
    --pid-file "$TMP/server.pid" --socket-file "$TMP/server.sock" --port 8443 -- "$TMP/backend" 2>/dev/null; then
    echo "second supervisor unexpectedly started" >&2
    kill "$first" 2>/dev/null || true
    wait "$first" 2>/dev/null || true
    exit 1
fi
[ -s "$TMP/server.pid" ]
[ -e "$TMP/server.sock" ]
wait "$first"
[ ! -e "$TMP/server.pid" ]
[ ! -e "$TMP/server.sock" ]

if [ ! -r "/proc/$$/stat" ] && ! ps -p "$$" -o lstart= >/dev/null 2>&1; then
    echo "ok - supervisor cleanup (orphan identity recovery requires /proc or ps)"
    exit 0
fi

ZENFM_BACKEND_SLEEP=30 ZENFM_BACKEND_LOG="$TMP/backend-crash.log" ZENFM_IPTABLES="$TMP/iptables" ZENFM_FIREWALL_LOG="$TMP/firewall-crash.log" \
    "$ROOT/plugin/zenfm.koplugin/supervisor.sh" \
    --pid-file "$TMP/server.pid" --socket-file "$TMP/server.sock" --port 9443 --kindle -- "$TMP/backend" &
crashed=$!
attempt=0
while [ ! -s "$TMP/server.pid" ] && [ "$attempt" -lt 50 ]; do sleep 0.02; attempt=$((attempt + 1)); done
[ -s "$TMP/server.pid" ]
attempt=0
while [ ! -s "$TMP/server.pid.lock/child.pid" ] && [ "$attempt" -lt 50 ]; do sleep 0.02; attempt=$((attempt + 1)); done
[ -s "$TMP/server.pid.lock/child.pid" ]
old_child=$(sed -n '1p' "$TMP/server.pid.lock/child.pid")
kill -0 "$old_child"
kill -9 "$crashed"
wait "$crashed" 2>/dev/null || true
ZENFM_BACKEND_SLEEP=0.05 ZENFM_BACKEND_LOG="$TMP/backend-crash.log" ZENFM_IPTABLES="$TMP/iptables" ZENFM_FIREWALL_LOG="$TMP/firewall-crash.log" \
    "$ROOT/plugin/zenfm.koplugin/supervisor.sh" \
    --pid-file "$TMP/server.pid" --socket-file "$TMP/server.sock" --port 9443 --kindle -- "$TMP/backend"
if kill -0 "$old_child" 2>/dev/null; then
    echo "orphaned backend survived supervisor recovery" >&2
    exit 1
fi
[ "$(wc -l < "$TMP/backend-crash.log" | tr -d ' ')" -eq 2 ]
recovery_line=$(grep -n -- "-D INPUT -j $chain_9443" "$TMP/firewall-crash.log" | sed -n '2p' | cut -d: -f1)
install_line=$(grep -n -- "-N $chain_9443" "$TMP/firewall-crash.log" | sed -n '2p' | cut -d: -f1)
[ "$recovery_line" -lt "$install_line" ]
echo "ok - supervisor cleanup"
