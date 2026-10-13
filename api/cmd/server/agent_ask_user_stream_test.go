package main

import "testing"

// The question of an ask_user call streams while its arguments are still
// arriving: the partial string value is read before its closing quote.
func TestPartialJSONStringField(t *testing.T) {
	cases := map[string]string{
		``:                                      "",
		`{"ques`:                                "",
		`{"question"`:                           "",
		`{"question": "`:                        "",
		`{"question": "我需要确认`:                   "我需要确认",
		`{"question":"Which \"date\"\nfor`:      "Which \"date\"\nfor",
		`{"question":"done","options":[{"label`: "done",
		`{"options":[{"label":"\"question\""}],"question":"after"`: "after",
		`{"question":"emoji 😀 ok`:                                  "emoji 😀 ok",
		`{"question":"cut \ud83d`:                                  "cut ",
		`{"question":"cut \u00`:                                    "cut ",
		`{"question":"trailing \`:                                  "trailing ",
	}
	for raw, want := range cases {
		if got := partialJSONStringField([]byte(raw), "question"); got != want {
			t.Errorf("partialJSONStringField(%q) = %q, want %q", raw, got, want)
		}
	}
}
