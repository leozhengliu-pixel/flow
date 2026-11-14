package store

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW19GitLabConfigProjection(data domain.Bootstrap) (string, []domain.IntegrationConnection) {
	var connections []domain.IntegrationConnection
	for _, item := range data.IntegrationConnections {
		if item.Provider != "gitlab" {
			continue
		}
		connections = append(connections, domain.IntegrationConnection{ID: item.ID, Provider: item.Provider, Config: item.Config})
	}
	return data.Workspace.URLKey, connections
}

func TestScaleP21SW19GitLabConfigsInstalledDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, id := f.data.Workspace.URLKey, idgen.New("sw19-installed-gitlab-")
	zone := time.FixedZone(strings.Repeat("omitted-date-location", 50000), 3661)
	when := time.Now().In(zone)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "integrationConnections"), workspace, "scale_p21_sw19.gitlab_configs", "", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections = []domain.IntegrationConnection{
			{ID: id + "-provider", Provider: "git\x00lab", Config: map[string]string{"ignored": strings.Repeat("other provider", 100000)}},
			{ID: id, Provider: "gitlab", Status: "con\x00nected", Name: "omitted", Config: map[string]string{"host": "https://gitlab.example.test", "webhookSecret": "before\x00after", "rotationEnabled": "tr\x00ue"}, CreatedAt: when, OAuthExpiresAt: &when},
			{ID: id + "-other", Provider: "gitlab", Config: map[string]string{"host": "https://duplicate.example.test"}},
			{ID: id + "-nilconfig", Provider: "gitlab"},
			{ID: id + "-empty", Provider: "gitlab", Config: map[string]string{}},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for pair := range 3 {
		for _, projected := range []bool{false, true} {
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for range 20 {
				if projected {
					read, found, err := f.BeginGitLabConnectionConfigsRead(t.Context(), workspace)
					if err != nil || !found {
						t.Fatal(err)
					}
					read.Close()
				} else if _, found := f.WorkspaceMetadataFields(workspace, "integrationConnections"); !found {
					t.Fatal("missing measured source")
				}
			}
			runtime.ReadMemStats(&after)
			t.Logf("pair=%d projected=%v bytes/op=%d allocs/op=%d", pair, projected, (after.TotalAlloc-before.TotalAlloc)/20, (after.Mallocs-before.Mallocs)/20)
		}
	}
	for _, stage := range []string{"warm", "reloaded"} {
		if stage == "reloaded" {
			f.mu.Lock()
			delete(f.copyRevisions, workspace)
			f.mu.Unlock()
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		probe, err := f.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []*SQLiteStore{f.SQLiteStore, probe} {
			for _, key := range []string{workspace, "", workspace + "-missing", strings.ToUpper(workspace)} {
				before, found := source.WorkspaceMetadataFields(key, "integrationConnections")
				expectedWorkspace, expected := scaleP21SW19GitLabConfigProjection(before)
				read, actualFound, err := source.BeginGitLabConnectionConfigsRead(t.Context(), key)
				if err != nil || actualFound != found {
					t.Fatal(stage, "installed selection", err, found, actualFound)
				}
				if !found {
					if read != nil {
						t.Fatal("missing source returned a handle")
					}
					continue
				}
				if read.WorkspaceURLKey != expectedWorkspace || !reflect.DeepEqual(read.Connections, expected) {
					t.Fatal(stage, "same installed source/config projection differs", read.Connections, expected)
				}
				read.Close()
				read.Close()
			}
		}
		owner, _ := f.WorkspaceMetadataFields(workspace, "integrationConnections")
		cold, _ := probe.WorkspaceMetadataFields(workspace, "integrationConnections")
		if len(owner.IntegrationConnections) == 0 || len(cold.IntegrationConnections) != 0 {
			t.Fatal("independent loader/source hold witness changed")
		}
		if probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
			t.Fatal("config read decoded full metadata or leaked allowance")
		}
	}
}

