package store

import (
	"fmt"
	"reflect"
	"testing"

	"flow/api/internal/domain"
)

// A user lookup of the display refresh returns the users field's last record
// with the reference's ID, as the scan did, from the frame's ID index:
// duplicates, a missing ID and stored or native references.
func TestScaleAggregateUserIndexLookup(t *testing.T) {
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: "users", URLKey: "users", Name: "Users"}}
	for i := range 300 {
		data.Users = append(data.Users, domain.User{ID: fmt.Sprintf("user-%03d", i%250), Name: fmt.Sprintf("User %d", i)})
	}
	lead := data.Users[17]
	data.Projects = []domain.Project{{ID: "p", Name: "P", Lead: &lead}}
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: "users", revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	want := func(id string) string {
		name := ""
		for _, user := range data.Users {
			if user.ID == id {
				name = user.Name
			}
		}
		return name
	}
	name := func(node metadataAggregateNode) string {
		if node == nil {
			return ""
		}
		value, err := node.nodeValue(t.Context(), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		return value.Interface().(domain.User).Name
	}
	for _, id := range []string{"user-000", "user-017", "user-049", "user-249", "user-250", "", "missing"} {
		got, err := metadataAggregateLookupUser(t.Context(), source, metadataAggregateNativeNode{value: reflect.ValueOf(id)})
		if err != nil {
			t.Fatal(err)
		}
		if name(got) != want(id) {
			t.Fatalf("lookup %q: %q, want %q", id, name(got), want(id))
		}
	}
	// A stored reference (the project's lead ID).
	index, _, _ := metadataLogicalField("projects")
	project := metadataAggregateStoredNode{source.fields[index]}
	var stored metadataAggregateNode
	if err := project.nodeChildren(t.Context(), func(_ reflect.Value, child metadataAggregateNode) error {
		leadNode, err := metadataAggregateChild(t.Context(), child, "Lead")
		if err != nil {
			return err
		}
		unwrapped, err := metadataAggregateUnwrap(t.Context(), leadNode, true)
		if err != nil {
			return err
		}
		stored, err = metadataAggregateChild(t.Context(), unwrapped, "ID")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	got, err := metadataAggregateLookupUser(t.Context(), source, stored)
	if err != nil {
		t.Fatal(err)
	}
	if name(got) != want("user-017") {
		t.Fatalf("stored reference: %q, want %q", name(got), want("user-017"))
	}
	usersIndex, _, _ := metadataLogicalField("users")
	users := source.fields[usersIndex]
	if index, found := users.source.userIndex(users); !found || !index.complete || len(index.users) != 250 {
		t.Fatalf("users frame index: found %v, %d IDs", found, len(index.users))
	}
}
