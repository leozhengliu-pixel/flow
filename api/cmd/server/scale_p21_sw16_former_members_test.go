package main

import (
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21SW16FormerMemberRequestMatrix(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		target := f.principals[2].user
		old, bounded := &server{store: repo}, &server{store: f.probe}
		check := func(want bool) {
			t.Helper()
			f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
				request := f.request(user, key, nil)
				for _, phase := range []string{"queued", "replayed"} {
					for _, ref := range []string{target.ID, target.ID + "-old-alias", target.ID + "\x00", ""} {
						previous := old.formerMemberOutsideWrite(request.Context(), f.data.Workspace.URLKey, ref)
						next := bounded.formerMemberOutsideWrite(request.Context(), f.data.Workspace.URLKey, ref)
						if previous != (ref == target.ID && want) || next != previous {
							t.Fatalf("%s former-member request changed: target=%q old=%t bounded=%t", phase, ref, previous, next)
						}
					}
					for _, source := range []*server{old, bounded} {
						if source.formerMemberOutsideWrite(request.Context(), f.data.Workspace.URLKey+"-foreign", target.ID) {
							t.Fatal("former-member refusal crossed a workspace binding")
						}
					}
				}
			})
		}
		check(false)
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, target.ID); err != nil {
			t.Fatal(err)
		}
		check(true)
		if err := repo.ResumeMember(t.Context(), f.data.Workspace.ID, target.ID); err != nil {
			t.Fatal(err)
		}
		check(false)
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("former-member request check decoded complete metadata")
		}
	}
}

func TestScaleP21SW16FormerMemberCollaborationLease(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, f.principals[2].user.ID); err != nil {
			t.Fatal(err)
		}
		held := []*store.FormerMembersRead{}
		defer func() {
			for _, read := range held {
				_ = read.Close()
			}
		}()
		saturated := false
		for range 4096 {
			read, err := f.probe.BeginFormerMembersRead(t.Context(), f.data.Workspace.URLKey)
			if err != nil {
				_ = read.Close()
				if !errors.Is(err, store.ErrMetadataWorkBudget) {
					t.Fatal("unexpected admission failure", err)
				}
				saturated = true
				break
			}
			held = append(held, read)
		}
		if !saturated || len(held) == 0 {
			t.Fatal("could not establish the real result-admission boundary")
		}
		request := f.request(f.data.Viewer, nil, nil)
		// Calibrate against the same source reader's own work allowance.
		// Free the smallest number of result leases that permits one nested
		// reader at its SQL boundary without an intervening former map.
		calibrated := false
		for len(held) > 0 {
			available, observed := false, false
			ctx := store.WithSQLStatementObserver(request.Context(), func(query string) {
				if observed || !strings.Contains(query, "issue_records") {
					return
				}
				observed = true
				read, err := f.probe.BeginFormerMembersRead(t.Context(), f.data.Workspace.URLKey)
				_ = read.Close()
				available = err == nil
				if err != nil && !errors.Is(err, store.ErrMetadataWorkBudget) {
					t.Error("unexpected nested-reader error", err)
				}
			})
			_, err := f.probe.IssueRecord(ctx, f.data.Workspace.URLKey, "sw16-missing-issue")
			if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, store.ErrMetadataWorkBudget) {
				t.Fatal("could not calibrate the source-reader work boundary", err)
			}
			if observed && available {
				calibrated = true
				break
			}
			_ = held[len(held)-1].Close()
			held = held[:len(held)-1]
		}
		if !calibrated {
			t.Fatal("could not calibrate one retained-result allowance")
		}
		observed := false
		ctx := store.WithSQLStatementObserver(request.Context(), func(query string) {
			if observed || !strings.Contains(query, "issue_records") {
				return
			}
			observed = true
			read, err := f.probe.BeginFormerMembersRead(t.Context(), f.data.Workspace.URLKey)
			_ = read.Close()
			if !errors.Is(err, store.ErrMetadataWorkBudget) {
				t.Error("collaboration released result admission before its source lookup", err)
			}
		})
		client := &realtimeSocketClient{workspace: f.data.Workspace.URLKey, issueDocuments: map[string]string{"sw16-description": "sw16-missing-issue"}}
		service := &server{store: f.probe, realtime: &realtimeHub{}}
		err := service.refuseFormerMemberCollaborationUpdate(request.WithContext(ctx), client, "sw16-description", nil)
		if !errors.Is(err, sql.ErrNoRows) || !observed {
			t.Fatal("collaboration did not reach the admitted source-lookup lifetime", err)
		}
		available, releasedObserved := false, false
		releasedContext := store.WithSQLStatementObserver(request.Context(), func(query string) {
			if releasedObserved || !strings.Contains(query, "issue_records") {
				return
			}
			releasedObserved = true
			read, err := f.probe.BeginFormerMembersRead(t.Context(), f.data.Workspace.URLKey)
			_ = read.Close()
			available = err == nil
		})
		_, err = f.probe.IssueRecord(releasedContext, f.data.Workspace.URLKey, "sw16-missing-issue")
		if !errors.Is(err, sql.ErrNoRows) || !releasedObserved || !available {
			t.Fatal("collaboration's error return leaked its retained result", err)
		}
	}
}

func TestScaleP21SW16FormerMemberProductionCallers(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate production caller source")
	}
	directory := filepath.Dir(source)
	files, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".go" || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, file.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "FormerMembers" && selector.Sel.Name != "BeginFormerMembersRead" {
				return true
			}
			receiver, ok := selector.X.(*ast.SelectorExpr)
			if !ok || receiver.Sel.Name != "store" {
				return true
			}
			if selector.Sel.Name == "FormerMembers" {
				t.Error("production server retained the unleased store map API", file.Name())
			} else {
				count++
			}
			return true
		})
	}
	if count != 3 {
		t.Fatalf("retained former-member reader has %d production callers, want 3", count)
	}
}
