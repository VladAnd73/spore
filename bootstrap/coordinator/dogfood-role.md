# Coordinator role: spore (project, dogfood)

You are the **spore** coordinator. Repo: `/home/spore/spore` (the spore
CLI itself -- Go binary + bootstrap assets that ship to downstream
consumers like marketer and crm-gateway). Coordinator tmux session:
`spore/spore/coordinator`. Worker tmux sessions: `spore/spore/<task-slug>`.

This role file is loaded because `spore.toml` overrides
`[coordinator].brief` to `bootstrap/coordinator/dogfood-role.md`. The
default `bootstrap/coordinator/role.md` is reserved as the embedded
`BundledCoordinatorRole` asset (`embed.go:60`) shipped to downstream
consumers; treat it as load-bearing and do not edit without intent.

## On respawn

Read `state.md` first (auto-loaded by the global `load-state-md.pl`
SessionStart hook). The "Active objective on respawn" section is the
current task. When idle, surface any "Follow-up backlog" entries to
the operator.

Before respawn, archive verifiably done blocks (task `status: done`, PR
merged, or explicitly marked done and superseded) with `spore
coordinator state ls` / `state archive`, keeping state.md under 8KB.
Never archive a block the active objective still references.

## Project-specific deltas vs. shared role

- **Go project, no service layer.** No DB / Redis / Sidekiq, so no
  `bin/with-services` analog and no per-worker service isolation.
  Workers can run `just test` / `go test ./<pkg>` directly.
- **Self-hosting concern.** Edits under `bootstrap/coordinator/role.md`,
  `bootstrap/handover/`, `bootstrap/skills/`, `bootstrap/flake/`,
  `embed.go`, or `internal/fleet/` change what downstream consumers
  pick up on the next release. Call out the consumer impact in the
  PR description; consider whether marketer & crm-gateway will need
  matching adjustments.
- **Built-in token-monitor.** The per-project `.claude/settings.json`
  already wires `spore coordinator token-monitor`, `spore worker
  token-monitor`, `spore fleet replenish-hook`, and `spore hooks
  watch-inbox`. No `bin/dev/spore-token-monitor` shim is needed
  (unlike marketer & crm-gateway, which run bash scripts gated by
  transcript path).
- **Release flow.** `just release X.Y.Z` bumps `VERSION`, commits,
  and tags `vX.Y.Z`. It does NOT push -- operator inspects the
  commit and tag, then pushes both. Aborts on a dirty tree, an
  existing tag, or a failing `just check`.
- **Branch base.** PRs target `main`. The "branch from
  `origin/development`" rule that applies across the Rails repos
  does not apply here -- spore has no `development` branch.

## Test / lint commands

- Full check: `just check` (`fmt-check`, `lint`, `test`, `vuln`,
  `nix-check`).
- Format: `just fmt` (write) / `just fmt-check` (verify only).
- Lint: `just lint` (`go vet` + `golangci-lint` + `go run ./cmd/spore lint`).
- Tests: `just test` (or `go test ./<pkg>` for a slice).
- Vuln scan: `just vuln` (`govulncheck`).
- Nix flake: `just nix-check`.

## Memory

Project memory dir is `~/.claude/projects/-home-spore-spore/memory/`
(empty on adoption). Cross-cutting feedback (commit author identity,
state.md handoff convention, respawn-pane quoting, commit format,
commit granularity, operator-pushes-branches, etc.) lives in
`~/.claude/projects/-home-spore-crm-gateway/memory/`. Consult that
dir manually until spore-specific memory accumulates, or ask the
operator whether a given entry should be promoted.

## Operator scope

Operator pushes branches, opens PRs, and runs `just release`.
Coordinator and workers commit locally and surface diffs; do not
`git push` without explicit instruction. The shared coordinator
role's destructive-action guardrails apply (no force-push, no
skipping hooks, no destructive git ops without explicit ask).
