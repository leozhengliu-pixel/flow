package store

import (
	"errors"
	"reflect"
	"testing"
)

func TestScaleP21SW328CollectionRootPatchLifetime(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		first, err := newMetadataLogicalRecordSpool(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		second, err := newMetadataLogicalRecordSpool(t.Context())
		if err != nil {
			first.Close()
			t.Fatal(err)
		}
		a := &legacySessionRootPatch{field: "integrationDeliveries", spool: first}
		b := &legacySessionRootPatch{field: "integrationConnections", spool: second}
		a.next = b
		defer a.Close()
		if cycle {
			b.next = a
		}
		var names []string
		err = a.visit(func(part *legacySessionRootPatch) error {
			names = append(names, part.field)
			return nil
		})
		if !reflect.DeepEqual(names, []string{"integrationDeliveries", "integrationConnections"}) ||
			cycle && !errors.Is(err, ErrBoundedMetadataCoverage) || !cycle && err != nil {
			t.Fatal("collection patch traversal lost order or failed to reject a cycle", names, err)
		}
		a.Close()
		a.Close()
		if a.spool != nil || b.spool != nil || a.next != nil || b.next != nil {
			t.Fatal("collection patch close retained a field owner")
		}
	}
}
