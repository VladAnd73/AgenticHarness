package watch

import "testing"

// Finding 1: the release-watcher and pr-watcher must not share one state
// file, or their unlocked load-modify-rename cycles clobber each other's
// just-written state during overlapping runs. They must live in separate
// files and each must persist its own writes independently.
func TestReleaseStateIsSeparateFromPRWatch(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	prSt, err := LoadState("proj")
	if err != nil {
		t.Fatal(err)
	}
	prSt.MarkKey(Key(1, "sha", "check"))
	if err := prSt.Save(); err != nil {
		t.Fatal(err)
	}

	relSt, err := LoadReleaseState("proj")
	if err != nil {
		t.Fatal(err)
	}
	relSt.Mark("o/backend", "v2.0.0")
	if err := relSt.Save(); err != nil {
		t.Fatal(err)
	}

	if prSt.path == relSt.path {
		t.Fatalf("pr-watch and release-watch share a state file: %s", prSt.path)
	}

	prAfter, err := LoadState("proj")
	if err != nil {
		t.Fatal(err)
	}
	if !prAfter.SeenKey(Key(1, "sha", "check")) {
		t.Fatal("pr-watch state lost after release-watch wrote its own file")
	}
	relAfter, err := LoadReleaseState("proj")
	if err != nil {
		t.Fatal(err)
	}
	if tag, _ := relAfter.Tag("o/backend"); tag != "v2.0.0" {
		t.Fatalf("release-watch state lost, tag=%q want v2.0.0", tag)
	}
}

// A missing release-watch.json is a clean first run, not an error.
func TestLoadReleaseStateMissingFileIsFirstRun(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	st, err := LoadReleaseState("proj")
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if _, ok := st.Tag("o/backend"); ok {
		t.Fatal("no repo should be seen on first run")
	}
}
