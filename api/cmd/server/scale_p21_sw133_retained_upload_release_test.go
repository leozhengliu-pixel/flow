package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW133Storage struct {
	t       *testing.T
	repo    *store.SQLiteStore
	deleted []string
}

func (s *scaleP21SW133Storage) Put(context.Context, string, io.Reader, string) (int64, error) {
	panic("unexpected Put")
}

func (s *scaleP21SW133Storage) Open(context.Context, string) (io.ReadCloser, string, int64, error) {
	panic("unexpected Open")
}

func (s *scaleP21SW133Storage) Delete(ctx context.Context, name string) error {
	if ctx.Err() != nil {
		s.t.Fatal("literal detached purge inherited cancellation")
	}
	if s.repo.BoundedMetadataEnabled() && s.repo.MetadataWorkUsedForTesting() == 0 {
		s.t.Fatal("candidate name owner ended before object deletion")
	}
	s.deleted = append(s.deleted, name)
	if strings.HasSuffix(name, "-failed.pdf") {
		return errors.New("test storage failure")
	}
	if strings.HasSuffix(name, "-missing.pdf") {
		return os.ErrNotExist
	}
	return nil
}

func TestScaleP21SW133RetainedUploadReleaseDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			prefix := idgen.New("sw133-")
			other, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW133 other", prefix+"other", "us")
			if err != nil {
				t.Fatal(err)
			}
			f.resources.workspaces = append(f.resources.workspaces, other.Workspace.URLKey)
			names := []string{}
			for _, suffix := range []string{"trash", "own", "foreign", "foreign-owner", "free", "failed", "missing", "markdown"} {
				names = append(names, prefix+"-"+suffix+".pdf")
			}
			names = append(names, prefix+"-free.pdf", "../invalid", "", "slash/file.pdf")
			now := time.Now().UTC()
			for _, key := range []string{workspace, other.Workspace.URLKey} {
				if err := repo.MutateWorkspace(t.Context(), key, "scale_p21.sw133_seed", "", nil, func(data *domain.Bootstrap) error {
					attachment := prefix + "-own.pdf"
					if key == other.Workspace.URLKey {
						attachment = prefix + "-foreign.pdf"
					}
					data.Issues = append(data.Issues, domain.Issue{
						ID: idgen.New("sw133-issue-"), Identifier: "SW133-1", Title: "Retains upload",
						Team: data.Teams[0], CreatedAt: now, UpdatedAt: now,
						Attachments: []domain.Attachment{{ID: idgen.New("sw133-attachment-"), URL: "/uploads/" + attachment, CreatedAt: now}},
					})
					if key == workspace {
						raw, err := json.Marshal(map[string]string{"url": "/uploads/" + prefix + "-trash.pdf", "description": "embedded /uploads/" + prefix + "-markdown.pdf"})
						if err != nil {
							return err
						}
						data.Trash = []domain.TrashEntry{{ID: prefix + "-trash", Payload: raw}}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			var expectedDeleted []string
			var expectedPresent []bool
			expectedQueries := 0
			for _, source := range []*store.SQLiteStore{repo, view} {
				for _, name := range names[:8] {
					owner := workspace
					if strings.HasSuffix(name, "-foreign-owner.pdf") {
						owner = other.Workspace.URLKey
					}
					_, err := repo.FileUploadByObject(t.Context(), name)
					if errors.Is(err, store.ErrFileUploadNotFound) {
						if err := repo.RecordStoredUpload(t.Context(), store.FileUpload{
							WorkspaceKey: owner, UserID: f.data.Viewer.ID, ObjectKey: name, Filename: "fixture.pdf",
							ContentType: "application/pdf", Purpose: "sw133", ActualSize: 1,
						}, now); err != nil {
							t.Fatal(err)
						}
					} else if err != nil {
						t.Fatal(err)
					}
				}
				storage := &scaleP21SW133Storage{t: t, repo: source}
				service := &server{store: source, objectStore: storage}
				queries := 0
				ctx, cancel := context.WithCancel(store.WithSQLStatementObserver(store.ContextWithActor(t.Context(), f.data.Viewer), func(string) { queries++ }))
				cancel()
				before := slices.Clone(names)
				service.releaseTrashedUploads(ctx, workspace, names)
				if !reflect.DeepEqual(names, before) {
					t.Fatal("release changed caller candidates")
				}
				present := make([]bool, 0, 8)
				for _, name := range names[:8] {
					_, err := repo.FileUploadByObject(t.Context(), name)
					if err != nil && !errors.Is(err, store.ErrFileUploadNotFound) {
						t.Fatal(err)
					}
					present = append(present, err == nil)
				}
				if source == repo {
					expectedDeleted, expectedPresent = slices.Clone(storage.deleted), present
					expectedQueries = queries
					want := []string{prefix + "-free.pdf", prefix + "-failed.pdf", prefix + "-missing.pdf", prefix + "-markdown.pdf", prefix + "-free.pdf"}
					if !reflect.DeepEqual(expectedDeleted, want) || !reflect.DeepEqual(present, []bool{true, true, true, true, false, true, false, false}) {
						t.Fatal("literal release fixture does not cover expected protections/results", storage.deleted, present)
					}
				} else if !reflect.DeepEqual(storage.deleted, expectedDeleted) || !reflect.DeepEqual(present, expectedPresent) || queries != expectedQueries {
					t.Fatal("bounded upload release differs", storage.deleted, expectedDeleted, present, expectedPresent)
				}
				t.Log("bounded / release SQL", source.BoundedMetadataEnabled(), queries)
				if source.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("upload release retained metadata work")
				}
			}
		})
	}
}
