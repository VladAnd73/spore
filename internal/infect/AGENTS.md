# infect

`spore infect <ip>` wipes a fresh server and reinstalls NixOS via
`nixos-anywhere`, using the bundled flake at `bootstrap/flake/`.
This file is the operations contract for the agent driving an
end-to-end infect: do not re-derive it, do not ask the operator for
it, do not pre-confirm steps the contract already authorises.

## handover contract

End state: a local tmux window named `coord` attached over SSH to
the live `spore/<project>/coordinator` session on the infected box.
The coordinator greets the operator on attach with project name,
host, fleet status, and active-task count, then drops to a shell.

The agent produces this end state. The operator confirms only the
IP. They do not need to confirm wipe-and-reinstall; that is the
whole job.

## two users, by design

- `root`: only used during infect (nixos-anywhere SSHes here, the
  agent rsyncs and runs `spore bootstrap` here) and for emergency
  admin. Operator-facing tooling never logs in as root.
- `spore`: declared by the bundled flake. Login shell is
  `/usr/local/bin/spore-attach` (in `bootstrap/handover/`); no sudo,
  no wheel, no shell prompt. Tmux sessions live in spore's tmux
  server, not root's. spore-attach landing modes
  (`bootstrap/handover/spore-attach.sh` is the one copy; the
  bundled flake's login shell wraps it):
  - `ssh -t spore@<ip>`: attaches the coordinator when exactly one
    runs. With none or several it lands in `spore/pilot/default`
    and lists the coordinators; it never exits non-zero for that.
    This is what the local `coord` window connects to right after
    infect, when only one project exists.
  - `ssh -t spore@<ip> <project>`: attaches
    `spore/<project>/coordinator`. `coord` shows a menu instead.
  - `spore-attach pilot <name>` (secondary pilots, wired via a
    per-key `command=` in `/home/spore/.ssh/authorized_keys`):
    attaches to a private session `spore/pilot/<name>`. Use one
    name per pilot so they do not share a pane.

## defaults the agent applies without asking

When the operator hands you `<ip>` and (optionally) a target repo:

- SSH user during infect: `root`. SSH key: `~/.ssh/id_ed25519`.
  Post-infect operator SSH: `spore` (forced into coord pane).
- Initial coordinator agent: `--coordinator-agent claude` unless the
  operator asks for Codex. Pass `--coordinator-model <model>` when
  the first coordinator should pin a model; empty means the
  selected CLI default. Use the Codex shape explicitly when requested:
  `--coordinator-agent codex --coordinator-model gpt-5.5
  --coordinator-effort high`.
- Hostname: `nixos` (the bundled flake default; survives
  reinstall).
- Disk: `/dev/sda` (`bootstrap/flake/disk-config.nix`). The infect
  command exists to wipe this. Do not ask.
- Repo destination on box: `/home/spore/<basename of source>`.
  Owned by `spore:users`. Rsync from local goes to root, then
  the agent moves and chowns; spore has no sshd write access
  beyond what spore-attach allows.
- Stages to `--skip` on `spore bootstrap`: `tests-pass`,
  `creds-wired`, `readme-followed`, `validation-green`,
  `pilot-aligned`. Each fails on consumer-side state the agent
  cannot or should not edit. Skipping is the prescribed escape
  hatch.
- `info-gathered.json`: write
  `{"tickets":{"tool":"none"},"knowledge":{"tool":"none"}}` unless
  the operator named a real ticketing or wiki tool.
- Handover artifacts are embedded in the CLI and installed by
  `spore infect --repo`: `bootstrap/handover/*.sh` to
  `/usr/local/bin/`, hooks to `/home/spore/.claude/hooks/`, and
  settings to `/home/spore/.claude/settings.json`. Persist
  `SPORE_COORDINATOR_AGENT`, `SPORE_AGENT_BINARY`,
  `SPORE_COORDINATOR_PROVIDER`, `SPORE_COORDINATOR_MODEL`, and
  `SPORE_COORDINATOR_EFFORT` in `/etc/spore/coordinator.env`; the
  spore user's `.bashrc` sources it for interactive recovery shells,
  and the systemd coordinator service reads it via `EnvironmentFile`.
  The coordinator wrapper launches Claude or Codex and falls back to
  the greet wrapper when the selected agent needs first login.

## when to ask

Only when an action is operator-bound or genuinely ambiguous:

- The host already runs an unrelated workload (mounts / hostnames
  do not match a fresh provider image). Confirm before wiping.
- The operator has not named a target repo and the box is meant
  to host one.
- An interactive auth dance (e.g. installing claude-code on the
  box with an OAuth flow).

## the script (idempotent)

1. `ssh-keygen -R <ip>` to clear stale host keys.
2. Launch infect in a tmux window so the operator can watch:
   `tmux new-window -d -n infect "nix run . -- infect <ip>
   --ssh-key ~/.ssh/id_ed25519 --repo <src> --coordinator-agent
   claude --coordinator-model sonnet | tee /tmp/spore-infect.log"`.
   Wait via `Monitor` until `=== EXIT ===` lands.
   `nix run .` records the build commit; a plain `go run` or
   `go build` does not, and infect refuses without one.
3. `spore infect --repo` copies the current binary to
   `/usr/local/bin/spore` (via the bundled flake's nix activation),
   rsyncs `<src>` to `/home/spore/<basename>` with `.git/` included
   and `.env*` excluded, installs handover assets (hooks +
   settings.json), writes `/etc/spore/coordinator.env`, creates
   `tasks/` when absent, enables the fleet flag, and runs the first
   reconcile. Lingering and the per-project reconcile timer are
   declared in the bundled flake (`users.users.spore.linger` +
   `services.spore-fleet` with `projects = import ./spore-projects.nix`),
   so they come up at activation; Stage() writes
   `spore-projects.nix` with the single `<basename>` entry before
   nixos-anywhere reads the flake. The staged flake lands in the
   host's `/etc/nixos` via `--extra-files`; later projects are added
   there and applied with `nixos-rebuild switch --flake /etc/nixos`.
4. Locally, open the handover window:
   `tmux new-window -d -n coord "ssh -t -o
   ServerAliveInterval=30 spore@<ip>"`. The forced login shell
   (`spore-attach`) does the tmux attach itself; do not pass an
   explicit attach command.

## known gaps

- The bundled flake includes Claude Code and Codex but not their
  credentials. First operator login on the box still needs an
  interactive login once; `ssh -t spore@<ip>` should attach to the
  coordinator pane and show the selected agent's login surface.
- `creds-wired`, `readme-followed`, `validation-green` skip with
  warnings. Consumer projects that want clean stages must
  document the secret surface in their agent instructions, ship a README
  with run / test instructions, and resolve any
  comment-noise / em-dash / file-size lint hits.
