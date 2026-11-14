package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestScaleP21SW29LogicalOrphanRetainsDiskAdmission(t *testing.T) {
	beforeDisk, beforeDescriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	builder, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer builder.Close()
	if _, err := builder.writeValue(reflect.ValueOf("owned logical bytes"), 0, false); err != nil {
		t.Fatal(err)
	}
	name := builder.storage.file.Name()
	orphan := name + ".owned-orphan"
	if err := os.Rename(name, orphan); err != nil {
		t.Fatal(err)
	}
	released := false
	t.Cleanup(func() {
		if err := os.RemoveAll(name); err != nil {
			t.Errorf("remove owned blocker: %v", err)
		}
		if err := os.Remove(orphan); err != nil {
			t.Errorf("remove owned orphan: %v", err)
		} else if released {
			metadataLogicalDiskUsed.Add(-builder.storage.bytes)
		}
	})
	if err := os.Mkdir(name, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(name, "owned-blocker"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	charged := metadataLogicalDiskUsed.Load()
	builder.Close()
	released = true
	if charged <= beforeDisk || metadataLogicalDiskUsed.Load() != charged {
		t.Fatal("failed file removal released disk admission while bytes remain")
	}
	if metadataLogicalDescriptorsUsed.Load() != beforeDescriptors {
		t.Fatal("closed orphan retained unrelated native descriptors")
	}
}
