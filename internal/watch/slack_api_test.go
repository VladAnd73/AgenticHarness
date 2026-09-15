package watch

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// captureStderr redirects os.Stderr to a pipe for the duration of fn and
// returns what was written. Used to assert on the has_more truncation
// warning, which is deliberately visible-not-silent (see
// ConversationsHistory's doc comment).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	fn()
	_ = w.Close()
	os.Stderr = orig
	b, _ := io.ReadAll(r)
	return string(b)
}

// fakeSlack starts an httptest.Server that answers Slack Web API calls from
// a method -> JSON-body map, and points SPORE_SLACK_API_BASE +
// SLACK_BOT_TOKEN at it. There is no Slack CLI to shell out to (unlike gh),
// so this is the seam: an overridable base URL, mirroring SPORE_GH_BINARY
// in spirit.
func fakeSlack(t *testing.T, responses map[string]string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/")
		body, ok := responses[method]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SPORE_SLACK_API_BASE", srv.URL)
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-test-token")
}

func TestConversationsHistoryParsesMessages(t *testing.T) {
	fakeSlack(t, map[string]string{
		"conversations.history": `{"ok":true,"has_more":false,"messages":[
			{"ts":"1000.0001","user":"U1","text":"first"},
			{"ts":"1000.0002","user":"U2","text":"second","thread_ts":"1000.0002"}
		]}`,
	})
	msgs, err := ConversationsHistory("C1", "999.0000")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if !msgs[0].IsThreadRoot() {
		t.Fatal("message with no thread_ts must be a thread root")
	}
	if !msgs[1].IsThreadRoot() {
		t.Fatal("message whose thread_ts equals its own ts must be a thread root")
	}
}

func TestConversationsHistoryReplyAlsoInChannelIsNotARoot(t *testing.T) {
	fakeSlack(t, map[string]string{
		"conversations.history": `{"ok":true,"has_more":false,"messages":[
			{"ts":"1000.0005","user":"U1","text":"also sent to channel","thread_ts":"1000.0001"}
		]}`,
	})
	msgs, err := ConversationsHistory("C1", "999.0000")
	if err != nil {
		t.Fatal(err)
	}
	if msgs[0].IsThreadRoot() {
		t.Fatal("a reply whose thread_ts differs from its own ts must not be a thread root")
	}
}

func TestConversationsRepliesParsesMessages(t *testing.T) {
	fakeSlack(t, map[string]string{
		"conversations.replies": `{"ok":true,"messages":[
			{"ts":"1000.0001","user":"U1","text":"root","thread_ts":"1000.0001"},
			{"ts":"1000.0003","user":"U2","text":"a reply","thread_ts":"1000.0001"}
		]}`,
	})
	msgs, err := ConversationsReplies("C1", "1000.0001", "1000.0001")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
}

func TestPermalinkReturnsURL(t *testing.T) {
	fakeSlack(t, map[string]string{
		"chat.getPermalink": `{"ok":true,"permalink":"https://homekeytechnologies.slack.com/archives/C1/p10000001"}`,
	})
	link, err := Permalink("C1", "1000.0001")
	if err != nil {
		t.Fatal(err)
	}
	if link != "https://homekeytechnologies.slack.com/archives/C1/p10000001" {
		t.Fatalf("got %q", link)
	}
}

func TestSlackAPIErrorSurfacesCode(t *testing.T) {
	fakeSlack(t, map[string]string{
		"conversations.history": `{"ok":false,"error":"channel_not_found"}`,
	})
	_, err := ConversationsHistory("C1", "999.0000")
	if err == nil || !strings.Contains(err.Error(), "channel_not_found") {
		t.Fatalf("want error containing channel_not_found, got %v", err)
	}
}

func TestSlackAPIMissingTokenErrors(t *testing.T) {
	t.Setenv("SLACK_BOT_TOKEN", "")
	t.Setenv("SPORE_SLACK_API_BASE", "http://127.0.0.1:1") // unreachable, must not even be dialed
	_, err := ConversationsHistory("C1", "999.0000")
	if err == nil || !strings.Contains(err.Error(), "SLACK_BOT_TOKEN") {
		t.Fatalf("want SLACK_BOT_TOKEN error, got %v", err)
	}
}

// A non-2xx response (rate limit, outage) commonly has a non-JSON body, so
// the status code - not just a "bad json" parse error - must surface, or a
// 429 vs a 503 vs a genuine parse bug are indistinguishable from the text.
func TestSlackAPINon2xxStatusSurfacesStatusCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, "rate limited")
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SPORE_SLACK_API_BASE", srv.URL)
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-test-token")

	_, err := ConversationsHistory("C1", "999.0000")
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("want error containing status code 429, got %v", err)
	}
}

func TestConversationsHistoryHasMoreWarnsAndReturnsFirstPage(t *testing.T) {
	fakeSlack(t, map[string]string{
		"conversations.history": `{"ok":true,"has_more":true,"messages":[
			{"ts":"1000.0001","user":"U1","text":"first"}
		]}`,
	})
	var msgs []SlackMessage
	var err error
	stderr := captureStderr(t, func() {
		msgs, err = ConversationsHistory("C1", "999.0000")
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Ts != "1000.0001" {
		t.Fatalf("got %+v, want the first page's message returned", msgs)
	}
	if !strings.Contains(stderr, "C1") {
		t.Fatalf("want a has_more warning mentioning the channel, got %q", stderr)
	}
}
