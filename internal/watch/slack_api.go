package watch

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

// SlackMessage is the subset of Slack's message JSON the watcher needs.
type SlackMessage struct {
	Ts       string `json:"ts"`
	ThreadTS string `json:"thread_ts,omitempty"`
	User     string `json:"user"`
	Text     string `json:"text"`
}

// IsThreadRoot reports whether m is a genuine new thread rather than a
// reply that was also posted to the channel (Slack's "also send to
// channel" option gives a reply a thread_ts that differs from its own ts).
func (m SlackMessage) IsThreadRoot() bool {
	return m.ThreadTS == "" || m.ThreadTS == m.Ts
}

type slackError struct {
	method string
	code   string
}

func (e *slackError) Error() string {
	return fmt.Sprintf("slack %s: %s", e.method, e.code)
}

// slackAPIBase is the Slack Web API base URL, overridable via
// SPORE_SLACK_API_BASE for tests. There is no Slack CLI to shell out to
// (unlike gh's SPORE_GH_BINARY), so this env-var-swappable base URL is the
// equivalent seam.
func slackAPIBase() string {
	if b := os.Getenv("SPORE_SLACK_API_BASE"); b != "" {
		return b
	}
	return "https://slack.com/api"
}

func slackGet(method string, query url.Values, out any) error {
	token := os.Getenv("SLACK_BOT_TOKEN")
	if token == "" {
		return fmt.Errorf("slack %s: SLACK_BOT_TOKEN not set", method)
	}
	u := slackAPIBase() + "/" + method
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("slack %s: %w", method, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack %s: %w", method, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("slack %s: %w", method, err)
	}
	var envelope struct {
		Ok    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		return fmt.Errorf("slack %s: bad json: %w", method, err)
	}
	if !envelope.Ok {
		return &slackError{method: method, code: envelope.Error}
	}
	return json.Unmarshal(b, out)
}

// ConversationsHistory returns channel messages strictly after oldest (a
// Slack ts string). Does not paginate: a response with has_more=true logs a
// warning and returns only the first page - a 1-minute poll interval should
// rarely accumulate more than one page between runs, and silent truncation
// would be worse than a visible warning.
func ConversationsHistory(channel, oldest string) ([]SlackMessage, error) {
	q := url.Values{"channel": {channel}}
	if oldest != "" {
		q.Set("oldest", oldest)
	}
	var resp struct {
		Messages []SlackMessage `json:"messages"`
		HasMore  bool           `json:"has_more"`
	}
	if err := slackGet("conversations.history", q, &resp); err != nil {
		return nil, err
	}
	if resp.HasMore {
		fmt.Fprintf(os.Stderr, "slack-watch: conversations.history for %s has more than one page; only the first page was read\n", channel)
	}
	return resp.Messages, nil
}

// ConversationsReplies returns messages on threadTS strictly after oldest,
// INCLUDING the thread root itself (callers that already have the root
// must filter it out by ts).
func ConversationsReplies(channel, threadTS, oldest string) ([]SlackMessage, error) {
	q := url.Values{"channel": {channel}, "ts": {threadTS}}
	if oldest != "" {
		q.Set("oldest", oldest)
	}
	var resp struct {
		Messages []SlackMessage `json:"messages"`
	}
	if err := slackGet("conversations.replies", q, &resp); err != nil {
		return nil, err
	}
	return resp.Messages, nil
}

// Permalink returns the Slack permalink for one message.
func Permalink(channel, ts string) (string, error) {
	q := url.Values{"channel": {channel}, "message_ts": {ts}}
	var resp struct {
		Permalink string `json:"permalink"`
	}
	if err := slackGet("chat.getPermalink", q, &resp); err != nil {
		return "", err
	}
	return resp.Permalink, nil
}
