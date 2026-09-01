package dream

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Slice struct {
	Kind string
	TS   time.Time
	Text string
}

type SessionDigest struct {
	Session          Session
	Brief            string
	OperatorMessages []Slice
	Failures         []Slice
	RepeatedCommands []Slice
	Denials          []Slice
	FinalReport      string
	End              string
	Score            int
	DeepRead         bool
}

// digestEntry carries the provenance fields rawEntry omits. Most
// text-bearing user entries in a real transcript are harness injections
// (skill bodies, task notifications, Stop-hook feedback) rather than the
// operator speaking, and only isMeta and promptSource tell them apart.
type digestEntry struct {
	Type         string          `json:"type"`
	Timestamp    string          `json:"timestamp"`
	IsMeta       bool            `json:"isMeta"`
	PromptSource string          `json:"promptSource"`
	Message      json.RawMessage `json:"message"`
}

// BuildDigest keeps only the slices that carry a lesson: what the
// operator said, what failed, what was retried, what was denied. The
// bulk of a transcript is successful tool calls and reasoning, which is
// dropped so a night's worth of sessions fits in a model's context.
func BuildDigest(s Session, repeatThreshold int) (SessionDigest, error) {
	f, err := os.Open(s.Path)
	if err != nil {
		return SessionDigest{}, err
	}
	defer f.Close()

	d := SessionDigest{Session: s, End: "unknown"}
	cmdCounts := map[string]int{}
	var lastAssistant string
	sawWrapUp := false

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var e digestEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		ts, _ := time.Parse(time.RFC3339, e.Timestamp)
		switch e.Type {
		case "user":
			if errText, isErr := toolError(e.Message); isErr {
				if denied(errText) {
					d.Denials = append(d.Denials,
						Slice{Kind: "denial", TS: ts, Text: errText})
				} else {
					d.Failures = append(d.Failures,
						Slice{Kind: "tool-error", TS: ts, Text: errText})
				}
				continue
			}
			text := messageText(e.Message)
			if strings.TrimSpace(text) == "" {
				continue
			}
			if wrapUp(text) {
				sawWrapUp = true
			}
			if denied(text) {
				d.Denials = append(d.Denials, Slice{Kind: "denial", TS: ts, Text: text})
				continue
			}
			if !operatorAuthored(e) {
				continue
			}
			if d.Brief == "" {
				d.Brief = text
				continue
			}
			d.OperatorMessages = append(d.OperatorMessages,
				Slice{Kind: "operator", TS: ts, Text: text})
		case "assistant":
			if cmd, ok := bashCommand(e.Message); ok {
				cmdCounts[cmd]++
			}
			if t := messageText(e.Message); strings.TrimSpace(t) != "" {
				lastAssistant = t
			}
		}
	}
	d.FinalReport = lastAssistant
	d.End = endState(lastAssistant, sawWrapUp)
	for cmd, n := range cmdCounts {
		if n >= repeatThreshold {
			d.RepeatedCommands = append(d.RepeatedCommands,
				Slice{Kind: "repeated", Text: fmt.Sprintf("%dx %s", n, cmd)})
		}
	}
	sort.Slice(d.RepeatedCommands, func(i, j int) bool {
		return d.RepeatedCommands[i].Text < d.RepeatedCommands[j].Text
	})
	return d, nil
}

// operatorAuthored reports whether a human typed the entry. Harness
// injections are marked isMeta, or carry promptSource "system"; an entry
// with neither marker is the operator's own prompt.
func operatorAuthored(e digestEntry) bool {
	return !e.IsMeta && e.PromptSource != "system"
}

// endState reads how the session finished. It feeds scoring: a session
// that ended blocked or ran out of context is far likelier to hold a
// lesson than one that simply finished.
func endState(finalReport string, sawWrapUp bool) string {
	switch {
	case strings.Contains(finalReport, "BLOCKED"):
		return "blocked"
	case sawWrapUp:
		return "tokens"
	case strings.Contains(finalReport, "DONE"):
		return "done"
	}
	return "unknown"
}

// wrapUp matches what the token monitors actually emit. Both the worker
// and coordinator messages carry "TOKEN MONITOR"; the other two spellings
// cover older reminder wording.
func wrapUp(text string) bool {
	return strings.Contains(text, "TOKEN MONITOR") ||
		strings.Contains(text, "wrap-up") ||
		strings.Contains(text, "token threshold")
}

func toolError(raw json.RawMessage) (string, bool) {
	var m struct {
		Content []struct {
			Type    string `json:"type"`
			IsError bool   `json:"is_error"`
			Content any    `json:"content"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return "", false
	}
	for _, c := range m.Content {
		if c.Type == "tool_result" && c.IsError {
			return fmt.Sprint(c.Content), true
		}
	}
	return "", false
}

func bashCommand(raw json.RawMessage) (string, bool) {
	var m struct {
		Content []struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Input struct {
				Command string `json:"command"`
			} `json:"input"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return "", false
	}
	for _, c := range m.Content {
		if c.Type == "tool_use" && c.Name == "Bash" && c.Input.Command != "" {
			return c.Input.Command, true
		}
	}
	return "", false
}

// denied matches both an operator refusing a tool call and a hook
// blocking one. Real transcripts deliver either as an is_error
// tool_result, not as user text, so this is applied to both.
func denied(text string) bool {
	return strings.Contains(text, "permission to use") ||
		strings.Contains(text, "requested permissions") ||
		strings.Contains(text, "doesn't want to proceed with this tool use") ||
		strings.Contains(text, "blocked by hook") ||
		strings.Contains(text, "hook feedback") ||
		strings.Contains(text, "must run in a tmux window")
}

// FormatDigest renders the digests as the markdown the proposer reads.
func FormatDigest(ds []SessionDigest) string {
	var b strings.Builder
	for _, d := range ds {
		fmt.Fprintf(&b, "## %s / %s (%s)\n\n", d.Session.Project,
			d.Session.Slug, d.Session.Kind)
		fmt.Fprintf(&b, "session: %s\nended: %s\ndeep-read: %v\n\n",
			filepath.Base(d.Session.Path), d.End, d.DeepRead)
		writeSlices(&b, "Operator messages", d.OperatorMessages)
		writeSlices(&b, "Failures", d.Failures)
		writeSlices(&b, "Repeated commands", d.RepeatedCommands)
		writeSlices(&b, "Denials", d.Denials)
	}
	return b.String()
}

func writeSlices(b *strings.Builder, title string, ss []Slice) {
	if len(ss) == 0 {
		return
	}
	fmt.Fprintf(b, "### %s\n\n", title)
	for _, s := range ss {
		fmt.Fprintf(b, "- [%s] %s\n", s.TS.Format(time.RFC3339), oneLine(s.Text))
	}
	b.WriteString("\n")
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 500 {
		return s[:500] + " ..."
	}
	return s
}
