package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTellWithPokePokesCoordinator(t *testing.T) {
	state := t.TempDir()
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", state)

	var told []string
	tell := func(slug, msg string) error {
		told = append(told, slug+":"+msg)
		return nil
	}

	if err := tellWithPoke("myproj", tell)("coordinator", "alert"); err != nil {
		t.Fatalf("tellWithPoke: %v", err)
	}
	if len(told) != 1 || told[0] != "coordinator:alert" {
		t.Fatalf("tell calls = %v, want [coordinator:alert]", told)
	}
	if n := countJSON(t, filepath.Join(state, "myproj", "inbox")); n != 1 {
		t.Fatalf("poke files = %d, want 1", n)
	}
}

func TestTellWithPokeSkipsNonCoordinator(t *testing.T) {
	state := t.TempDir()
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", state)

	tell := func(slug, msg string) error { return nil }
	if err := tellWithPoke("myproj", tell)("some-worker", "hi"); err != nil {
		t.Fatalf("tellWithPoke: %v", err)
	}
	if n := countJSON(t, filepath.Join(state, "myproj", "inbox")); n != 0 {
		t.Fatalf("poke files = %d, want 0", n)
	}
}

func TestTellWithPokeSkipsPokeOnTellError(t *testing.T) {
	state := t.TempDir()
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", state)

	want := errors.New("boom")
	tell := func(slug, msg string) error { return want }
	if err := tellWithPoke("myproj", tell)("coordinator", "alert"); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if n := countJSON(t, filepath.Join(state, "myproj", "inbox")); n != 0 {
		t.Fatalf("poke files = %d, want 0", n)
	}
}

func countJSON(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	return n
}
