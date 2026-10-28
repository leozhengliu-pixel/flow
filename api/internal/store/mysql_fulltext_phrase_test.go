package store

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func longCJKText(length int) string {
	var builder strings.Builder
	for index := 0; index < length; index++ {
		builder.WriteRune(rune(0x4e00 + (index*7919)%5000))
	}
	return builder.String()
}

// A phrase InnoDB would refuse (more than 128 tokens, Error 191) becomes at
// most mysqlPhraseMaxParts required pieces of at most mysqlPhraseMaxRunes
// characters; a phrase that fits is unchanged.
func TestMySQLBooleanPhraseBoundsLongText(t *testing.T) {
	for _, short := range []string{"UAT", "data migration", "车辆市场图片预览组件", strings.Repeat("界", mysqlPhraseMaxRunes)} {
		if got := mysqlBooleanPhrase(short); got != "\""+short+"\"" {
			t.Fatalf("phrase %q = %q", short, got)
		}
	}
	if got := mysqlBooleanPhrase(`say "hi" there`); got != `"say  hi  there"` {
		t.Fatalf("quotes kept: %q", got)
	}
	words := make([]string, 300)
	for index := range words {
		words[index] = []string{"billing", "migration", "staging", "export", "a", "dashboard"}[index%6] + strings.Repeat("x", index%5)
	}
	for name, text := range map[string]string{
		"cjk":     longCJKText(2048),
		"english": strings.Join(words, " "),
		"mixed":   "Release " + longCJKText(300) + " checklist " + strings.Join(words[:40], " "),
	} {
		query := mysqlBooleanPhrase(text)
		parts := strings.Split(strings.TrimPrefix(query, "+\""), "\" +\"")
		parts[len(parts)-1] = strings.TrimSuffix(parts[len(parts)-1], "\"")
		if len(parts) < 2 || len(parts) > mysqlPhraseMaxParts || !strings.HasPrefix(query, "+\"") {
			t.Fatalf("%s: %d parts: %q", name, len(parts), query)
		}
		seen := map[string]bool{}
		for _, part := range parts {
			if size := utf8.RuneCountInString(strings.ReplaceAll(part, " ", "")); size < 2 || size > mysqlPhraseMaxRunes {
				t.Fatalf("%s: part of %d characters: %q", name, size, part)
			}
			if seen[part] || !strings.Contains(text, part) {
				t.Fatalf("%s: part %q repeated or not in the text", name, part)
			}
			seen[part] = true
		}
	}
	if got := mysqlBooleanPhrase(strings.Repeat("界", 2048)); got != "\""+strings.Repeat("界", mysqlPhraseMaxRunes)+"\"" {
		t.Fatalf("repeated text = %q", got)
	}
}
