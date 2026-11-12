// Package spore exists solely to host go:embed assets that ship with
// the spore CLI. The kernel implementation lives under cmd/ and
// internal/; this top-level package is just an asset container.
package spore

import (
	"embed"
	"strings"
)

// Version returns the release version embedded from the repository's
// VERSION file.
//
//go:embed VERSION
var versionFile string

var buildCommit = "unknown"

func Version() string {
	return strings.TrimSpace(versionFile)
}

func BuildCommit() string {
	return strings.TrimSpace(buildCommit)
}

func BuildVersion() string {
	commit := BuildCommit()
	if commit == "" || commit == "unknown" {
		return Version() + " (commit unknown)"
	}
	return Version() + " (" + commit + ")"
}

// BundledFlake is the minimal NixOS flake `spore infect` stages into a
// temp directory and runs nixos-anywhere against when the operator
// does not pass --flake. See bootstrap/flake/README.md for shape and
// limits.
//
//go:embed all:bootstrap/flake
var BundledFlake embed.FS

// BundledSkills is the skill tree `spore install` drops into a target
// project's .claude/skills/ and .agents/skills/ so the agent can discover
// the spore-bootstrap, diagram, and select-agent skills without a
// source-tree checkout.
//
//go:embed all:bootstrap/skills
var BundledSkills embed.FS

// BundledHandover is the attach shell, agent wrappers, hooks, and
// systemd user units installed by `spore infect --repo`.
//
//go:embed all:bootstrap/handover
var BundledHandover embed.FS

// BundledCoordinatorRole is the default role file the fleet reconciler
// uses to boot the singleton coordinator agent. Consumers can override
// by writing their own bootstrap/coordinator/role.md before bootstrap.
//
//go:embed bootstrap/coordinator/role.md
var BundledCoordinatorRole []byte

// BundledMigrations is the host-migration script tree consumed by
// `spore migrate` and the spore-fleet module's activation hook. Each file
// under bootstrap/migrations/ is a numbered, idempotent shell script.
// See bootstrap/migrations/README.md for the authoring contract.
//
//go:embed all:bootstrap/migrations
var BundledMigrations embed.FS

// BundledRecipes is the embedded recipe library browsed via
// `spore recipes ls` / `spore recipes show <name>`. Each .md file
// under bootstrap/recipes/ is one recipe; the filename (sans .md) is
// its canonical name.
//
//go:embed all:bootstrap/recipes
var BundledRecipes embed.FS

// BundledRoles holds the role-loop bodies (engineer, reviewer,
// reviewer-code-review). A project's bootstrap/roles/<name>.md
// overrides the bundled body of the same name.
//
//go:embed all:bootstrap/roles
var BundledRoles embed.FS
