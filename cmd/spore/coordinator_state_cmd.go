package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/coordinator/statearchive"
	"github.com/versality/spore/internal/fleet"
	"github.com/versality/spore/internal/task"
)

const coordinatorStateUsage = `spore coordinator state - keep state.md under the SessionStart budget

Usage:
  spore coordinator state ls [--project NAME]
  spore coordinator state archive <id>... --slug NAME [--project NAME] [--dry-run]

state.md is <main repo root>/state.md, the file the load-state-md
SessionStart hook injects. Above 8192 bytes Claude Code injects only a
short preview, so the rest of the handover is lost on respawn.

ls lists archivable units with an id, byte size, and first line:
"## " sections (id N) and, inside a section, "### " subsections or
paragraphs starting with a bold lead "**..." (id N.M). A unit runs to
the next boundary, trailing blank lines included. The preamble before
the first "## " and the "## Archive" section are not listed.

archive moves the selected units verbatim into
  <state dir>/coordinator/state-archive/<YYYY-MM-DD>-<slug>.md
(host-side, never the project tree) and adds one link line per archive
file under "## Archive" at the end of state.md, creating it if missing.
The archive file is written and fsynced first; state.md is then
replaced atomically. Refuses when the archive file exists, when an id
is unknown, or when state.md changed mid-run. Only archive units that
are verifiably done; you decide, the command only moves bytes.

Flags:
  --project NAME  project name (resolved through the tmux session
                  spore/NAME/coordinator) or a path inside the project;
                  default: the cwd project
  --slug NAME     archive file slug (archive only, required)
  --dry-run       print the archive file and the new state.md size
                  without writing (archive only)
`

func runCoordinatorState(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, coordinatorStateUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(coordinatorStateUsage)
		return 0
	case "ls":
		return runCoordinatorStateLs(rest)
	case "archive":
		return runCoordinatorStateArchive(rest)
	default:
		fmt.Fprintf(os.Stderr, "spore coordinator state: unknown subcommand %q\n\n%s", sub, coordinatorStateUsage)
		return 2
	}
}

func runCoordinatorStateLs(args []string) int {
	fs := flag.NewFlagSet("coordinator state ls", flag.ContinueOnError)
	project := fs.String("project", "", "")
	help := fs.Bool("h", false, "")
	helpLong := fs.Bool("help", false, "")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator state ls:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(coordinatorStateUsage)
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprint(os.Stderr, coordinatorStateUsage)
		return 2
	}
	root, err := resolveStateProjectRoot(*project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator state ls:", err)
		return 1
	}
	path := filepath.Join(root, "state.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator state ls:", err)
		return 1
	}
	fmt.Print(statearchive.FormatList(path, statearchive.Parse(raw)))
	return 0
}

func runCoordinatorStateArchive(args []string) int {
	fs := flag.NewFlagSet("coordinator state archive", flag.ContinueOnError)
	project := fs.String("project", "", "")
	slug := fs.String("slug", "", "")
	dryRun := fs.Bool("dry-run", false, "")
	help := fs.Bool("h", false, "")
	helpLong := fs.Bool("help", false, "")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator state archive:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(coordinatorStateUsage)
		return 0
	}
	if fs.NArg() == 0 || *slug == "" {
		fmt.Fprint(os.Stderr, coordinatorStateUsage)
		return 2
	}
	root, err := resolveStateProjectRoot(*project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator state archive:", err)
		return 1
	}
	stateDir, err := task.StateDirForProject(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator state archive:", err)
		return 1
	}
	name, _ := task.ProjectName(root)
	res, err := statearchive.Archive(statearchive.Request{
		StateFile:  filepath.Join(root, "state.md"),
		ArchiveDir: filepath.Join(stateDir, fleet.CoordinatorSlug, "state-archive"),
		Project:    name,
		Slug:       *slug,
		IDs:        fs.Args(),
		DryRun:     *dryRun,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator state archive:", err)
		return 1
	}
	if *dryRun {
		fmt.Printf("dry run: would write %s:\n\n%s\n", res.ArchivePath, res.ArchiveContent)
		fmt.Printf("dry run: state.md %d -> %d bytes, link:\n%s\n", res.OldSize, res.NewSize, res.Link)
		return 0
	}
	ids := make([]string, len(res.Removed))
	for i, u := range res.Removed {
		ids[i] = u.ID
	}
	fmt.Printf("archived %s -> %s\nstate.md %d -> %d bytes (budget %d)\n",
		strings.Join(ids, " "), res.ArchivePath, res.OldSize, res.NewSize, statearchive.Budget)
	return 0
}

// resolveStateProjectRoot maps --project to the main repo root whose
// state.md the SessionStart hook reads. There is no project registry,
// so a bare name goes through the live coordinator session's cwd.
func resolveStateProjectRoot(project string) (string, error) {
	if project == "" {
		return task.MainRepoRoot("")
	}
	if strings.ContainsRune(project, '/') {
		if fi, err := os.Stat(project); err != nil || !fi.IsDir() {
			return "", fmt.Errorf("--project %s: not a directory", project)
		}
		return task.MainRepoRoot(project)
	}
	session := fmt.Sprintf("spore/%s/%s", project, fleet.CoordinatorSlug)
	out, err := exec.Command("tmux", "display-message", "-p", "-t", "="+session+":", "#{pane_current_path}").Output()
	dir := strings.TrimSpace(string(out))
	if err != nil || dir == "" {
		return "", fmt.Errorf("--project %s: no tmux session %s to resolve the project dir; pass a path instead", project, session)
	}
	return task.MainRepoRoot(dir)
}
