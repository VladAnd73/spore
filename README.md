<p align="center">
  <img src="docs/logo.png" alt="spore" width="850">
</p>

# spore

[![CI](https://github.com/versality/spore/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/versality/spore/actions/workflows/ci.yml)
[![Coverage](https://codecov.io/gh/versality/spore/branch/main/graph/badge.svg)](https://codecov.io/gh/versality/spore)

A local harness for LLM coding agents. Each project gets one
coordinator agent and a fleet of worker agents. Every task is a
`tasks/<slug>.md` file, gets its own git worktree and tmux session, and
closes only with evidence. Status: beta.

Tmux session names:

| Session                       | What                           |
|-------------------------------|--------------------------------|
| `spore/<project>/coordinator` | the project's coordinator      |
| `spore/<project>/<slug>`      | one worker per active task     |
| `spore/pilot/<name>`          | an operator shell on the host  |

In commands below, `$IP`, `$HOST`, `$NAME`, `$PROJECT` and `$SLUG` are
placeholders: set them in your shell or replace them.

## Requirements

Workstation (runs `spore infect`):

- Nix with flakes enabled, `ssh`, `ssh-keygen`.
- The `spore` CLI. Either of:

```sh
nix profile install github:versality/spore
# or from a checkout, Go 1.25+; the commit lets infect pin the host's spore
go build -ldflags "-X github.com/versality/spore.buildCommit=$(git rev-parse HEAD)" \
  -o ~/.local/bin/spore ./cmd/spore
```

A CLI built without the commit makes `spore infect` refuse, because the
host would get the older spore pinned in the bundled `flake.lock`.

Target host:

- A fresh x86_64 VM, root over SSH by key, able to kexec, disk at
  `/dev/sda`. `spore infect` wipes it.

## Install on a fresh host

```sh
ssh-keygen -y -f ~/.ssh/id_ed25519 > ~/.ssh/id_ed25519.pub  # .pub must exist
spore infect $IP \
  --ssh-key ~/.ssh/id_ed25519 \
  --hostname $HOST \
  --repo /path/to/project \
  --coordinator-agent claude
ssh -t spore@$IP
```

What you get:

- NixOS from `bootstrap/flake/`. The same flake is left on the host at
  `/etc/nixos`, with the config exposed as `#<host>` and
  `#spore-bootstrap`.
- The project at `/home/spore/<basename>`, registered in
  `/etc/nixos/spore-projects.nix`, with its coordinator running.
- User `spore`: login shell is `spore-attach` (no plain shell prompt,
  no sudo). Use `root` for admin.

First login on the box, as `spore`:

```sh
git config --global user.name "<name>"       # the host has no git identity;
git config --global user.email "<email>"     # commits fail without it
echo "<token>" | gh auth login --with-token  # browser login fails: no xdg-open
```

Log the agent in once: attach the coordinator and finish the `claude`
login in its pane.

Own NixOS config instead of the bundled one: `--flake ./nixos#<attr>`.
That flake is not copied to `/etc/nixos`. Full flags:
[docs/infect.md](docs/infect.md).

## Add a project

On the host, as `spore` (from a pilot shell):

```sh
spore project new /home/spore/$NAME
```

It runs `git init`, installs the skills under `.claude/skills/`, writes
`bootstrap/coordinator/role.md`, and adds `.worktrees/` to
`.gitignore`. It keeps files that already exist. It prints the step
that registers the project with the host; as root:

1. Add the project's line inside the `{ }` of
   `/etc/nixos/spore-projects.nix`. With two projects the file reads:

   ```nix
   {
     first.path = "/home/spore/first";
     "<name>".path = "/home/spore/<name>";
   }
   ```

2. Apply it:

```sh
nixos-rebuild switch --flake /etc/nixos
```

`switch` drains the running workers of every project, then kills the
ones still running at the timeout. Run `spore fleet status` as the
spore user first, and switch when the fleet is idle.

The host's reconciler then starts `spore/<name>/coordinator`. To start
it before registering:

```sh
cd /home/spore/$NAME && spore coordinator start
```

Once the project has a build file (`go.mod`, `package.json`,
`flake.nix`, ...), write its agent instructions:

```sh
cd /home/spore/$NAME && spore bootstrap
```

`spore bootstrap` is re-entrant: each run records the stages it can
prove and prints the first blocker.

The first agent launch in a new folder shows Claude Code's folder-trust
prompt. Attach the coordinator and accept it.

## Attach

```sh
ssh -t spore@$HOST              # the only coordinator, else the pilot shell plus a list
ssh -t spore@$HOST $PROJECT     # that project's coordinator
ssh -t spore@$HOST coord        # menu of running coordinators
ssh -t spore@$HOST pilot $NAME  # private shell at spore/pilot/$NAME
```

- Detach with `Ctrl-b d`; that ends the SSH session.
- In a pilot shell, `Ctrl-b s` switches sessions.
- A second operator with a fixed landing: add to
  `/home/spore/.ssh/authorized_keys`:
  `command="/usr/local/bin/spore-attach pilot <name>",no-port-forwarding ssh-ed25519 AAAA...`

## Day to day

Run inside the project directory.

```sh
spore task new "<title>" --body "<brief>"  # draft task
spore task start $SLUG                     # worktree + tmux session + agent
spore task ls                              # everything not done
spore task tell $SLUG "<message>"          # message a worker (or: coordinator)
spore task verify $SLUG                    # evidence verdict
spore task merge $SLUG                     # merge wt/<slug>; refuses on red just check
spore task done $SLUG                      # close, kill session, remove worktree

spore coordinator status                   # is the coordinator alive
spore coordinator restart

spore fleet status                         # kill switch + live workers
spore fleet disable                        # stop spawning (host-wide)
spore fleet enable
spore fleet reconcile                      # one reconcile pass now

spore lint                                 # drift, file-size, comment-noise, em-dash
```

Optional:

- `spore dream`: nightly, digest this machine's session transcripts into
  reviewed memory entries. Off until `[dreams] enabled = true`; see
  [nightly-dreaming-deploy.md](docs/todo/nightly-dreaming-deploy.md).
- `spore eval list` / `spore eval run <scenario>`: run a sandboxed agent
  against a scenario to measure a prompt change. Spends tokens.

## Rebuild and update

On the host, as root:

```sh
cd /etc/nixos
nix flake update spore                                        # take the newest spore
nixos-rebuild switch --flake /etc/nixos                       # switch, not boot
readlink -f /run/current-system /nix/var/nix/profiles/system  # must match
```

`switch` drains running workers first (see "Add a project").
`nixos-rebuild boot` (or `switch-to-configuration boot`) avoids that but
only takes effect after a reboot: the profile moves on, the running
system does not. If the last check prints two different paths, run the
`switch` again.

### Hosts installed before the shared login shell

The host flake (`/etc/nixos`, or your own copy) is not part of the
`spore` input, so a spore update does not change it. If its
`configuration.nix` defines `sporeAttach` with an inline script, the
spore user keeps the old login: it exits when several coordinators run,
and tmux servers it starts take it as their default shell. Drop the
inline script, point the login shell at the shipped one, then switch:

```nix
users.users.spore.shell = "${sporePkgs.shims}/bin/spore-attach";
```

Until then, keep `set -g default-shell <path to bash>` in the spore
user's `~/.tmux.conf`.

## Troubleshooting

| Symptom | Check | Fix |
|---|---|---|
| `ssh spore@<host>` drops you right away | `ssh root@<host> runuser -u spore -- tmux ls` | update spore and rebuild; old `spore-attach` exits when several coordinators run |
| Coordinator `died on spawn`, though the agent is on PATH | `tmux show -g default-shell` | if it names `spore-attach`: `tmux set -g default-shell "$(command -v bash)"`, then `spore fleet reconcile` |
| Change not live after a rebuild | `readlink -f /run/current-system /nix/var/nix/profiles/system` | `nixos-rebuild switch --flake /etc/nixos` |
| `experimental Nix feature 'nix-command' is disabled` | `nix config show experimental-features` | rebuild with the current bundled flake, or pass `--extra-experimental-features 'nix-command flakes'` |
| `nixos-rebuild --flake` cannot find the config | `nix eval /etc/nixos#nixosConfigurations --apply builtins.attrNames` | use `#spore-bootstrap`, or set `networking.hostName` in `local.nix` |
| New project has no coordinator | `grep <name> /etc/nixos/spore-projects.nix` | add it there and rebuild, or `spore coordinator start` in the project |
| `unable to auto-detect email address` | `git config --global user.email` | set `user.name` and `user.email` |
| `gh` missing or not logged in | `gh auth status` | rebuild (the flake installs `gh`), then `gh auth login --with-token` |
| Agent stuck on a trust or login prompt | `ssh -t spore@<host> <project>` | answer it in the pane |
| Workers not spawning | `spore fleet status` | `spore fleet enable && spore fleet reconcile` |

## Develop spore

```sh
nix develop
just check  # fmt, vet, golangci-lint, spore lint, go test, govulncheck, nix flake check
just build
```

Start at [cmd/spore/main.go](cmd/spore/main.go).

## Docs

- [docs/infect.md](docs/infect.md): `spore infect` flags and failures.
- [docs/configuration.md](docs/configuration.md): `spore.toml`
  worker mix, matter plugins (Linear), editor, graceful deploys.
- [docs/design.md](docs/design.md): design rationale.
- [docs/worker-dispatch.md](docs/worker-dispatch.md): worker spawn and
  merge path.
- [docs/evidence.md](docs/evidence.md): the evidence contract for
  closing tasks.
- [docs/budget.md](docs/budget.md): spend tracking.
- [docs/todo/nightly-dreaming.md](docs/todo/nightly-dreaming.md): how
  `spore dream` works;
  [nightly-dreaming-deploy.md](docs/todo/nightly-dreaming-deploy.md):
  putting it on a timer.
- [docs/todo/eval-harness-for-prompt-surfaces.md](docs/todo/eval-harness-for-prompt-surfaces.md):
  `spore eval` scenarios and recorded runs.
- [bootstrap/README.md](bootstrap/README.md): bootstrap stages and
  skills.

## License

Apache License 2.0. See [LICENSE](LICENSE).
