package watch

import (
	"errors"
	"strings"
	"testing"
)

type poked struct {
	project string
}

// setupReleases wires a temp config + state home, writes a [releases] config,
// installs a fake gh, and returns capturing tell/poke funcs.
func setupReleases(t *testing.T, cfgBody, ghScript string) (root string, tells *[]told, pokes *[]poked,
	tell func(string, string) error, poke func(string) error) {
	t.Helper()
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	writeWatchToml(t, cfgDir, "proj", cfgBody)
	fakeGH(t, ghScript)
	var gotTells []told
	var gotPokes []poked
	return t.TempDir(), &gotTells, &gotPokes,
		func(project, msg string) error {
			gotTells = append(gotTells, told{project, msg})
			return nil
		},
		func(project string) error {
			gotPokes = append(gotPokes, poked{project})
			return nil
		}
}

const oneRepoConfig = `
[releases]
enabled = true
repos = ["o/backend"]
coordinators = ["frontend"]
`

// oneReleaseScript returns a single latest release for any repo queried.
const oneReleaseScript = `echo '{"tagName":"v2.0.0","url":"https://github.com/o/backend/releases/tag/v2.0.0","publishedAt":"2026-07-21T10:00:00Z"}'`

// Scenario 1: end-to-end. A repo whose latest tag differs from the stored tag
// delivers a message envelope (repo/tag/url) AND a poke to each coordinator,
// then stores the new tag.
func TestRunReleasesEndToEnd(t *testing.T) {
	root, tells, pokes, tell, poke := setupReleases(t, oneRepoConfig, oneReleaseScript)
	// Seed a prior tag so v2.0.0 is a NEW release, not a first observation.
	st, err := LoadReleaseState("proj")
	if err != nil {
		t.Fatal(err)
	}
	st.Mark("o/backend", "v1.9.0")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	rep, err := RunReleases(root, "proj", false, tell, poke)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pokes != 1 {
		t.Fatalf("want 1 poke, got %+v", rep)
	}
	if len(*tells) != 1 || (*tells)[0].slug != "frontend" {
		t.Fatalf("want one tell to frontend, got %v", *tells)
	}
	for _, want := range []string{"o/backend", "v2.0.0", "releases/tag/v2.0.0", "worker"} {
		if !strings.Contains((*tells)[0].msg, want) {
			t.Fatalf("msg missing %q:\n%s", want, (*tells)[0].msg)
		}
	}
	if len(*pokes) != 1 || (*pokes)[0].project != "frontend" {
		t.Fatalf("want one poke to frontend, got %v", *pokes)
	}
	after, err := LoadReleaseState("proj")
	if err != nil {
		t.Fatal(err)
	}
	if tag, _ := after.Tag("o/backend"); tag != "v2.0.0" {
		t.Fatalf("stored tag = %q, want v2.0.0", tag)
	}
}

const configWithInstruction = `
[releases]
enabled = true
repos = ["o/backend"]
coordinators = ["frontend"]
instruction = "Use the my-skill skill: sync the KB for this release now."
`

// A configured instruction replaces the generic default in the message body.
func TestRunReleasesUsesConfiguredInstruction(t *testing.T) {
	root, tells, _, tell, poke := setupReleases(t, configWithInstruction, oneReleaseScript)
	st, _ := LoadReleaseState("proj")
	st.Mark("o/backend", "v1.9.0")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := RunReleases(root, "proj", false, tell, poke); err != nil {
		t.Fatal(err)
	}
	if len(*tells) != 1 {
		t.Fatalf("want one tell, got %v", *tells)
	}
	msg := (*tells)[0].msg
	if !strings.Contains(msg, "Use the my-skill skill: sync the KB for this release now.") {
		t.Fatalf("msg missing configured instruction:\n%s", msg)
	}
	if strings.Contains(msg, "Notion Product Knowledge KB") {
		t.Fatalf("configured instruction must replace the generic default:\n%s", msg)
	}
	// The repo/tag/url prefix is still present.
	for _, want := range []string{"o/backend", "v2.0.0", "releases/tag/v2.0.0"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("msg missing %q:\n%s", want, msg)
		}
	}
}

// With no instruction configured, the generic default is used.
func TestRunReleasesDefaultInstructionWhenUnset(t *testing.T) {
	root, tells, _, tell, poke := setupReleases(t, oneRepoConfig, oneReleaseScript)
	st, _ := LoadReleaseState("proj")
	st.Mark("o/backend", "v1.9.0")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := RunReleases(root, "proj", false, tell, poke); err != nil {
		t.Fatal(err)
	}
	if len(*tells) != 1 {
		t.Fatalf("want one tell, got %v", *tells)
	}
	if !strings.Contains((*tells)[0].msg, "Start a worker to sync the knowledge base for this release.") {
		t.Fatalf("msg missing generic default:\n%s", (*tells)[0].msg)
	}
	// The kernel default must not carry a consumer-specific term.
	if strings.Contains((*tells)[0].msg, "Notion Product Knowledge KB") {
		t.Fatalf("kernel default must be generic, leaked consumer wording:\n%s", (*tells)[0].msg)
	}
}

