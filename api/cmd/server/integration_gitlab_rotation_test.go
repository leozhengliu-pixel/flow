package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// SW559: with bounded metadata on, BeginGitLabConnectionConfigsRead read the
// resident image, which holds no integration connections, so the minute
// sweep's token rotation found no connection and never renewed a token. The
// differential seeds one database flag off (GitLab connections that rotate,
// one already failed, one disabled, one not due, one of another provider)
// and runs the sweep on three copies: flag off; bounded with the connections
// made due through the bounded store itself (its installed source patched
// warm); and bounded opened after they were made due (the installed source
// loaded from SQL). Each must present the same tokens to GitLab in the same
// order, store the same credentials and leave the same connection configs
// (rotation times masked).
func TestScaleP21SW559GitLabRotationDifferential(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	enableConnectorSecrets(t)
	// Servers below disable auth, which lets them reach the loopback fake.
	fake := newFakeGitLab(t)
	tokens := map[string]string{
		"sw559-due-a": "glpat-sw559-a", "sw559-due-b": "glpat-sw559-b", "sw559-failed": "glpat-sw559-failed",
		"sw559-disabled": "glpat-sw559-disabled", "sw559-later": "glpat-sw559-later",
	}
	reset := func() {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		fake.rotated, fake.seen = nil, nil
		for _, token := range tokens {
			fake.tokens[token] = []string{"read_api", "self_rotate"}
		}
		for token := range fake.tokens {
			if strings.HasPrefix(token, "glpat-fake-rotated-") {
				delete(fake.tokens, token)
			}
		}
		// The second due connection's token was revoked on GitLab: its
		// rotation fails and is recorded on the connection.
		delete(fake.tokens, "glpat-sw559-b")
	}
	const workspace = "test-workspace"
	ctx := t.Context()
	dir := t.TempDir()
	seed := filepath.Join(dir, "seed.db")
	open := func(path string, bounded bool) *store.SQLiteStore {
		t.Helper()
		value := "off"
		if bounded {
			value = "on"
		}
		t.Setenv("FLOW_BOUNDED_METADATA", value)
		repo, err := store.OpenSQLiteTestFixture(path)
		if err != nil {
			t.Fatal(err)
		}
		if repo.BoundedMetadataEnabled() != bounded {
			t.Fatalf("store opened with bounded=%v, want %v", repo.BoundedMetadataEnabled(), bounded)
		}
		return repo
	}
	repo := open(seed, false)
	later := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	gitlab := func(id string, config map[string]string) domain.IntegrationConnection {
		merged := map[string]string{"host": fake.URL, "canSelfRotate": "true", "rotationEnabled": "true", "nextRotationAt": later, "tokenHint": id[len(id)-2:]}
		for key, value := range config {
			merged[key] = value
		}
		return domain.IntegrationConnection{ID: id, Provider: "gitlab", Name: id, Status: "connected", Config: merged}
	}
	if err := repo.MutateWorkspace(store.WithMetadataFields(ctx, "integrationConnections"), workspace, "scale_p21_sw559.gitlab_seed", "", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections = append(data.IntegrationConnections,
			domain.IntegrationConnection{ID: "sw559-other", Provider: "github", Name: "other", Config: map[string]string{"rotationEnabled": "true", "canSelfRotate": "true", "nextRotationAt": "2020-01-01T00:00:00Z"}},
			gitlab("sw559-due-a", nil),
			gitlab("sw559-failed", map[string]string{"rotationFailureReason": "Last rotation failed: earlier"}),
			gitlab("sw559-disabled", map[string]string{"rotationEnabled": "false"}),
			gitlab("sw559-due-b", nil),
			gitlab("sw559-later", nil),
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	seeder := &server{store: repo, authDisabled: true}
	for id, token := range tokens {
		if err := seeder.putGitLabCredential(ctx, workspace, id, gitlabCredential{Token: token, URL: fake.URL, TokenID: 7}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.CompleteMetadataCatalogForTesting(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	makeDue := func(ctx context.Context, s *server) {
		t.Helper()
		past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
		for _, id := range []string{"sw559-due-a", "sw559-failed", "sw559-disabled", "sw559-due-b"} {
			if _, err := s.saveGitLabConnection(ctx, workspace, id, "scale_p21_sw559.rotation_due", func(connection *domain.IntegrationConnection, _ time.Time) {
				connection.Config["nextRotationAt"] = past
			}); err != nil {
				t.Fatal(id, err)
			}
		}
	}
	type outcome struct {
		Presented   []string
		Credentials map[string]string
		Connections string
	}
	run := func(mode string) outcome {
		t.Helper()
		path := filepath.Join(dir, mode+".db")
		scaleP21SW559CopyDatabase(t, seed, path)
		bounded := mode != "legacy"
		work := ctx
		if bounded {
			work = store.WithColdWork(ctx, "sw559")
		}
		if mode == "bounded-reloaded" {
			legacy := open(path, false)
			makeDue(ctx, &server{store: legacy, authDisabled: true})
			if err := legacy.Close(); err != nil {
				t.Fatal(err)
			}
		}
		repo := open(path, bounded)
		defer repo.Close()
		s := &server{store: repo, authDisabled: true}
		if mode != "bounded-reloaded" {
			makeDue(work, s)
		}
		reset()
		s.rotateDueGitLabTokens(work, workspace, time.Now().UTC())
		fake.mu.Lock()
		result := outcome{Presented: append([]string(nil), fake.rotated...), Credentials: map[string]string{}}
		fake.mu.Unlock()
		for id := range tokens {
			credential, err := s.gitlabCredential(ctx, workspace, id)
			if err != nil {
				t.Fatal(mode, id, err)
			}
			result.Credentials[id] = credential.Token
		}
		data, found := repo.WorkspaceMetadataFields(workspace, "integrationConnections")
		if !found {
			t.Fatal(mode, "workspace missing")
		}
		connections := make([]domain.IntegrationConnection, 0, len(data.IntegrationConnections))
		for _, connection := range data.IntegrationConnections {
			config := map[string]string{}
			for key, value := range connection.Config {
				switch key {
				case "lastRotatedAt", "nextRotationAt", "expiresAt":
					if value != "" {
						value = "<time>"
					}
				}
				config[key] = value
			}
			connection.Config, connection.UpdatedAt, connection.CreatedAt = config, time.Time{}, time.Time{}
			connections = append(connections, connection)
		}
		sort.Slice(connections, func(i, j int) bool { return connections[i].ID < connections[j].ID })
		raw, err := json.Marshal(connections)
		if err != nil {
			t.Fatal(err)
		}
		result.Connections = string(raw)
		return result
	}
	legacy := run("legacy")
	if !reflect.DeepEqual(legacy.Presented, []string{"glpat-sw559-a"}) || legacy.Credentials["sw559-due-a"] != "glpat-fake-rotated-a" ||
		!strings.Contains(legacy.Connections, "Last rotation failed") {
		t.Fatalf("legacy rotation did not renew the due connection and record the revoked one: %+v", legacy)
	}
	for _, mode := range []string{"bounded-warm", "bounded-reloaded"} {
		got := run(mode)
		if !reflect.DeepEqual(got.Presented, legacy.Presented) || !reflect.DeepEqual(got.Credentials, legacy.Credentials) {
			t.Errorf("%s rotation renewed %v / stored %v; legacy renewed %v / stored %v", mode, got.Presented, got.Credentials, legacy.Presented, legacy.Credentials)
		}
		if got.Connections != legacy.Connections {
			t.Errorf("%s connections differ\n%s\nlegacy:\n%s", mode, got.Connections, legacy.Connections)
		}
	}
}

func scaleP21SW559CopyDatabase(t *testing.T, from, to string) {
	t.Helper()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		source, err := os.Open(from + suffix)
		if os.IsNotExist(err) && suffix != "" {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		target, err := os.Create(to + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(target, source); err != nil {
			t.Fatal(err)
		}
		_ = source.Close()
		if err := target.Close(); err != nil {
			t.Fatal(fmt.Sprint(to, err))
		}
	}
}
