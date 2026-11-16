package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type scaleP21SW168Aliases struct {
	ID, Slug string
	Previous []string
}

func scaleP21SW168AliasValue(document domain.Document) scaleP21SW168Aliases {
	return scaleP21SW168Aliases{document.ID, document.SlugID, slices.Clone(document.PreviousSlugIDs)}
}

func TestScaleP21SW168DocumentAliasHeaderDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw168-")
	public, private, child := prefix+"public", prefix+"private", prefix+"child"
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw168_teams", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: public}, domain.Team{ID: private, Private: true}, domain.Team{ID: child})
		data.TeamSettings[public] = domain.TeamSettings{TeamID: public, Access: "public"}
		data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
		data.TeamSettings[child] = domain.TeamSettings{TeamID: child, Access: "public", ParentTeamID: private}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	admin := scaleP21SW143Join(t, f, "admin", []string{private})
	member := scaleP21SW143Join(t, f, "member", []string{public})
	guest := scaleP21SW143Join(t, f, "guest", []string{public})
	inherited := scaleP21SW143Join(t, f, "member", []string{private})
	orderedAliases := []string{prefix + "second", prefix + "first", prefix + "second", " Old alias "}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw168_documents", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = []domain.Document{
			{ID: prefix + "public-doc", SlugID: prefix + "current", PreviousSlugIDs: orderedAliases, Title: "Public aliases", Creator: admin, TeamIDs: []string{public}},
			{ID: prefix + "private-doc", PreviousSlugIDs: []string{prefix + "hidden-alias"}, Title: "Private aliases", Creator: admin, TeamIDs: []string{private},
				Permissions: []domain.DocumentPermission{{ID: prefix + "grant", SubjectType: "user", SubjectID: guest.ID, Role: "viewer"}}},
			{ID: prefix + "child-doc", PreviousSlugIDs: nil, Title: "Inherited aliases", Creator: admin, TeamIDs: []string{child}},
			{ID: prefix + "empty-doc", PreviousSlugIDs: []string{}, Title: "Empty aliases", Creator: admin, TeamIDs: []string{public}},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	principals := []struct {
		name string
		id   string
	}{
		{"admin", admin.ID}, {"admin-private-outsider", f.data.Viewer.ID},
		{"member-private-outsider", member.ID}, {"guest-explicit-grant", guest.ID},
		{"sub-team-inheritance", inherited.ID},
	}
	keys := []*domain.APIKey{nil,
		{ID: prefix + "all", Scopes: []string{"read"}},
		{ID: prefix + "empty", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}},
		{ID: prefix + "set", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{public}},
	}
	fields := append([]string{"workspace", "viewer", "favorites", "projects", "initiatives", "initiativeRelations", "labels", "labelGroups"}, documentReadPolicyFields...)
	for _, state := range []string{"warm", "reloaded"} {
		if state == "reloaded" {
			f.mu.Lock()
			f.forgetCopyRevisionLocked(workspace)
			f.mu.Unlock()
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		for _, principal := range principals {
			for keyIndex, key := range keys {
				t.Run(fmt.Sprint(state, "/", principal.name, "/", keyIndex), func(t *testing.T) {
					literal, err := f.DocumentDirectoryMetadata(t.Context(), workspace, principal.id, key, fields)
					if errors.Is(err, ErrResourceAccessChanged) {
						literal.Data, err = f.PagedWorkspaceMetadata(t.Context(), workspace, principal.id)
					}
					if err != nil {
						t.Fatal(err)
					}
					read, err := view.BeginResourceDirectoryRead(t.Context(), workspace, principal.id, key, "documents", nil)
					if err != nil {
						t.Fatal(err)
					}
					defer read.Close()
					base := view.MetadataWorkUsedForTesting()
					want, visited, loaded := []scaleP21SW168Aliases{}, []scaleP21SW168Aliases{}, []scaleP21SW168Aliases{}
					for _, document := range literal.Data.Documents {
						if document.ID == prefix+"public-doc" && !reflect.DeepEqual(document.PreviousSlugIDs, orderedAliases) {
							t.Fatal("legacy alias fixture lost duplicates or ordering")
						}
						want = append(want, scaleP21SW168AliasValue(document))
					}
					err = read.Visit(t.Context(), func(header *ResourceDirectoryValue) error {
						visited = append(visited, scaleP21SW168AliasValue(header.Document))
						value, err := read.LoadHeader(t.Context(), header.Token)
						if err != nil {
							return err
						}
						loaded = append(loaded, scaleP21SW168AliasValue(value.Document))
						if len(value.Document.PreviousSlugIDs) > 0 {
							value.Document.PreviousSlugIDs[0] = "owned mutation"
						}
						value.Close()
						if !reflect.DeepEqual(header.Document.PreviousSlugIDs, visited[len(visited)-1].Previous) {
							t.Fatal("mutable alias output changed the live visitor header")
						}
						if header.Document.Content != "" || header.Document.ContentState != "" || header.Document.ContentData != nil || header.Document.Revisions != nil {
							t.Fatal("alias header loaded excluded document bodies")
						}
						return nil
					})
					if err != nil || !reflect.DeepEqual(want, visited) || !reflect.DeepEqual(want, loaded) {
						t.Fatalf("projected directory aliases differ: %v\nwant=%#v\nvisit=%#v\nload=%#v", err, want, visited, loaded)
					}
					if view.MetadataWorkUsedForTesting() != base {
						t.Fatal("alias headers leaked per-record admission")
					}
					read.Close()
					if view.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("alias directory leaked admission")
					}
				})
			}
		}
		view.releaseMetadataLogicalSources()
	}
}

func TestScaleP21SW168DocumentAliasHeaderAdmissionAndRelease(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, idgen.New("sw168-large-")
	aliases := make([]string, 4096)
	for i := range aliases {
		aliases[i] = fmt.Sprint(strings.Repeat("alias", 16), "-", i)
	}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw168_large", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = []domain.Document{{ID: target, Title: "Large alias header", Creator: data.Viewer, PreviousSlugIDs: aliases}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	read, err := view.BeginResourceDirectoryRead(t.Context(), workspace, "", nil, "documents", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	base := view.MetadataWorkUsedForTesting()
	token := ResourceDirectoryToken{Ordinal: 0, Child: -1}
	value, err := read.LoadHeader(t.Context(), token)
	if err != nil || !reflect.DeepEqual(value.Document.PreviousSlugIDs, aliases) || view.MetadataWorkUsedForTesting() <= base {
		t.Fatal("legal alias list lost data or admission", err)
	}
	value.Close()
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = base + 1
	_, err = read.LoadHeader(t.Context(), token)
	view.metadataResidency.work.limit = limit
	if !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != base {
		t.Fatal("alias admission refusal leaked work", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := read.LoadHeader(cancelled, token); !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != base {
		t.Fatal("alias header ignored cancellation or leaked", err)
	}
	if err := read.Visit(t.Context(), func(header *ResourceDirectoryValue) error {
		if !reflect.DeepEqual(header.Document.PreviousSlugIDs, aliases) {
			t.Fatal("large Visit aliases differ")
		}
		return nil
	}); err != nil || view.MetadataWorkUsedForTesting() != base {
		t.Fatal("large alias visitor leaked admission", err)
	}
	value, err = read.LoadHeader(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	read.Close()
	if !reflect.DeepEqual(value.Document.PreviousSlugIDs, aliases) || view.MetadataWorkUsedForTesting() == 0 {
		t.Fatal("directory close released independently owned aliases")
	}
	value.Close()
	if value.Document.PreviousSlugIDs != nil || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("closed alias output retained data or admission")
	}
}