// Scenario 2: first-run seeding. No stored tag -> store baseline, do NOT poke.
func TestRunReleasesFirstRunSeeds(t *testing.T) {
	root, tells, pokes, tell, poke := setupReleases(t, oneRepoConfig, oneReleaseScript)
	rep, err := RunReleases(root, "proj", false, tell, poke)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pokes != 0 || len(*tells) != 0 || len(*pokes) != 0 {
		t.Fatalf("first observation must not fire, got %+v / %v / %v", rep, *tells, *pokes)
	}
	after, err := LoadReleaseState("proj")
	if err != nil {
		t.Fatal(err)
	}
	if tag, ok := after.Tag("o/backend"); !ok || tag != "v2.0.0" {
		t.Fatalf("baseline not seeded: tag=%q ok=%v", tag, ok)
	}
}

// Scenario 3: unchanged tag -> no message, no poke, state unchanged.
func TestRunReleasesUnchangedIsSilent(t *testing.T) {
	root, tells, pokes, tell, poke := setupReleases(t, oneRepoConfig, oneReleaseScript)
	st, _ := LoadReleaseState("proj")
	st.Mark("o/backend", "v2.0.0")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	rep, err := RunReleases(root, "proj", false, tell, poke)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pokes != 0 || rep.Unchanged != 1 || len(*tells) != 0 || len(*pokes) != 0 {
		t.Fatalf("unchanged must be silent, got %+v / %v / %v", rep, *tells, *pokes)
	}
}

const twoReposConfig = `
[releases]
enabled = true
repos = ["o/backend", "o/frontend"]
coordinators = ["frontend"]
`

// twoRepoScript: o/backend has a new tag v3.0.0, o/frontend stays at v1.0.0.
const twoRepoScript = `
case "$4" in
"o/backend") echo '{"tagName":"v3.0.0","url":"https://github.com/o/backend/releases/tag/v3.0.0","publishedAt":"2026-07-21T10:00:00Z"}' ;;
"o/frontend") echo '{"tagName":"v1.0.0","url":"https://github.com/o/frontend/releases/tag/v1.0.0","publishedAt":"2026-07-20T10:00:00Z"}' ;;
esac`

// Scenario 4: per-repo isolation. One new, one unchanged -> exactly one poke.
func TestRunReleasesPerRepoIsolation(t *testing.T) {
	root, tells, pokes, tell, poke := setupReleases(t, twoReposConfig, twoRepoScript)
	st, _ := LoadReleaseState("proj")
	st.Mark("o/backend", "v2.0.0")
	st.Mark("o/frontend", "v1.0.0")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	rep, err := RunReleases(root, "proj", false, tell, poke)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pokes != 1 || len(*pokes) != 1 {
		t.Fatalf("want exactly one poke (backend only), got %+v / %v", rep, *pokes)
	}
	if len(*tells) != 1 || !strings.Contains((*tells)[0].msg, "o/backend") {
		t.Fatalf("tell must be for o/backend, got %v", *tells)
	}
	after, _ := LoadReleaseState("proj")
	if tag, _ := after.Tag("o/backend"); tag != "v3.0.0" {
		t.Fatalf("backend tag = %q, want v3.0.0", tag)
	}
	if tag, _ := after.Tag("o/frontend"); tag != "v1.0.0" {
		t.Fatalf("frontend tag = %q, want unchanged v1.0.0", tag)
	}
}

// Scenario 5: no releases / benign. Zero releases -> no error, no poke.
func TestRunReleasesNoReleasesBenign(t *testing.T) {
	root, tells, pokes, tell, poke := setupReleases(t, oneRepoConfig,
		`echo "release not found" >&2; exit 1`)
	rep, err := RunReleases(root, "proj", false, tell, poke)
	if err != nil {
		t.Fatalf("zero-release repo must not error, got %v", err)
	}
	if rep.Pokes != 0 || len(*tells) != 0 || len(*pokes) != 0 {
		t.Fatalf("zero-release repo must not fire, got %+v", rep)
	}
}

// Scenario 6: real error is safe. A non-benign query error skips the repo
// WITHOUT advancing its stored tag, so it fires next good run.
func TestRunReleasesRealErrorDoesNotAdvance(t *testing.T) {
	root, tells, pokes, tell, poke := setupReleases(t, oneRepoConfig,
		`echo "gh: authentication failed" >&2; exit 4`)
	st, _ := LoadReleaseState("proj")
	st.Mark("o/backend", "v1.9.0")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	rep, err := RunReleases(root, "proj", false, tell, poke)
	if err != nil {
		t.Fatalf("a single repo error must not abort the run: %v", err)
	}
	if rep.Pokes != 0 || len(*tells) != 0 || len(*pokes) != 0 {
		t.Fatalf("errored repo must not fire, got %+v", rep)
	}
	after, _ := LoadReleaseState("proj")
	if tag, _ := after.Tag("o/backend"); tag != "v1.9.0" {
		t.Fatalf("errored repo tag must NOT advance, got %q, want v1.9.0", tag)
	}
}

