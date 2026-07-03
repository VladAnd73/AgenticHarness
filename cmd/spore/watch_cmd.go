package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/watch"
)

func runWatch(args []string) int {
	if len(args) < 1 || args[0] != "prs" {
		fmt.Fprintln(os.Stderr, "usage: spore watch prs [--project-root DIR] [--dry-run]")
		return 1
	}
	fs := flag.NewFlagSet("watch prs", flag.ExitOnError)
	root := fs.String("project-root", "", "project root (default: cwd)")
	dryRun := fs.Bool("dry-run", false, "report without telling or saving state")
	_ = fs.Parse(args[1:])

	if *root != "" {
		if err := os.Chdir(*root); err != nil {
			fmt.Fprintf(os.Stderr, "spore watch: %v\n", err)
			return 1
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore watch: %v\n", err)
		return 1
	}
	project, err := task.ProjectName(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore watch: %v\n", err)
		return 1
	}
	rep, err := watch.Run(cwd, project, *dryRun, task.Tell)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore watch: %v\n", err)
		return 1
	}
	fmt.Printf("pr-watch %s: %d alert(s), %d already-seen\n", project, rep.Alerts, rep.Skipped)
	return 0
}
