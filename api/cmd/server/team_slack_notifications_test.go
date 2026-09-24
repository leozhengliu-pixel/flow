package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSlackStateChanged(t *testing.T) {
	cases := map[string]bool{
		``:                                 false,
		`{"title":"Old title"}`:            false,
		`{"state":{"id":"state_todo"}}`:    true,
		`{"state":{"id":"a"},"title":"x"}`: true,
		`not json`:                         false,
	}
	for input, want := range cases {
		if got := slackStateChanged(json.RawMessage(input)); got != want {
			t.Errorf("slackStateChanged(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestSlackLinkEscapesReservedCharacters(t *testing.T) {
	got := slackLink("https://flow.example/ws/issue/ENG-1", "ENG-1 Fix <b> & more")
	want := "<https://flow.example/ws/issue/ENG-1|ENG-1 Fix &lt;b&gt; &amp; more>"
	if got != want {
		t.Fatalf("slackLink = %q, want %q", got, want)
	}
}

func TestPostSlackMessage(t *testing.T) {
	var received struct {
		auth    string
		channel string
		text    string
	}
	slack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			http.NotFound(w, r)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		received.auth, received.channel, received.text = r.Header.Get("Authorization"), body["channel"], body["text"]
		if body["channel"] == "C-missing" {
			_, _ = w.Write([]byte(`{"ok":false,"error":"channel_not_found"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer slack.Close()
	t.Setenv("FLOW_SLACK_API_URL", slack.URL)

	if err := postSlackMessage(context.Background(), "xoxb-test", "C123", "hello"); err != nil {
		t.Fatalf("postSlackMessage: %v", err)
	}
	if received.auth != "Bearer xoxb-test" || received.channel != "C123" || received.text != "hello" {
		t.Fatalf("unexpected request: %+v", received)
	}
	if err := postSlackMessage(context.Background(), "xoxb-test", "C-missing", "hello"); err == nil || err.Error() != "slack: channel_not_found" {
		t.Fatalf("expected channel_not_found error, got %v", err)
	}
}
