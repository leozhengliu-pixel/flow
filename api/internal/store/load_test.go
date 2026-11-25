//go:build loadtest

package store

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP01PrincipalCounts(t *testing.T) {
	for _, count := range []int{5, 2000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "principals.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			teams := []string{}
			if err := repo.MutateWorkspace(t.Context(), "test-workspace", "test.p01_teams", "", nil, func(data *domain.Bootstrap) error {
				for i := range 13 {
					id := fmt.Sprintf("p01-team-%d", i)
					teams = append(teams, id)
					data.Teams = append(data.Teams, domain.Team{ID: id, Name: id, Key: fmt.Sprintf("P%02d", i), Private: i >= 2})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			people, err := repo.SeedScaleP01Principals(t.Context(), "test-workspace", teams, count, true)
			if err != nil {
				t.Fatal(err)
			}
			ids, tokens, scopes := map[string]bool{}, map[string]bool{}, map[string]bool{}
			for i, person := range people {
				if ids[person.User.ID] || tokens[person.Token] {
					t.Fatal("reused identity or session")
				}
				ids[person.User.ID], tokens[person.Token] = true, true
				scopes[strings.Join(person.TeamIDs, ",")] = true
				if i%100 == 0 || i == count-1 {
					user, err := repo.AuthenticateSession(t.Context(), person.Token)
					if err != nil || user.ID != person.User.ID {
						t.Fatal("seeded session cannot authenticate")
					}
					role, status, err := repo.WorkspaceRole(t.Context(), repo.Bootstrap().Workspace.ID, person.User.ID)
					if err != nil || status != "active" || role != person.Role {
						t.Fatal("seeded membership mismatch")
					}
				}
			}
			if len(ids) != count || len(scopes) != count {
				t.Fatalf("identities=%d scopes=%d want=%d", len(ids), len(scopes), count)
			}
		})
	}
}

func TestScaleP01MetadataScales(t *testing.T) {
	for _, factors := range [][2]float64{{1, 1}, {10, 1}, {1, 10}} {
		t.Run(fmt.Sprint(factors), func(t *testing.T) {
			repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "metadata.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			data := repo.Bootstrap()
			spec := SyntheticMetadata{Projects: 2, Trash: 2, Documents: 2, Conversations: 2, Notifications: 2, ProjectBytes: 1200, TrashBytes: 100, DocumentBytes: 100, ConversationBytes: 100, TeamIDs: []string{data.Teams[0].ID}, UserIDs: []string{data.Viewer.ID}, CountScale: factors[0], BodyScale: factors[1], Relations: true}
			if err := repo.SeedSyntheticMetadata(t.Context(), "test-workspace", spec); err != nil {
				t.Fatal(err)
			}
			if err := repo.ReloadWorkspace(t.Context(), "test-workspace"); err != nil {
				t.Fatal(err)
			}
			after := repo.Bootstrap()
			if len(after.Projects)-len(data.Projects) != int(2*factors[0]) || len(after.Documents)-len(data.Documents) != int(2*factors[0]) || len(after.Notifications)-len(data.Notifications) != int(2*factors[0]) {
				t.Fatal("count scale did not survive persistence")
			}
			var document domain.Document
			for _, item := range after.Documents {
				if item.ID == "lt-doc-0000" {
					document = item
				}
			}
			if len(document.Content) != int(100*factors[1]) || len(document.Revisions) != 1 || len(document.Revisions[0].Content) != int(50*factors[1]) || len(document.ProjectIDs) != 1 {
				t.Fatal("body/history/relation scale did not survive persistence")
			}
		})
	}
	for _, factor := range []float64{-1, math.NaN(), math.Inf(1)} {
		if _, err := scaleP01SyntheticSpec(SyntheticMetadata{CountScale: factor}); err == nil {
			t.Fatal("invalid scale accepted")
		}
	}
	if _, err := scaleP01SyntheticSpec(SyntheticMetadata{Projects: 1}); err == nil {
		t.Fatal("missing team must return an error rather than panic")
	}
}

func TestScaleP01SQLProbeCommitAndRollback(t *testing.T) {
	config := DatabaseConfig{Driver: "sqlite", Path: filepath.Join(t.TempDir(), "probe.db"), FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 1}
	repo, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	probe, err := repo.EnableScaleP01Probe(config)
	if err != nil {
		t.Fatal(err)
	}
	before, _, _ := probe.Snapshot()
	ctx := context.Background()
	var one int
	if err := repo.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatal(err)
	}
	stmt, err := repo.db.PrepareContext(ctx, "SELECT ?")
	if err != nil {
		t.Fatal(err)
	}
	if err := stmt.QueryRowContext(ctx, 2).Scan(&one); err != nil {
		t.Fatal(err)
	}
	stmt.Close()
	after, _, _ := probe.Snapshot()
	if after.Statements-before.Statements != 2 || after.Reads-before.Reads != 2 {
		t.Fatal("direct or prepared SQL omitted or double-counted")
	}
	data := repo.Bootstrap()
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: "rolled-back", Type: "project.updated", AggregateID: data.Projects[0].ID, WorkspaceID: data.Workspace.ID, Payload: []byte(`{}`), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	_, events, _ := probe.Snapshot()
	if len(events) != 0 {
		t.Fatal("rolled back event recorded as committed")
	}
	// The membership commit precedes a separately committed audit/event.
	ctx = ScaleP01RequestContext(ctx, "probe-request")
	tx, err = repo.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE team_memberships SET role=role WHERE workspace_id=?", data.Workspace.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	firstEnded := time.Now()
	published := false
	repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		_, commits, _ := probe.Snapshot()
		for _, commit := range commits {
			if commit.ID == event.ID {
				published = true
				if commit.At.After(firstEnded) || commit.At.After(time.Now()) {
					t.Error("commit clock followed audit/publication rather than first business commit")
				}
			}
		}
	})
	if err := repo.MutateWorkspace(ctx, "test-workspace", "project.updated", data.Projects[0].ID, nil, func(data *domain.Bootstrap) error { data.Projects[0].Summary = "Probe edit"; return nil }); err != nil {
		t.Fatal(err)
	}
	if !published {
		t.Fatal("commit was not observable before realtime publication")
	}
}
