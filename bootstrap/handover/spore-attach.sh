#!/usr/bin/env bash
# Login shell for the spore operator user.
#
#   ssh -t spore@<host>               The only coordinator if exactly one
#                                     runs; otherwise the default pilot
#                                     shell, with the running
#                                     coordinators listed.
#   ssh -t spore@<host> <project>     That project's coordinator.
#   ssh -t spore@<host> coord         Menu of running coordinators.
#   ssh -t spore@<host> pilot [name]  Private shell session at
#                                     spore/pilot/<name> (default
#                                     "default").
#
# Never exits non-zero just because several projects run: that used to
# lock the operator out of the spore user.
#
# Give a secondary pilot a fixed landing with a per-key forced command in
# /home/spore/.ssh/authorized_keys:
#   command="/usr/local/bin/spore-attach pilot andrei",no-port-forwarding,no-X11-forwarding ssh-ed25519 AAAA... andrei@laptop
#
# Coordinator sessions are owned by `spore fleet reconcile`; this script
# never spawns one. From a pilot shell, switch with `Ctrl-b s` or
#   tmux attach -r -t =spore/<project>/coordinator   # read-only
#
# Detach (Ctrl-b d) ends the SSH session. For privileged work, SSH in as
# root instead.
set -euf

# sshd runs the login shell as `<shell> -c "<command>"`. A forced
# authorized_keys command repeats this script's path as its first word;
# a plain `ssh spore@<host> <args>` does not, so only drop the first word
# when it is this script. -f above keeps the split from globbing.
if [ "${1:-}" = "-c" ]; then
    # shellcheck disable=SC2086
    set -- ${2:-}
    case "${1:-}" in
        */spore-attach | spore-attach | */spore-attach.sh) shift ;;
    esac
fi

say() { printf 'spore-attach: %s\n' "$@" >&2; }

valid_name() {
    case "$1" in
        '' | .* | *[!A-Za-z0-9._-]*) return 1 ;;
    esac
}

coordinators() {
    tmux ls -F '#{session_name}' 2>/dev/null | grep -E '^spore/[^/]+/coordinator$' || true
}

attach_pilot() {
    name="${1:-default}"
    if ! valid_name "$name"; then
        say "invalid pilot name: $name (use letters, digits, . _ -)"
        exit 2
    fi
    # Pass `bash -l` as the session command; the spore user's login
    # shell is this script, and tmux would otherwise recurse into it.
    exec tmux new-session -A -s "spore/pilot/${name}" bash -l
}

attach_session() {
    exec tmux attach-session -t "=$1"
}

list_coordinators() {
    for s in "$@"; do
        p="${s#spore/}"
        say "  $s    ssh -t spore@<host> ${p%/coordinator}"
    done
}

mode="${1:-}"
arg="${2:-}"
# shellcheck disable=SC2046
set -- $(coordinators)

case "$mode" in
    '')
        if [ "$#" -eq 1 ]; then
            attach_session "$1"
        fi
        if [ "$#" -eq 0 ]; then
            say "no coordinator session running on this host." \
                "to start one, from this shell run:" \
                "  spore fleet enable && spore fleet reconcile"
        else
            say "$# coordinators running; landing in the pilot shell. Attach one with:"
            list_coordinators "$@"
        fi
        attach_pilot default
        ;;
    coord)
        case "$#" in
            0)
                say "no coordinator session running; landing in the pilot shell."
                attach_pilot default
                ;;
            1) attach_session "$1" ;;
        esac
        PS3="coordinator> "
        select s in "$@" "pilot shell"; do
            case "$s" in
                '') continue ;;
                "pilot shell") attach_pilot default ;;
                *) attach_session "$s" ;;
            esac
        done
        exit 1
        ;;
    pilot)
        attach_pilot "${arg:-default}"
        ;;
    *)
        if ! valid_name "$mode"; then
            say "invalid project name: $mode"
            exit 2
        fi
        for s in "$@"; do
            if [ "$s" = "spore/$mode/coordinator" ]; then
                attach_session "$s"
            fi
        done
        say "no coordinator running for project: $mode"
        if [ "$#" -gt 0 ]; then
            say "running coordinators:"
            list_coordinators "$@"
        fi
        say "usage: ssh -t spore@<host> [<project> | coord | pilot [<name>]]"
        exit 1
        ;;
esac
