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
#
# Non-attach commands fall through to bash: when the `-c` string does
# not start with spore-attach, `coord`, `pilot` or the name of a running
# coordinator's project, it runs as `bash -lc "<string>"`. This serves
# editor keys with no `command=` (VS Code Remote-SSH installs and runs
# its server this way). Forced-command keys never reach the
# fall-through: sshd always hands the forced string, not the client's.
#
# A login with no command and no TTY falls through too: it runs as
# `bash -l` reading stdin. VS Code Remote-SSH (local-server mode) runs
# `ssh -T <host>` and pipes its install script in this way. A no-command
# login with a TTY keeps the coord landing, except in a VS Code
# integrated terminal (`TERM_PROGRAM=vscode`): that one runs `bash -l`
# on the TTY, so the terminal opens a plain shell rather than a tmux
# landing. `spore-attach coord` still attaches there.
#
# Leading bash login-shell flags (`-i`, `-l`, `--login`, combined like
# `-il`) are dropped before either rule applies. VS Code Remote-SSH
# resolves the user environment with `$SHELL -i -l -c "<cmd>"`.
set -euf

say() { printf 'spore-attach: %s\n' "$@" >&2; }

# No "." (tmux turns it into "_" in session names) and no leading "-"
# (ssh reads it as an option). Keep in sync with internal/project.
valid_name() {
    case "$1" in
        '' | -* | *[!A-Za-z0-9_-]*) return 1 ;;
    esac
}

coordinators() {
    tmux ls -F '#{session_name}' 2>/dev/null | grep -E '^spore/[^/]+/coordinator$' || true
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --login|-[il]|-[il][il]) shift ;;
        *) break ;;
    esac
done

# sshd invokes the login shell as `<shell> -c "<command-string>"`,
# both for forced commands and for client-supplied ones. A forced
# command repeats this script's path as its first word; a plain
# `ssh spore@<host> <args>` does not. -f above keeps the split from
# globbing.
if [ "${1:-}" = "-c" ]; then
    cmd="${2:-}"
    first="${cmd#"${cmd%%[![:space:]]*}"}"
    first="${first%%[[:space:]]*}"
    case "${first##*/}" in
        spore-attach|spore-attach.sh)
            # word-split the attach string on purpose
            # shellcheck disable=SC2086
            set -- $cmd
            shift   # drop the leading script path
            ;;
        coord|pilot)
            # shellcheck disable=SC2086
            set -- $cmd
            ;;
        *)
            if valid_name "$first" && coordinators | grep -qxF "spore/$first/coordinator"; then
                # shellcheck disable=SC2086
                set -- $cmd
            else
                # $BASH is the interpreter running this script; it survives
                # a minimal sshd PATH on NixOS.
                exec "${BASH:-bash}" -lc "$cmd"
            fi
            ;;
    esac
elif [ "$#" -eq 0 ] && { [ ! -t 0 ] || [ "${TERM_PROGRAM:-}" = vscode ]; }; then
    exec "${BASH:-bash}" -l
fi

# A tmux server started from here takes $SHELL (this script) as its
# default-shell, and the reconciler's coordinator spawn would re-enter
# it as `spore-attach -c <cmd>` and die.
SHELL=$(command -v bash)
export SHELL

no_coordinator() {
    cat >&2 <<'EOF'
spore-attach: no coordinator session running on this host.
spore-attach: dropping into a default pilot session so you can recover.
spore-attach: if Claude or Codex is not logged in yet, run its login command here first.
spore-attach: to start the coordinator, from inside the pane run:
spore-attach:   spore fleet enable && spore fleet reconcile
EOF
}

attach_pilot() {
    name="${1:-default}"
    if ! valid_name "$name"; then
        say "invalid pilot name: $name (use letters, digits, _ -; no leading -)"
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
            no_coordinator
        else
            say "$# coordinators running; landing in the pilot shell. Attach one with:"
            list_coordinators "$@"
        fi
        attach_pilot default
        ;;
    coord)
        case "$#" in
            0)
                no_coordinator
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
        if valid_name "$mode"; then
            for s in "$@"; do
                if [ "$s" = "spore/$mode/coordinator" ]; then
                    attach_session "$s"
                fi
            done
        fi
        say "unknown mode: $mode (not coord, pilot, or a project with a running coordinator)"
        if [ "$#" -gt 0 ]; then
            say "running coordinators:"
            list_coordinators "$@"
        fi
        say "usage: ssh -t spore@<host> [<project> | coord | pilot [<name>]]"
        exit 2
        ;;
esac
