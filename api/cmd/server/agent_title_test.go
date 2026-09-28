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
