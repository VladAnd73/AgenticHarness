package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/versality/spore/internal/hooks"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/watch"
)

// tellWithPoke wraps a tell func so coordinator-bound envelopes also
// drop a poke into the coordinator's wake channel. The watcher runs
// headless (systemd timer, no Claude session), so no Notification hook
// fires on its behalf; without the poke an idle coordinator's Stop-hook
// watch never wakes and the alert sits unread (incident 2026-07-06).
// Poke failure is best-effort: the envelope is already delivered, so
// warn on stderr rather than re-firing the alert next cycle.
func tellWithPoke(project string, tell func(slug, msg string) error) func(slug, msg string) error {
	return func(slug, msg string) error {
		if err := tell(slug, msg); err != nil {
			return err
		}
		if slug == "coordinator" {
			if err := hooks.NotifyCoordinator(project); err != nil {
				fmt.Fprintf(os.Stderr, "spore watch: poke: %v\n", err)
			}
		}
		return nil
	}
}

func runWatch(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: spore watch <prs|releases> [--project-root DIR] [--dry-run]")
		return 1
	}
	switch args[0] {
	case "prs":
		return runWatchPRs(args)
	case "releases":
		return runWatchReleases(args)
	default:
		fmt.Fprintln(os.Stderr, "usage: spore watch <prs|releases> [--project-root DIR] [--dry-run]")
		return 1
	}
}

func runWatchPRs(args []string) int {
	fs := flag.NewFlagSet("watch prs", flag.ExitOnError)
	root := fs.String("project-root", "", "project root (default: cwd)")
	dryRun := fs.Bool("dry-run", false, "report without telling or saving state")
	_ = fs.Parse(args[1:])

	cwd, project, code := watchContext(*root)
	if code != 0 {
		return code
	}
	rep, err := watch.Run(cwd, project, *dryRun, tellWithPoke(project, task.Tell))
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore watch: %v\n", err)
		return 1
	}
	fmt.Printf("pr-watch %s: %d alert(s), %d already-seen\n", project, rep.Alerts, rep.Skipped)
	return 0
}

func runWatchReleases(args []string) int {
	fs := flag.NewFlagSet("watch releases", flag.ExitOnError)
	root := fs.String("project-root", "", "project root (default: cwd)")
	dryRun := fs.Bool("dry-run", false, "report without telling, poking, or saving state")
	_ = fs.Parse(args[1:])

	cwd, project, code := watchContext(*root)
	if code != 0 {
		return code
	}
	// tell delivers the envelope to a coordinator project's message inbox;
	// poke wakes that coordinator's headless watch. Both are project-name
	// addressed so the configurable coordinator list reaches OTHER projects,
	// not only the host.
	tell := func(coord, msg string) error { return task.TellProject(coord, "coordinator", msg) }
	poke := func(coord string) error { return hooks.NotifyCoordinator(coord) }
	rep, err := watch.RunReleases(cwd, project, *dryRun, tell, poke)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore watch: %v\n", err)
		return 1
	}
	fmt.Printf("release-watch %s: %d poke(s), %d repo(s) unchanged\n", project, rep.Pokes, rep.Unchanged)
	return 0
}

// watchContext resolves the working dir and project name for a watch run,
// chdir-ing to root first when set. A non-zero return is the exit code.
func watchContext(root string) (cwd, project string, code int) {
	if root != "" {
		if err := os.Chdir(root); err != nil {
			fmt.Fprintf(os.Stderr, "spore watch: %v\n", err)
			return "", "", 1
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore watch: %v\n", err)
		return "", "", 1
	}
	project, err = task.ProjectName(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore watch: %v\n", err)
		return "", "", 1
	}
	return cwd, project, 0
}
