package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	spore "github.com/versality/spore"
	"github.com/versality/spore/internal/project"
)

const projectUsage = `spore project - create spore-managed projects

Usage:
  spore project new <dir>

new  Make <dir> a spore project: git init, bundled skills, the
     coordinator role at bootstrap/coordinator/role.md, and .worktrees/
     in .gitignore. Keeps files that already exist, so it is safe to
     re-run on a half-set-up project. Prints the host registration step
     that still has to be done as root.
`

func runProject(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, projectUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Print(projectUsage)
		return 0
	case "new":
		return runProjectNew(args[1:])
	}
	fmt.Fprintf(os.Stderr, "spore project: unknown subcommand %q\n\n%s", args[0], projectUsage)
	return 2
}

func runProjectNew(args []string) int {
	fs := flag.NewFlagSet("project new", flag.ContinueOnError)
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore project new:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(projectUsage)
		return 0
	}
	if fs.NArg() != 1 {
		fmt.Fprint(os.Stderr, projectUsage)
		return 2
	}
	res, err := project.New(fs.Arg(0), spore.BundledSkills, spore.BundledCoordinatorRole)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore project new:", err)
		return 1
	}
	for _, p := range res.Created {
		fmt.Printf("wrote %s\n", p)
	}
	fmt.Printf("project %s ready at %s\n\n", res.Name, res.Root)
	if res.NoGitIdentity {
		fmt.Println("git has no identity here, so commits will fail. Set one:")
		fmt.Println(`  git config --global user.name "<name>"`)
		fmt.Println(`  git config --global user.email "<email>"`)
		fmt.Println()
	}
	fmt.Print(res.HostStep)
	return 0
}
