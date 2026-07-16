package task

import (
	"errors"
	"testing"
)

// These unit tests pin the skip-vs-fail decision that TestNetnsIsolationLive
// relies on, driving each host situation through a stubbed command runner so
// the three acceptance scenarios are deterministic (no real pasta / userns).

func TestClassifyNetnsLiveSkipsWhenNamespaceSetupFails(t *testing.T) {
	// Scenario 1: the probe command errors, so this host cannot build a
	// namespace at all (e.g. userns unavailable on a CI runner). The live
	// test must SKIP, not fail.
	run := func(wrapped string) ([]byte, error) {
		if wrapped == "probe" {
			return []byte("no usable userns"), errors.New("pasta setup failed")
		}
		t.Fatalf("worker command ran after a failed probe: %q", wrapped)
		return nil, nil
	}

	result, reason := classifyNetnsLive(run, "probe", "worker", "3000")
	if result != liveSkip {
		t.Fatalf("got result %v, want liveSkip", result)
	}
	if reason == "" {
		t.Fatal("skip must carry a reason so the operator sees why")
	}
}

func TestClassifyNetnsLivePassesWhenIsolationHolds(t *testing.T) {
	// Scenario 2: probe succeeds and both workers bind the port, which is
	// only possible if each ran in its own netns. The live test passes.
	run := func(wrapped string) ([]byte, error) {
		if wrapped == "probe" {
			return []byte(""), nil
		}
		return []byte("BOUND 3000\n"), nil
	}

	result, reason := classifyNetnsLive(run, "probe", "worker", "3000")
	if result != livePass {
		t.Fatalf("got result %v (reason %q), want livePass", result, reason)
	}
}

func TestClassifyNetnsLiveFailsWhenIsolationBreached(t *testing.T) {
	// Scenario 3 (the guard): the probe succeeds, so the mechanism works
	// here, but a worker errors (EADDRINUSE from two workers sharing one
	// netns). That is a real regression and must FAIL, never skip.
	run := func(wrapped string) ([]byte, error) {
		if wrapped == "probe" {
			return []byte(""), nil
		}
		return []byte("OSError: [Errno 98] Address already in use"),
			errors.New("exit status 1")
	}

	result, _ := classifyNetnsLive(run, "probe", "worker", "3000")
	if result != liveFail {
		t.Fatalf("got result %v, want liveFail", result)
	}
}
