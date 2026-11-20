package store

import (
	"reflect"
	"slices"
	"testing"
)

// The display pass's kept collections and records decode exactly as the
// rewritten ones: with the directory's users equal to the records' and after
// a user changed under the records (a peer's rename reloaded).
func TestLegacyDisplayRefreshKeepsUnchangedRecordsExactly(t *testing.T) {
	_, view, workspace := coldDecodeFixture(t)
	compare := func(label string, source *metadataLogicalSource) {
		t.Helper()
		kept := metadataDisplayRefreshesKept.Load()
		display, err := view.refreshLegacyDisplayImage(t.Context(), source)
		if err != nil {
			t.Fatal(label, err)
		}
		defer display.Release()
		got := coldDecodeReference(t, display)
		keptNow := metadataDisplayRefreshesKept.Load() - kept
		metadataDisplayRefreshKeep.Store(false)
		reference, err := view.refreshLegacyDisplayImage(t.Context(), source)
		metadataDisplayRefreshKeep.Store(true)
		if err != nil {
			t.Fatal(label, err)
		}
		defer reference.Release()
		coldDecodeAssertEqual(t, label, got, coldDecodeReference(t, reference))
		t.Logf("%s: %d collections kept", label, keptNow)
	}
	source := coldDecodeInstalled(t, view, workspace)
	defer source.Release()
	compare("directory equal", source)
	// The directory's users renamed under the records that hold them.
	installed := coldDecodeReference(t, source)
	renamed := slices.Clone(installed.Users)
	for i := range renamed {
		renamed[i].DisplayName = "Renamed " + renamed[i].DisplayName
	}
	mismatched, err := source.Transform(t.Context(), func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if name == "users" {
			return metadataAggregateNativeNode{reflect.ValueOf(renamed)}, true, nil
		}
		return metadataAggregateStoredNode{original}, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mismatched.Release()
	compare("users renamed", mismatched)
}
