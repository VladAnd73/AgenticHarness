package watch

import (
	"fmt"
	"os"
)

// defaultReleaseInstruction is the generic KB-sync wording used when the
// [releases] config sets no instruction. It names no skill; consumers that
// want a skill-specific instruction set one in their watch.toml.
const defaultReleaseInstruction = "Start a worker to sync the Notion Product Knowledge KB for this release."

// ReleaseReport summarizes a release-watch run: pokes fired (one per
// coordinator per newly released repo) and repos whose tag was unchanged.
type ReleaseReport struct {
	Pokes     int
	Unchanged int
}

// RunReleases polls each configured repo's latest release and, when a repo's
// tag differs from the last one notified, delivers a message envelope and a
// poke to each configured coordinator, then stores the new tag. tell writes
// the envelope to a coordinator project's inbox; poke wakes that coordinator.
// projectRoot is the working dir for gh; project names the host whose
// watch.toml and dedup state are read.
func RunReleases(projectRoot, project string, dryRun bool,
	tell func(coordProject, msg string) error,
	poke func(coordProject string) error) (ReleaseReport, error) {

	var rep ReleaseReport
	cfg, err := LoadReleasesConfig(project)
	if err != nil || !cfg.Enabled {
		return rep, err
	}
	st, err := LoadState(project)
	if err != nil {
		return rep, err
	}

	instruction := cfg.Instruction
	if instruction == "" {
		instruction = defaultReleaseInstruction
	}

	dirty := false
	for _, repo := range cfg.Repos {
		rel, found, err := LatestRelease(projectRoot, repo)
		if err != nil {
			// A single repo's real error skips it WITHOUT advancing its stored
			// tag, so a transient failure fires next good run rather than
			// swallowing the release.
			fmt.Fprintf(os.Stderr, "release-watch: %s: %v\n", repo, err)
			continue
		}
		if !found {
			continue // zero releases: benign, nothing to report
		}
		prev, seen := st.ReleaseTag(repo)
		if !seen {
			// First observation: seed the baseline silently, do not poke, so
			// installing the watcher (or adding a repo) is not a poke storm.
			if !dryRun {
				st.MarkRelease(repo, rel.TagName)
				dirty = true
			}
			continue
		}
		if prev == rel.TagName {
			rep.Unchanged++
			continue
		}

		msg := fmt.Sprintf("New release on `%s` (tag `%s`): %s. %s",
			repo, rel.TagName, rel.URL, instruction)
		if dryRun {
			rep.Pokes += len(cfg.Coordinators)
			continue
		}
		for _, coord := range cfg.Coordinators {
			if err := tell(coord, msg); err != nil {
				_ = st.Save()
				return rep, err
			}
			// Poke is best-effort after the envelope lands: the message is
			// already delivered, so warn rather than re-firing next cycle.
			if err := poke(coord); err != nil {
				fmt.Fprintf(os.Stderr, "release-watch: poke %s: %v\n", coord, err)
			}
			rep.Pokes++
		}
		st.MarkRelease(repo, rel.TagName)
		dirty = true
	}

	if dryRun {
		return rep, nil
	}
	if dirty {
		if err := st.Save(); err != nil {
			return rep, err
		}
	}
	return rep, nil
}