// Scenario 7: disabled. [releases] enabled=false (or absent) -> no-op.
func TestRunReleasesDisabledIsNoOp(t *testing.T) {
	root, tells, pokes, tell, poke := setupReleases(t, `
[releases]
enabled = false
repos = ["o/backend"]
coordinators = ["frontend"]
`, `echo "gh should not be called" >&2; exit 99`)
	rep, err := RunReleases(root, "proj", false, tell, poke)
	if err != nil {
		t.Fatalf("disabled run: %v", err)
	}
	if rep.Pokes != 0 || len(*tells) != 0 || len(*pokes) != 0 {
		t.Fatalf("disabled run must do nothing, got %+v", rep)
	}
}

// Scenario 9: dry-run reports intent, writes no state, sends nothing.
func TestRunReleasesDryRun(t *testing.T) {
	root, tells, pokes, tell, poke := setupReleases(t, oneRepoConfig, oneReleaseScript)
	st, _ := LoadReleaseState("proj")
	st.Mark("o/backend", "v1.9.0")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	rep, err := RunReleases(root, "proj", true, tell, poke)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pokes != 1 {
		t.Fatalf("dry-run must report the intended poke count, got %+v", rep)
	}
	if len(*tells) != 0 || len(*pokes) != 0 {
		t.Fatalf("dry-run must send nothing, got tells=%v pokes=%v", *tells, *pokes)
	}
	after, _ := LoadReleaseState("proj")
	if tag, _ := after.Tag("o/backend"); tag != "v1.9.0" {
		t.Fatalf("dry-run must not write state, tag=%q want v1.9.0", tag)
	}
}

const emptyCoordinatorsConfig = `
[releases]
enabled = true
repos = ["o/backend"]
coordinators = []
`

// A new release with no coordinators configured must NOT advance the stored
// tag, so the release still fires once a coordinator is added rather than
// being silently consumed by an empty notify loop.
func TestRunReleasesEmptyCoordinatorsDoesNotAdvance(t *testing.T) {
	root, tells, pokes, tell, poke := setupReleases(t, emptyCoordinatorsConfig, oneReleaseScript)
	st, _ := LoadReleaseState("proj")
	st.Mark("o/backend", "v1.9.0")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	rep, err := RunReleases(root, "proj", false, tell, poke)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pokes != 0 || len(*tells) != 0 || len(*pokes) != 0 {
		t.Fatalf("no coordinators must fire nothing, got %+v / %v / %v", rep, *tells, *pokes)
	}
	after, _ := LoadReleaseState("proj")
	if tag, _ := after.Tag("o/backend"); tag != "v1.9.0" {
		t.Fatalf("tag must NOT advance with no coordinators, got %q want v1.9.0", tag)
	}
}

const emptyReposConfig = `
[releases]
enabled = true
repos = []
coordinators = ["frontend"]
`

// An enabled config with no repos is a no-op: gh is never called and nothing
// fires.
func TestRunReleasesEmptyReposIsNoOp(t *testing.T) {
	root, tells, pokes, tell, poke := setupReleases(t, emptyReposConfig,
		`echo "gh must not be called" >&2; exit 99`)
	rep, err := RunReleases(root, "proj", false, tell, poke)
	if err != nil {
		t.Fatalf("empty repos must not error, got %v", err)
	}
	if rep.Pokes != 0 || rep.Unchanged != 0 || len(*tells) != 0 || len(*pokes) != 0 {
		t.Fatalf("empty repos must be a no-op, got %+v", rep)
	}
}

// The poke is best-effort AFTER the envelope lands: a poke failure must not
// fail the run, must not lose the delivered envelope, and must still advance
// the stored tag (so the release is not re-fired next cycle).
func TestRunReleasesPokeFailureStillAdvances(t *testing.T) {
	root, tells, _, tell, _ := setupReleases(t, oneRepoConfig, oneReleaseScript)
	st, _ := LoadReleaseState("proj")
	st.Mark("o/backend", "v1.9.0")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	failPoke := func(string) error { return errors.New("wake channel down") }
	rep, err := RunReleases(root, "proj", false, tell, failPoke)
	if err != nil {
		t.Fatalf("poke failure is best-effort, run must succeed: %v", err)
	}
	if rep.Pokes != 1 || len(*tells) != 1 {
		t.Fatalf("envelope still delivered and counted, got %+v tells=%v", rep, *tells)
	}
	after, _ := LoadReleaseState("proj")
	if tag, _ := after.Tag("o/backend"); tag != "v2.0.0" {
		t.Fatalf("tag must advance despite poke failure, got %q want v2.0.0", tag)
	}
}
