#!/bin/sh
set -eu

pid_file=
socket_file=
port=
kindle=0

while [ "$#" -gt 0 ]; do
    case "$1" in
        --pid-file) pid_file=$2; shift 2 ;;
        --socket-file) socket_file=$2; shift 2 ;;
        --port) port=$2; shift 2 ;;
        --kindle) kindle=1; shift ;;
        --) shift; break ;;
        *) echo "unknown supervisor option: $1" >&2; exit 2 ;;
    esac
done

case "$pid_file:$socket_file:$port" in
    /*:/*:[1-9]|/*:/*:[1-9][0-9]|/*:/*:[1-9][0-9][0-9]|/*:/*:[1-9][0-9][0-9][0-9]|/*:/*:[1-5][0-9][0-9][0-9][0-9]|/*:/*:6[0-4][0-9][0-9][0-9]|/*:/*:65[0-4][0-9][0-9]|/*:/*:655[0-2][0-9]|/*:/*:6553[0-5]) ;;
    *) echo "invalid supervisor paths or port" >&2; exit 2 ;;
esac
[ "$#" -gt 0 ] || { echo "missing backend command" >&2; exit 2; }

iptables_bin=${ZENFM_IPTABLES:-iptables}
firewall_open=0
child=
lock_dir=$pid_file.lock
owns_lock=0
runtime_id=$(basename "$(dirname "$pid_file")" | tr -cd '[:alnum:]' | awk '{ value=$0; if (length(value) > 12) value=substr(value, length(value)-11); print value }')
[ -n "$runtime_id" ] || { echo "could not derive firewall ownership identity" >&2; exit 2; }
firewall_chain=ZFM$runtime_id$port

pause() {
    usleep "$1" 2>/dev/null || sleep 1
}

process_start() {
    pid=$1
    if [ -r "/proc/$pid/stat" ]; then
        sed 's/^.*) //' "/proc/$pid/stat" 2>/dev/null | awk '{print $20}'
        return
    fi
    value=$(ps -p "$pid" -o lstart= 2>/dev/null | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' || true)
    if [ -n "$value" ]; then printf '%s\n' "$value"; else printf 'unverified-%s\n' "$pid"; fi
}

process_exe() {
    pid=$1
    if [ -e "/proc/$pid/exe" ]; then
        value=$(readlink "/proc/$pid/exe" 2>/dev/null | sed 's/ (deleted)$//' || true)
        if [ -n "$value" ]; then
            printf '%s\n' "$value"
            return
        fi
    fi
    value=$(ps -p "$pid" -o comm= 2>/dev/null | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' || true)
    if [ -n "$value" ]; then printf '%s\n' "$value"; else printf 'unverified-%s\n' "$pid"; fi
}

process_zombie() {
    pid=$1
    if [ -r "/proc/$pid/stat" ]; then
        state=$(sed 's/^.*) //' "/proc/$pid/stat" 2>/dev/null | awk '{print $1}')
    else
        state=$(ps -p "$pid" -o stat= 2>/dev/null | sed 's/^[[:space:]]*//' || true)
    fi
    case "$state" in Z*) return 0 ;; *) return 1 ;; esac
}

process_matches() {
    pid=$1
    start=$2
    recorded_exe=$3
    expected_exe=$4
    case "$pid" in ''|*[!0-9]*) return 1 ;; esac
    [ -n "$start" ] || return 1
    kill -0 "$pid" 2>/dev/null || return 1
    current_start=$(process_start "$pid")
    [ -n "$current_start" ] && [ "$current_start" = "$start" ] || return 1
    # Linux start ticks identify the same process across an intentional exec.
    # The lower-resolution ps fallback still needs the executable check below.
    if [ -r "/proc/$pid/stat" ]; then return 0; fi
    current_exe=$(process_exe "$pid")
    [ -n "$current_exe" ] || return 1
    [ "$current_exe" = "$recorded_exe" ] || [ "$current_exe" = "$expected_exe" ]
}

read_record() {
    value=
    if [ -r "$1" ]; then IFS= read -r value < "$1" || true; fi
    printf '%s' "$value"
}

