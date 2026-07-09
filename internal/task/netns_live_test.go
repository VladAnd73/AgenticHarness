package task

import (
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// TestNetnsIsolationLive proves the real wrapForIsolation output gives
// two workers their own netns: both bind the SAME loopback port in
// parallel without EADDRINUSE, which is only possible if each command
// runs in a private network namespace. Self-skips where the mechanism
// is not installed so `just check` stays green on a bare devshell; runs
// for real on the flake-configured host (passt in systemPackages).
func TestNetnsIsolationLive(t *testing.T) {
	if _, err := exec.LookPath("pasta"); err != nil {
		t.Skip("pasta not on PATH; skipping live netns isolation check")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH; skipping live netns isolation check")
	}

	// A worker that binds the fixed port on loopback and reports. With
	// isolation the port is per-netns, so two of these collide only if
	// isolation failed.
	const port = "3000"
	bind := python + ` -c 'import socket; s=socket.socket(); ` +
		`s.bind(("127.0.0.1",` + port + `)); print("BOUND ` + port + `")'`

	wrapped, err := wrapForIsolation("sh -c "+shellSingleQuoteLocal(bind), true)
	if err != nil {
		t.Fatalf("wrapForIsolation: %v", err)
	}

	run := func() (string, error) {
		out, err := exec.Command("sh", "-c", wrapped).CombinedOutput()
		return string(out), err
	}

	var wg sync.WaitGroup
	outs := make([]string, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outs[i], errs[i] = run()
		}(i)
	}
	wg.Wait()

	for i := 0; i < 2; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d failed (isolation breach or missing userns?): %v\noutput: %s", i, errs[i], outs[i])
		}
		if !strings.Contains(outs[i], "BOUND "+port) {
			t.Fatalf("worker %d did not bind :%s: %s", i, port, outs[i])
		}
	}
}

// shellSingleQuoteLocal mirrors the shell single-quote escaping used
// elsewhere in the kernel, kept test-local to avoid exporting a helper
// solely for this file.
func shellSingleQuoteLocal(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
