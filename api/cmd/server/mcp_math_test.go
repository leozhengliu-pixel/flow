package main

import (
	"reflect"
	"testing"
)

func TestMCPMathMarkdownRoundTrip(t *testing.T) {
	f := newMCPContractFixture(t)
	source := "Literal $x^2$ stays text.\n\n$$\n\\frac{a_b}{c_d} + \\text{TST-1 @Admin https://flow.test}\n\\sum_{i=1}^{n} i\n$$\n\nAfter"
	created := mcpObject(t, f, "save_document", map[string]any{"title": "Formula notes", "content": source})
	id := created["id"].(string)
	found := mcpObject(t, f, "get_document", map[string]any{"id": id})
	if found["content"] != source {
		t.Fatalf("markdown changed: %v", found["content"])
	}
	doc, ok := f.document(t, id)
	if !ok {
		t.Fatal("document missing")
	}
	want := mcpMarkdownDocument(source)
	if !reflect.DeepEqual(doc.ContentData, want) {
		t.Fatalf("rich document changed math source: %#v", doc.ContentData)
	}
	blocks := want["content"].([]any)
	if len(blocks) != 3 || blocks[1].(map[string]any)["type"] != "mathBlock" {
		t.Fatalf("math block missing: %#v", blocks)
	}
	mcpObject(t, f, "save_document", map[string]any{"id": id, "content": found["content"]})
	again, _ := f.document(t, id)
	if !reflect.DeepEqual(again.ContentData, doc.ContentData) {
		t.Fatal("save/get/save did not preserve math")
	}
}

func TestMCPMathMarkdownBoundaries(t *testing.T) {
	for _, source := range []string{"$x^2$", "$$", "$$\nunclosed", "```tex\n$$\nx\n$$\n```"} {
		for _, raw := range mcpMarkdownDocument(source)["content"].([]any) {
			if raw.(map[string]any)["type"] == "mathBlock" {
				t.Fatalf("unexpected math block for %q", source)
			}
		}
	}
	blocks := mcpMarkdownDocument("Before\n$$\nx\n$$\nAfter")["content"].([]any)
	if len(blocks) != 3 || blocks[1].(map[string]any)["type"] != "mathBlock" {
		t.Fatalf("adjacent paragraphs swallowed math: %#v", blocks)
	}
}
