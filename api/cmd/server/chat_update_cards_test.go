package main

import (
	"math"
	"testing"
)

func TestChatOrderedListStartRejectsInvalidFloatValues(t *testing.T) {
	list := func(start any) string {
		return (chatMarkdownContext{}).block(map[string]any{
			"type":  "orderedList",
			"attrs": map[string]any{"start": start},
			"content": []any{map[string]any{
				"type": "listItem",
				"content": []any{map[string]any{
					"type":    "paragraph",
					"content": []any{map[string]any{"type": "text", "text": "item"}},
				}},
			}},
		}, "")
	}

	for _, test := range []struct {
		name, want string
		start      any
	}{
		{name: "valid", start: float64(3), want: "3. item"},
		{name: "fractional", start: 2.5, want: "1. item"},
		{name: "oversized", start: math.MaxFloat64, want: "1. item"},
		{name: "past 32-bit", start: float64(math.MaxInt32) + 1, want: "1. item"},
		{name: "largest 32-bit", start: float64(math.MaxInt32), want: "2147483647. item"},
		{name: "not a number", start: math.NaN(), want: "1. item"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := list(test.start); got != test.want {
				t.Fatalf("ordered list = %q, want %q", got, test.want)
			}
		})
	}
}
