package task

import (
	"errors"
	"strings"
	"testing"
)

func TestWrapForIsolationOff(t *testing.T) {
	// Isolation off: command passes through untouched, and pasta
	// availability is never consulted (missing pasta must not error).
	restore := stubLookPath(func(string) (string, error) {
		t.Fatal("lookPath called while isolation off")
		return "", nil
	})
	defer restore()

	got, err := wrapForIsolation("claude", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "claude" {
		t.Fatalf("got %q, want unchanged %q", got, "claude")
	}
	// The sandbox-bypass env is tied to isolation: with isolation off it
	// must never leak into the worker's command.
	if strings.Contains(got, "IS_SANDBOX") {
		t.Fatalf("got %q, want no IS_SANDBOX when isolation off", got)
	}
}

func TestWrapForIsolationOnPastaPresent(t *testing.T) {
	restore := stubLookPath(func(name string) (string, error) {
		if name != "pasta" {
			t.Fatalf("looked up %q, want pasta", name)
		}
		return "/nix/store/x/bin/pasta", nil
	})
	defer restore()

	got, err := wrapForIsolation("sleep 30", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// pasta --config-net runs the worker in a user namespace mapped to
	// uid 0. claude refuses to run as root, so the netns spawn must inject
	// its sandbox-bypass env (IS_SANDBOX=1) via coreutils `env`. This is
	// legitimate: the worker IS sandboxed (own netns) and uid-0 is only a
	// userns mapping to the unprivileged spore user.
	want := "pasta --config-net -- env IS_SANDBOX=1 sleep 30"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestWrapForIsolationOnPastaMissing(t *testing.T) {
	restore := stubLookPath(func(string) (string, error) {
		return "", errors.New("executable file not found in $PATH")
	})
	defer restore()

	_, err := wrapForIsolation("claude", true)
	if err == nil {
		t.Fatal("want fail-loud error when pasta missing, got nil")
	}
	// Error must name both the binary and how to get it, so an
	// operator can act without reading the source.
	msg := err.Error()
	for _, want := range []string{"pasta", "passt", "isolate_network"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

// stubLookPath swaps the package lookPath indirection for the duration
// of a test and returns a restore func.
func stubLookPath(fn func(string) (string, error)) func() {
	prev := lookPath
	lookPath = fn
	return func() { lookPath = prev }
}