terminate_recorded_child() {
    recorded_pid=$(read_record "$lock_dir/child.pid")
    recorded_start=$(read_record "$lock_dir/child.start")
    recorded_exe=$(read_record "$lock_dir/child.exe")
    recorded_expected=$(read_record "$lock_dir/child.expected")
    [ -n "$recorded_pid" ] || return 0
    case "$recorded_start:$recorded_exe" in
        *unverified-*)
            if kill -0 "$recorded_pid" 2>/dev/null; then
                echo "cannot safely recover an unverified stale ZenFM child" >&2
                return 1
            fi
            return 0
            ;;
    esac
    if ! process_matches "$recorded_pid" "$recorded_start" "$recorded_exe" "$recorded_expected"; then
        if kill -0 "$recorded_pid" 2>/dev/null && [ "$(process_start "$recorded_pid")" = "$recorded_start" ]; then
            echo "could not verify stale ZenFM child identity" >&2
            return 1
        fi
        return 0
    fi
    kill "$recorded_pid" 2>/dev/null || true
    attempt=0
    while process_matches "$recorded_pid" "$recorded_start" "$recorded_exe" "$recorded_expected" && [ "$attempt" -lt 50 ]; do
        pause 100000
        attempt=$(expr "$attempt" + 1)
    done
    if process_matches "$recorded_pid" "$recorded_start" "$recorded_exe" "$recorded_expected"; then
        kill -9 "$recorded_pid" 2>/dev/null || true
        attempt=0
        while process_matches "$recorded_pid" "$recorded_start" "$recorded_exe" "$recorded_expected" && [ "$attempt" -lt 20 ]; do
            pause 100000
            attempt=$(expr "$attempt" + 1)
        done
    fi
    ! process_matches "$recorded_pid" "$recorded_start" "$recorded_exe" "$recorded_expected"
}

remove_firewall() {
    "$iptables_bin" -D INPUT -j "$firewall_chain" >/dev/null 2>&1 || true
    "$iptables_bin" -F "$firewall_chain" >/dev/null 2>&1 || true
    "$iptables_bin" -X "$firewall_chain" >/dev/null 2>&1 || true
}

cleanup() {
    trap - EXIT INT TERM HUP
    if [ -n "$child" ]; then
        kill "$child" 2>/dev/null || true
        attempt=0
        while kill -0 "$child" 2>/dev/null && ! process_zombie "$child" && [ "$attempt" -lt 50 ]; do
            pause 100000
            attempt=$(expr "$attempt" + 1)
        done
        if kill -0 "$child" 2>/dev/null; then kill -9 "$child" 2>/dev/null || true; fi
        wait "$child" 2>/dev/null || true
        child=
    fi
    if [ "$firewall_open" -eq 1 ]; then
        remove_firewall
    fi
    if [ "$owns_lock" -eq 1 ]; then
        owner=
        if [ -r "$lock_dir/owner" ]; then IFS= read -r owner < "$lock_dir/owner" || true; fi
        if [ "$owner" = "$$" ]; then
            rm -f -- "$socket_file" "$pid_file" "$lock_dir/owner" \
                "$lock_dir/owner.start" "$lock_dir/owner.exe" \
                "$lock_dir/child.pid" "$lock_dir/child.start" \
                "$lock_dir/child.exe" "$lock_dir/child.expected"
            rmdir "$lock_dir" 2>/dev/null || true
        fi
    fi
}
trap cleanup EXIT INT TERM HUP

