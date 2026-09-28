package main

import "testing"

func TestCleanAgentSessionTitle(t *testing.T) {
	cases := map[string]string{
		"\"Summarize Compare Test Project Status.\"": "Summarize Compare Test Project Status",
		"# 本周站会总结。\nextra line":                      "本周站会总结",
		"  ":                                         "",
	}
	for input, want := range cases {
		if got := cleanAgentSessionTitle(input); got != want {
			t.Fatalf("cleanAgentSessionTitle(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLeakedProgressSteps(t *testing.T) {
	steps, rest := leakedProgressSteps("{\"title\":\"Gathering weekly activity\",\"message\":\"I'll review it.\"}{\"title\":\"Checking history…\"}## Summary\nDone")
	if len(steps) != 2 || steps[0].Title != "Gathering weekly activity" || steps[0].Message != "I'll review it." || steps[1].Title != "Checking history" || rest != "## Summary\nDone" {
		t.Fatalf("unexpected: %+v %q", steps, rest)
	}
	if steps, rest := leakedProgressSteps(`{"name":"x"} text`); steps != nil || rest != `{"name":"x"} text` {
		t.Fatalf("non-progress JSON must be kept: %+v %q", steps, rest)
	}
}
