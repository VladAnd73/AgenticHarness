package watch

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type slackTold struct{ slug, msg string }

func setupSlack(t *testing.T, cfgBody string, slackResponses map[string]string) (root string, tells *[]slackTold, tell func(string, string) error) {
	t.Helper()
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	writeWatchToml(t, cfgDir, "proj", cfgBody)
	fakeSlack(t, slackResponses)
	var got []slackTold
	return t.TempDir(), &got, func(slug, msg string) error {
		got = append(got, slackTold{slug, msg})
		return nil
	}
}

const oneChannelConfig = `
[slack]
enabled = true
channel_id = "C1"
`

func alwaysInactive(tasksDir, slug string) (bool, error) { return false, nil }

// Scenario 2: first-run seeding. No prior state -> seed cursor to now,
// relay nothing, and never call the Slack API (no responses registered).
func TestRunSlackFirstRunSeeds(t *testing.T) {
	root, tells, tell := setupSlack(t, oneChannelConfig, map[string]string{})
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	rep, err := RunSlack(root, "proj", false, now, tell, alwaysInactive)
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewThreads != 0 || rep.RepliesRelayed != 0 || len(*tells) != 0 {
		t.Fatalf("first run must relay nothing, got %+v tells=%v", rep, *tells)
	}
	st, err := LoadSlackState("proj")
	if err != nil {
		t.Fatal(err)
	}
	if st.Cursor == "" {
		t.Fatal("cursor must be seeded on first run")
	}
}

// Scenario 1 (end to end) + Scenario 10 (reply-also-in-channel ignored).
func TestRunSlackNewThreadEndToEnd(t *testing.T) {
	root, tells, tell := setupSlack(t, oneChannelConfig, map[string]string{
		"conversations.history": `{"ok":true,"has_more":false,"messages":[
			{"ts":"1000.0002","user":"U1","text":"login is broken"},
			{"ts":"1000.0003","user":"U2","text":"also sent to channel","thread_ts":"1000.0001"}
		]}`,
		"chat.getPermalink": `{"ok":true,"permalink":"https://slack.example/p1"}`,
	})
	// Seed a non-empty cursor so this is treated as a normal (not first) run.
	st, _ := LoadSlackState("proj")
	st.Cursor = "1000.0000"
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	rep, err := RunSlack(root, "proj", false, now, tell, alwaysInactive)
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewThreads != 1 {
		t.Fatalf("want 1 new thread (the also-in-channel reply must be ignored), got %+v", rep)
	}
	if len(*tells) != 1 || (*tells)[0].slug != "coordinator" {
		t.Fatalf("want one tell to coordinator, got %v", *tells)
	}
	for _, want := range []string{"login is broken", "https://slack.example/p1"} {
		if !strings.Contains((*tells)[0].msg, want) {
			t.Fatalf("msg missing %q:\n%s", want, (*tells)[0].msg)
		}
	}
	after, _ := LoadSlackState("proj")
	if after.Cursor != "1000.0003" {
		t.Fatalf("cursor = %q, want advanced to 1000.0003 (max ts seen)", after.Cursor)
	}
	if _, ok := after.Threads["1000.0002"]; !ok {
		t.Fatal("new thread must be tracked")
	}
	if _, ok := after.Threads["1000.0003"]; ok {
		t.Fatal("the also-in-channel reply must not be tracked as its own thread")
	}
}

// Scenario 4: reply routed to coordinator when no active worker.
func TestRunSlackReplyNoActiveWorkerGoesToCoordinator(t *testing.T) {
	root, tells, tell := setupSlack(t, oneChannelConfig, map[string]string{
		"conversations.history": `{"ok":true,"has_more":false,"messages":[]}`,
		"conversations.replies": `{"ok":true,"messages":[
			{"ts":"1000.0001","user":"U1","text":"root","thread_ts":"1000.0001"},
			{"ts":"1000.0005","user":"U2","text":"any update?","thread_ts":"1000.0001"}
		]}`,
	})
	st, _ := LoadSlackState("proj")
	st.Cursor = "1000.0001"
	st.Threads["1000.0001"] = SlackThread{LastActivity: time.Date(2026, 9, 15, 11, 0, 0, 0, time.UTC), LastReplyTS: "1000.0001"}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	rep, err := RunSlack(root, "proj", false, now, tell, alwaysInactive)
	if err != nil {
		t.Fatal(err)
	}
	if rep.RepliesRelayed != 1 {
		t.Fatalf("want 1 reply relayed, got %+v", rep)
	}
	if len(*tells) != 1 || (*tells)[0].slug != "coordinator" {
		t.Fatalf("want one tell to coordinator, got %v", *tells)
	}
	if !strings.Contains((*tells)[0].msg, "no active worker") {
		t.Fatalf("msg must note no active worker:\n%s", (*tells)[0].msg)
	}
}