umask 077
owner_start=$(process_start "$$")
owner_exe=$(process_exe "$$")
[ -n "$owner_start" ] && [ -n "$owner_exe" ] || { echo "could not identify supervisor process" >&2; exit 1; }
if ! mkdir "$lock_dir" 2>/dev/null; then
    # A second starter can observe the directory between mkdir and the three
    # ownership writes below. Give that initializer time to finish, and never
    # tear down an incomplete record while its owner is still alive.
    attempt=0
    while [ "$attempt" -lt 50 ]; do
        owner=$(read_record "$lock_dir/owner")
        recorded_owner_start=$(read_record "$lock_dir/owner.start")
        recorded_owner_exe=$(read_record "$lock_dir/owner.exe")
        if [ -n "$owner" ] && [ -n "$recorded_owner_start" ] && [ -n "$recorded_owner_exe" ]; then
            break
        fi
        pause 20000
        attempt=$(expr "$attempt" + 1)
    done
    owner=${owner:-}
    recorded_owner_start=${recorded_owner_start:-}
    recorded_owner_exe=${recorded_owner_exe:-}
    case "$owner" in
        ''|*[!0-9]*)
            echo "cannot safely recover an incomplete supervisor lock" >&2
            exit 1
            ;;
        *) if kill -0 "$owner" 2>/dev/null; then owner_live=1; else owner_live=0; fi ;;
    esac
    if [ "$owner_live" -eq 1 ] && { [ -z "$recorded_owner_start" ] || [ -z "$recorded_owner_exe" ]; }; then
        echo "another ZenFM supervisor is initializing" >&2
        exit 1
    fi
    if process_matches "$owner" "$recorded_owner_start" "$recorded_owner_exe" "$recorded_owner_exe"; then
        echo "ZenFM is already supervised by process $owner" >&2
        exit 1
    fi
    if [ "$owner_live" -eq 1 ] && [ "$(process_start "$owner")" = "$recorded_owner_start" ]; then
        echo "cannot safely recover an unverifiable live supervisor lock" >&2
        exit 1
    fi
    terminate_recorded_child || exit 1
    rm -f -- "$lock_dir/owner" "$lock_dir/owner.start" "$lock_dir/owner.exe" \
        "$lock_dir/child.pid" "$lock_dir/child.start" "$lock_dir/child.exe" \
        "$lock_dir/child.expected"
    rmdir "$lock_dir" 2>/dev/null || { echo "could not recover stale supervisor lock" >&2; exit 1; }
    mkdir "$lock_dir" 2>/dev/null || { echo "another ZenFM supervisor is starting" >&2; exit 1; }
fi
owns_lock=1
printf '%s\n' "$$" > "$lock_dir/owner" || { echo "could not record supervisor ownership" >&2; exit 1; }
printf '%s\n' "$owner_start" > "$lock_dir/owner.start"
printf '%s\n' "$owner_exe" > "$lock_dir/owner.exe"
echo "$$" > "$pid_file"
echo "Supervisor acquired the ZenFM runtime lock (pid $$)."
if [ "$kindle" -eq 1 ]; then
    if "$iptables_bin" --version >/dev/null 2>&1; then
        # Recover an owned rule left by SIGKILL or power loss before installing one
        # jump to a dedicated chain. A losing concurrent supervisor never reaches
        # this point because it does not own the runtime lock.
        firewall_open=1
        remove_firewall
        "$iptables_bin" -N "$firewall_chain"
        "$iptables_bin" -A "$firewall_chain" -p tcp --dport "$port" -j ACCEPT
        "$iptables_bin" -I INPUT -j "$firewall_chain"
        echo "Supervisor opened the Kindle firewall for TCP port $port."
    else
        echo "warning: iptables is unavailable; the Kindle firewall may block TCP port $port." >&2
    fi
fi

case "$1" in
    /*) expected_exe=$(CDPATH= cd -- "$(dirname "$1")" && pwd -P)/$(basename "$1") ;;
    *) expected_exe=$1 ;;
esac
echo "Supervisor launching the ZenFM backend."
"$@" &
child=$!
child_start=$(process_start "$child")
[ -n "$child_start" ] || { echo "could not identify ZenFM child process" >&2; exit 1; }
printf '%s\n' "$child_start" > "$lock_dir/child.start"
printf '%s\n' "$child" > "$lock_dir/child.pid"
printf '%s\n' "$expected_exe" > "$lock_dir/child.expected"
attempt=0
child_exe=
while kill -0 "$child" 2>/dev/null && [ "$attempt" -lt 20 ]; do
    child_exe=$(process_exe "$child")
    [ "$child_exe" = "$expected_exe" ] && break
    pause 10000
    attempt=$(expr "$attempt" + 1)
done
[ -n "$child_exe" ] || { echo "could not identify ZenFM child executable" >&2; exit 1; }
printf '%s\n' "$child_exe" > "$lock_dir/child.exe"
echo "Supervisor started the ZenFM backend (pid $child)."
if wait "$child"; then status=0; else status=$?; fi
child=
echo "ZenFM backend exited with status $status."
exit "$status"
