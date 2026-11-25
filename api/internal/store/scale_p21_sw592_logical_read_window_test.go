package store

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// SW592: materializing a node reads it once into a window and decodes it and
// its nested nodes from that copy; the decoded values equal the captured
// ones whether the node fits a window (a label, a project), exceeds one (a
// large settings map, the whole projects collection) or nests one inside
// another.
func TestScaleP21SW592LogicalReadWindow(t *testing.T) {
	large := strings.Repeat("window-", metadataLogicalWindowBytes/7+100)
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "sw592", URLKey: "sw592", Name: "SW592"},
		Labels:    []domain.IssueLabel{{ID: "a", Name: "Alpha", Scope: "Workspace"}, {ID: "b", Name: "Beta", Description: strings.Repeat("d", 5000)}},
		Settings:  map[string]any{"large": large, "nested": map[string]any{"k": []any{"x", 1.5, true}}},
	}
	for i := range 40 {
		data.Projects = append(data.Projects, domain.Project{ID: "p" + strings.Repeat("x", i), Name: strings.Repeat("n", 3000+i)})
	}
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: "sw592", revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	metadataLogicalSpoolCounting.Store(true)
	defer metadataLogicalSpoolCounting.Store(false)
	for _, field := range []string{"labels", "projects", "settings", "workspace"} {
		got, err := source.Select(t.Context(), MetadataRecordScope{Fields: []string{field}}, 1<<30)
		if err != nil {
			t.Fatal(field, err)
		}
		gotJSON, _ := json.Marshal(metadataRecordSnapshot(got, MetadataRecordScope{Fields: []string{field}}, metadataRecordFields(MetadataRecordScope{Fields: []string{field}})))
		wantJSON, _ := json.Marshal(metadataRecordSnapshot(data, MetadataRecordScope{Fields: []string{field}}, metadataRecordFields(MetadataRecordScope{Fields: []string{field}})))
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("SW592 %s decoded differently through windows", field)
		}
	}
	// A windowed node's children read from its window.
	if err := source.VisitFields(t.Context(), func(name string, node metadataLogicalNode) error {
		if name != "labels" {
			return nil
		}
		windowed := node.windowed()
		if windowed.window == nil {
			t.Fatal("SW592 a small collection got no window")
		}
		c := metadataLogicalCursor{windowed, windowed.offset + 16, t.Context()}
		if _, err := c.byte(); err != nil {
			t.Fatal(err)
		}
		if _, err := c.uint(); err != nil {
			t.Fatal(err)
		}
		child, err := c.child(node.typ.Elem())
		if err != nil {
			t.Fatal(err)
		}
		if child.window != windowed.window {
			t.Fatal("SW592 a child of a windowed node does not share its window")
		}
		// A node moved to another storage (a copied frame) keeps the field
		// but never reads the window of the storage it left.
		moved := child
		moved.source = &metadataLogicalStorage{}
		var probe [8]byte
		if moved.window.read(probe[:], moved.source, moved.offset) {
			t.Fatal("SW592 a window served a read of another storage")
		}
		before := metadataLogicalSpoolReadBytes.Load()
		if _, err := child.value(t.Context(), 0); err != nil {
			t.Fatal(err)
		}
		if read := metadataLogicalSpoolReadBytes.Load() - before; read != 0 {
			t.Fatalf("SW592 decoding a windowed child read %d spool bytes", read)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// A node too large for one window (a large collection) is traversed in
// chunks: its child headers and small children come from the cursor's
// sliding window, not one spool read each, and decode as before.
func TestScaleP21LogicalReadSlidingWindow(t *testing.T) {
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: "slide", URLKey: "slide", Name: "Slide"}}
	for i := range 3000 {
		data.Labels = append(data.Labels, domain.IssueLabel{ID: fmt.Sprintf("label-%04d", i), Name: strings.Repeat("n", 40+i%90), Description: strings.Repeat("d", i%7*60)})
	}
	// One child larger than a chunk: it is read on its own.
	data.Labels[1500].Description = strings.Repeat("big", metadataLogicalSlideBytes/2)
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: "slide", revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	index, _, _ := metadataLogicalField("labels")
	node := source.fields[index]
	if node.size <= metadataLogicalWindowBytes {
		t.Fatalf("labels are %d bytes: not larger than a window", node.size)
	}
	metadataLogicalSpoolCounting.Store(true)
	defer metadataLogicalSpoolCounting.Store(false)
	calls := metadataLogicalSpoolReadCalls.Load()
	got, err := source.Select(t.Context(), MetadataRecordScope{Fields: []string{"labels"}}, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	reads := metadataLogicalSpoolReadCalls.Load() - calls
	if !reflect.DeepEqual(got.Labels, data.Labels) {
		t.Fatal("labels decoded differently through sliding windows")
	}
	if limit := node.size/(metadataLogicalSlideBytes/2) + 16; reads > limit {
		t.Fatalf("decoding %d bytes of labels took %d spool reads, want at most %d", node.size, reads, limit)
	}
	calls = metadataLogicalSpoolReadCalls.Load()
	count := 0
	if err := node.Children(t.Context(), func(_ reflect.Value, item metadataLogicalNode) error {
		value, err := item.Value(t.Context(), int(item.cost))
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(value.Interface(), data.Labels[count]) {
			t.Fatalf("child %d decoded differently", count)
		}
		count++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != len(data.Labels) {
		t.Fatalf("visited %d children", count)
	}
}