// Scenario 3: reply routed to the active worker, not the coordinator.
func TestRunSlackReplyActiveWorkerGoesToWorker(t *testing.T) {
	root, tells, tell := setupSlack(t, oneChannelConfig, map[string]string{
		"conversations.history": `{"ok":true,"has_more":false,"messages":[]}`,
		"conversations.replies": `{"ok":true,"messages":[
			{"ts":"1000.0005","user":"U2","text":"any update?","thread_ts":"1000.0001"}
		]}`,
	})
	st, _ := LoadSlackState("proj")
	st.Cursor = "1000.0001"
	st.Threads["1000.0001"] = SlackThread{
		TaskSlug:     "investigate-login-bug",
		LastActivity: time.Date(2026, 9, 15, 11, 0, 0, 0, time.UTC),
		LastReplyTS:  "1000.0001",
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	alwaysActive := func(tasksDir, slug string) (bool, error) { return slug == "investigate-login-bug", nil }
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	rep, err := RunSlack(root, "proj", false, now, tell, alwaysActive)
	if err != nil {
		t.Fatal(err)
	}
	if rep.RepliesRelayed != 1 {
		t.Fatalf("want 1 reply relayed, got %+v", rep)
	}
	if len(*tells) != 1 || (*tells)[0].slug != "investigate-login-bug" {
		t.Fatalf("want one tell to investigate-login-bug, got %v", *tells)
	}
}

// Scenario 5 + 6: idle prune, and a reply on a pruned thread is not relayed.
func TestRunSlackIdlePruneStopsTracking(t *testing.T) {
	root, tells, tell := setupSlack(t, oneChannelConfig, map[string]string{
		"conversations.history": `{"ok":true,"has_more":false,"messages":[]}`,
	})
	st, _ := LoadSlackState("proj")
	st.Cursor = "1000.0001"
	st.Threads["1000.0001"] = SlackThread{LastActivity: time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC), LastReplyTS: "1000.0001"}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) // 60h later, past the 48h TTL
	rep, err := RunSlack(root, "proj", false, now, tell, alwaysInactive)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pruned != 1 {
		t.Fatalf("want 1 pruned, got %+v", rep)
	}
	if len(*tells) != 0 {
		t.Fatalf("a pruned thread must not be polled for replies, got tells=%v", *tells)
	}
	after, _ := LoadSlackState("proj")
	if _, ok := after.Threads["1000.0001"]; ok {
		t.Fatal("pruned thread must be removed from state")
	}
}

// Scenario 7: coordinator-set mapping (via SetTaskSlug, as the
// slack-set-thread CLI in Task 6 will call it) survives to the next run.
func TestRunSlackHonorsMappingSetBetweenRuns(t *testing.T) {
	root, tells, tell := setupSlack(t, oneChannelConfig, map[string]string{
		"conversations.history": `{"ok":true,"has_more":false,"messages":[]}`,
		"conversations.replies": `{"ok":true,"messages":[
			{"ts":"1000.0005","user":"U2","text":"update","thread_ts":"1000.0001"}
		]}`,
	})
	st, _ := LoadSlackState("proj")
	st.Cursor = "1000.0001"
	st.Threads["1000.0001"] = SlackThread{LastActivity: time.Date(2026, 9, 15, 11, 0, 0, 0, time.UTC), LastReplyTS: "1000.0001"}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	// Simulate the coordinator running slack-set-thread between polls.
	between, _ := LoadSlackState("proj")
	between.SetTaskSlug("1000.0001", "investigate-login-bug")
	if err := between.Save(); err != nil {
		t.Fatal(err)
	}
	alwaysActive := func(tasksDir, slug string) (bool, error) { return true, nil }
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	if _, err := RunSlack(root, "proj", false, now, tell, alwaysActive); err != nil {
		t.Fatal(err)
	}
	if len(*tells) != 1 || (*tells)[0].slug != "investigate-login-bug" {
		t.Fatalf("want tell routed to the mapping set between runs, got %v", *tells)
	}
}

// Scenario 8: disabled config is a no-op.
func TestRunSlackDisabledIsNoOp(t *testing.T) {
	root, tells, tell := setupSlack(t, `
[slack]
enabled = false
channel_id = "C1"
`, map[string]string{})
	rep, err := RunSlack(root, "proj", false, time.Now(), tell, alwaysInactive)
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewThreads != 0 || len(*tells) != 0 {
		t.Fatalf("disabled run must do nothing, got %+v", rep)
	}
}

// Scenario 9: dry-run reports intent, writes no state, sends nothing.
func TestRunSlackDryRun(t *testing.T) {
	root, tells, tell := setupSlack(t, oneChannelConfig, map[string]string{
		"conversations.history": `{"ok":true,"has_more":false,"messages":[
			{"ts":"1000.0002","user":"U1","text":"login is broken"}
		]}`,
		"chat.getPermalink": `{"ok":true,"permalink":"https://slack.example/p1"}`,
	})
	st, _ := LoadSlackState("proj")
	st.Cursor = "1000.0000"
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	rep, err := RunSlack(root, "proj", true, now, tell, alwaysInactive)
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewThreads != 1 {
		t.Fatalf("dry-run must still report intent, got %+v", rep)
	}
	if len(*tells) != 0 {
		t.Fatalf("dry-run must send nothing, got %v", *tells)
	}
	after, _ := LoadSlackState("proj")
	if after.Cursor != "1000.0000" {
		t.Fatalf("dry-run must not write state, cursor=%q want 1000.0000", after.Cursor)
	}
}

// A tell failure on a new thread must surface as a run error (mirrors
// RunReleases' behavior: an envelope failing to write is not swallowed).
func TestRunSlackTellFailurePropagates(t *testing.T) {
	root, _, _ := setupSlack(t, oneChannelConfig, map[string]string{
		"conversations.history": `{"ok":true,"has_more":false,"messages":[
			{"ts":"1000.0002","user":"U1","text":"login is broken"}
		]}`,
		"chat.getPermalink": `{"ok":true,"permalink":"https://slack.example/p1"}`,
	})
	st, _ := LoadSlackState("proj")
	st.Cursor = "1000.0000"
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	failTell := func(string, string) error { return errors.New("inbox unwritable") }
	_, err := RunSlack(root, "proj", false, time.Now(), failTell, alwaysInactive)
	if err == nil {
		t.Fatal("want error when tell fails")
	}
}
