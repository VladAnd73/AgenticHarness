package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const slackThreadTTL = 48 * time.Hour

// SlackReport summarizes a slack-watch run.
type SlackReport struct {
	NewThreads     int
	RepliesRelayed int
	Pruned         int
}

// RunSlack polls the configured Slack channel: new top-level threads and
// new replies on tracked threads are both always told to the project's
// coordinator, never directly to a worker - the coordinator decides
// whether to relay or act itself. When a tracked thread has an active
// worker, the reply message names that worker's slug so the coordinator
// knows who already owns the thread; otherwise it notes no worker is
// assigned. now is injected for deterministic tests. tell delivers a
// message to a slug WITHIN this project (this design is single
// fixed-project, so no cross-project TellProject is needed - matches
// task.Tell's shape). taskActive reports whether slug names a task in
// tasksDir with status "active".
func RunSlack(projectRoot, project string, dryRun bool, now time.Time,
	tell func(slug, msg string) error,
	taskActive func(tasksDir, slug string) (bool, error),
) (SlackReport, error) {
	var rep SlackReport
	cfg, err := LoadSlackConfig(project)
	if err != nil || !cfg.Enabled {
		return rep, err
	}
	st, err := LoadSlackState(project)
	if err != nil {
		return rep, err
	}

	if st.Cursor == "" {
		// First run: seed to now, relay nothing from prior history (no
		// backlog sweep - locked decision).
		if !dryRun {
			st.Cursor = fmt.Sprintf("%d.000000", now.Unix())
			if err := st.Save(); err != nil {
				return rep, err
			}
		}
		return rep, nil
	}

	tasksDir := filepath.Join(projectRoot, "tasks")
	dirty := false

	msgs, err := ConversationsHistory(cfg.ChannelID, st.Cursor)
	if err != nil {
		return rep, err
	}
	// maxTs only advances past a message once it is fully, successfully
	// handled. Advancing past a message whose tell failed would be worse
	// than re-telling it: Slack's oldest= excludes messages at or before
	// the cursor, so that message would never be fetched again and its
	// thread would be silently dropped rather than retried next poll.
	maxTs := st.Cursor
	for _, m := range msgs {
		if !m.IsThreadRoot() {
			if m.Ts > maxTs {
				maxTs = m.Ts
			}
			continue // a reply that was also posted to the channel
		}
		permalink, perr := Permalink(cfg.ChannelID, m.Ts)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "slack-watch: permalink for %s: %v\n", m.Ts, perr)
		}
		msg := formatNewThread(m, permalink)
		if !dryRun {
			if err := tell("coordinator", msg); err != nil {
				if maxTs != st.Cursor {
					st.Cursor = maxTs
				}
				_ = st.Save()
				return rep, err
			}
			st.Threads[m.Ts] = SlackThread{LastActivity: now, LastReplyTS: m.Ts}
			dirty = true
		}
		if m.Ts > maxTs {
			maxTs = m.Ts
		}
		rep.NewThreads++
	}
	if maxTs != st.Cursor {
		st.Cursor = maxTs
		dirty = true
	}

	for ts, th := range st.Threads {
		if now.Sub(th.LastActivity) > slackThreadTTL {
			delete(st.Threads, ts)
			rep.Pruned++
			dirty = true
			continue
		}
		replies, rerr := ConversationsReplies(cfg.ChannelID, ts, th.LastReplyTS)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "slack-watch: replies for %s: %v\n", ts, rerr)
			continue
		}
		for _, r := range replies {
			if r.Ts == ts {
				continue // conversations.replies includes the thread root
			}
			active := false
			if th.TaskSlug != "" {
				active, err = taskActive(tasksDir, th.TaskSlug)
				if err != nil {
					fmt.Fprintf(os.Stderr, "slack-watch: task status for %s: %v\n", th.TaskSlug, err)
				}
			}
			if !dryRun {
				if err := tell("coordinator", formatReply(r, ts, active, th.TaskSlug)); err != nil {
					st.Threads[ts] = th
					_ = st.Save()
					return rep, err
				}
			}
			rep.RepliesRelayed++
			th.LastReplyTS = r.Ts
			th.LastActivity = now
			dirty = true
		}
		st.Threads[ts] = th
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

func formatNewThread(m SlackMessage, permalink string) string {
	link := permalink
	if link == "" {
		link = "(permalink unavailable)"
	}
	return fmt.Sprintf("New Slack thread from %s: %q - %s", m.User, m.Text, link)
}

func formatReply(r SlackMessage, threadTS string, activeWorker bool, taskSlug string) string {
	note := " (no active worker for this thread)"
	if activeWorker {
		note = fmt.Sprintf(" (worker %q is on this thread)", taskSlug)
	}
	return fmt.Sprintf("Slack update on thread %s from %s: %q%s", threadTS, r.User, r.Text, note)
}