func TestScaleP21SW19GitLabConfigsOwnershipAdmission(t *testing.T) {
	id := "owned-gitlab-id"
	body := strings.Repeat("owned<&>\x00", 2000)
	config := map[string]string{"owned-key": body}
	source := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "owned-workspace-id", URLKey: "owned-url-key"},
		IntegrationConnections: []domain.IntegrationConnection{
			{ID: id, Provider: "gitlab", Config: config},
			{ID: id, Provider: "gitlab", Config: map[string]string{"later": "duplicate"}},
			{ID: "", Provider: "gitlab"},
		},
	}
	repo := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"physical": source}, lastWorkspaceKey: "physical", metadataResidency: newMetadataResidency()}
	parent, releaseParent, err := repo.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	read, found, err := repo.BeginGitLabConnectionConfigsRead(parent, "")
	if err != nil || !found {
		releaseParent()
		t.Fatal(err)
	}
	releaseParent()
	if repo.metadataResidency.work.used == 0 || read.Connections[0].Config["owned-key"] != body {
		t.Fatal("retained read reused parent admission")
	}
	if unsafe.StringData(read.WorkspaceURLKey) == unsafe.StringData(source.Workspace.URLKey) ||
		unsafe.StringData(read.Connections[0].ID) == unsafe.StringData(id) ||
		unsafe.StringData(read.Connections[0].Config["owned-key"]) == unsafe.StringData(body) {
		t.Fatal("owned config read retained source string backing")
	}
	if len(read.Connections) != 3 || read.Connections[1].ID != id || read.Connections[2].ID != "" {
		t.Fatal("duplicate/empty IDs or source order changed")
	}
	repo.workspaces["physical"] = domain.Bootstrap{}
	config["owned-key"] = "changed"
	if read.Connections[0].Config["owned-key"] != body {
		t.Fatal("source generation changed owned config")
	}
	read.Close()
	if repo.metadataResidency.work.used != 0 || read.Connections != nil {
		t.Fatal("config handle did not release")
	}
	if _, _, err := repo.BeginGitLabConnectionConfigsRead(parent, ""); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("stale same-store parent was accepted", err)
	}
	repo.workspaces["physical"] = source
	repo.metadataResidency.work.limit = 1
	if _, _, err := repo.BeginGitLabConnectionConfigsRead(t.Context(), ""); !errors.Is(err, ErrMetadataWorkBudget) || repo.metadataResidency.work.used != 0 {
		t.Fatal("saturated admission did not reject before cloning", err)
	}
	repo.metadataResidency.work.limit = metadataWorkBytes
	source.IntegrationConnections[0].Config = map[string]string{"over": strings.Repeat("x", scaleP21SW19GitLabConfigsMaxWork/33)}
	repo.workspaces["physical"] = source
	if _, _, err := repo.BeginGitLabConnectionConfigsRead(t.Context(), ""); !errors.Is(err, ErrMetadataWorkBudget) || repo.metadataResidency.work.used != 0 {
		t.Fatal("oversized config did not reject before allocation", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := repo.BeginGitLabConnectionConfigsRead(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled read succeeded", err)
	}
	source.IntegrationConnections[0].Config = map[string]string{"rotationEnabled": "false"}
	repo.workspaces["physical"] = source
	read, found, err = repo.BeginGitLabConnectionConfigsRead(context.WithoutCancel(ctx), "")
	if err != nil || !found || len(read.Connections) != 3 {
		t.Fatal("rotation's context-insensitive snapshot changed", err)
	}
	read.Close()
	repo.workspaces["next"] = domain.Bootstrap{Workspace: domain.Workspace{URLKey: "next-logical"}}
	repo.lastWorkspaceKey = "next"
	read, found, err = repo.BeginGitLabConnectionConfigsRead(t.Context(), "")
	if err != nil || !found || read.WorkspaceURLKey != "next-logical" || len(read.Connections) != 0 {
		t.Fatal("default source did not follow publication", err)
	}
	read.Close()
}
