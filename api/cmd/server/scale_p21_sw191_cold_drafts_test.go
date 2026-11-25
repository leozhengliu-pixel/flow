package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW191DraftFixture struct {
	scaleP21M2Fixture
	project, document, issue, draft, beforeDecode string
	reset                                         func(bool)
}

func scaleP21SW191DraftSetup(t *testing.T, repo *store.SQLiteStore) scaleP21SW191DraftFixture {
	t.Helper()
	f := scaleP21M2Setup(t, repo)
	prefix, workspace := idgen.New("sw191-draft-"), f.data.Workspace.URLKey
	result := scaleP21SW191DraftFixture{scaleP21M2Fixture: f, project: prefix + "project", document: prefix + "document", issue: prefix + "issue", draft: prefix + "draft", beforeDecode: prefix + "decode-project"}
	at := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw191_draft_seed", "", nil, func(data *domain.Bootstrap) error {
		var team domain.Team
		for _, value := range data.Teams {
			if value.ID == f.public {
				team = value
			}
		}
		data.Projects = append(data.Projects,
			domain.Project{ID: result.project, TeamIDs: []string{f.public}, Description: strings.Repeat("cold large project ", 20000)},
			domain.Project{ID: prefix + "duplicate", TeamIDs: []string{f.public}},
			domain.Project{ID: prefix + "duplicate", TeamIDs: []string{"missing"}},
		)
		data.Documents = append(data.Documents, domain.Document{ID: result.document, SlugID: prefix + "doc-slug", PreviousSlugIDs: []string{prefix + "doc-old"}, Creator: data.Viewer})
		data.Issues = append(data.Issues, domain.Issue{ID: result.issue, Identifier: "SW191-801", Team: team, Creator: data.Viewer, State: data.States[0], CreatedAt: at, UpdatedAt: at})
		data.Drafts = append(data.Drafts, domain.Draft{ID: result.draft, UserID: f.data.Viewer.ID, Type: "issue", Body: "original", ClientKey: prefix + "client", CreatedAt: at, UpdatedAt: at})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	baseline, found := repo.BootstrapFor(workspace)
	if !found {
		t.Fatal("draft baseline missing")
	}
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	result.reset = func(reload bool) {
		t.Helper()
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		var data domain.Bootstrap
		if err := json.Unmarshal(saved, &data); err != nil {
			t.Fatal(err)
		}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw191_draft_reset", "", nil, func(target *domain.Bootstrap) error {
			*target = data
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if reload {
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
	}
	result.reset(false)
	return result
}

func TestScaleP21SW191ColdDraftValidatorDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21SW191DraftSetup(t, repo)
		cases := []domain.Draft{
			{Type: "issue"}, {Type: "loop"}, {Type: "unsupported"},
			{Type: "comment"}, {Type: "comment", ResourceID: f.issue},
			{Type: "project_update", ResourceID: f.project},
			{Type: "project_update", ResourceID: "m2-unrelated"},
			{Type: "project_update", ResourceID: "foreign-project"},
			{Type: "document", ResourceID: f.document},
			{Type: "document", ResourceID: "foreign-document"},
			{Type: "comment", ResourceID: f.project, Metadata: map[string]any{"resourceType": "project"}},
			{Type: "comment", ResourceID: "m2-unrelated", Metadata: map[string]any{"resourceType": "project"}},
			{Type: "comment", ResourceID: f.public, Metadata: map[string]any{"resourceType": "team"}},
			{Type: "comment", ResourceID: f.document, Metadata: map[string]any{"resourceType": "document"}},
			{Type: "issue", Title: strings.Repeat("x", 513)},
			{Type: "issue", Body: strings.Repeat("x", 1<<20+1)},
		}
		for _, reload := range []bool{false, true} {
			f.reset(reload)
			f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
				for _, development := range []bool{false, true} {
					var expected []string
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						service := &server{store: source, authDisabled: development}
						request := f.literalRequest(user, key, nil)
						request.Method, request.URL.Path = http.MethodPost, "/api/drafts"
						actual := []string{}
						err := source.WithColdMetadataRead(request.Context(), "server/advanced_features.go:createDraft", func(ctx context.Context, borrowed *store.SQLiteStore) error {
							validate, err := service.draftRecordValidator(request.WithContext(ctx), borrowed)
							if err != nil {
								return err
							}
							for _, draft := range cases {
								message := ""
								if err := validate(nil, draft); err != nil {
									message = err.Error()
								}
								actual = append(actual, message)
							}
							return nil
						})
						if err != nil {
							t.Fatal("cold validator construction", err)
						}
						if source == repo {
							expected = actual
						} else if !reflect.DeepEqual(expected, actual) {
							t.Fatalf("reload=%t development=%t legacy=%v cold=%v", reload, development, expected, actual)
						}
					}
				}
			})
		}
		if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("draft validation escaped its cold lifetime")
		}
	}
}

type scaleP21SW191ReadHook struct {
	io.Reader
	before func()
	once   sync.Once
}

func (r *scaleP21SW191ReadHook) Read(data []byte) (int, error) {
	r.once.Do(r.before)
	return r.Reader.Read(data)
}

func scaleP21SW191NormalizeDraft(t *testing.T, raw []byte, generatedID string, started, finished time.Time) any {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	var walk func(any) any
	walk = func(value any) any {
		switch typed := value.(type) {
		case string:
			if generatedID != "" && typed == generatedID {
				return "<generated-draft>"
			}
			if at, err := time.Parse(time.RFC3339Nano, typed); err == nil && !at.Before(started) && !at.After(finished) {
				return "<generated-clock>"
			}
		case []any:
			for index := range typed {
				typed[index] = walk(typed[index])
			}
		case map[string]any:
			for key := range typed {
				typed[key] = walk(typed[key])
			}
		}
		return value
	}
	return walk(value)
}

func TestScaleP21SW191ColdDraftRoutesAndCaptureTiming(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21SW191DraftSetup(t, repo)
		workspace := f.data.Workspace.URLKey
		for _, reload := range []bool{false, true} {
			for _, test := range []struct {
				name, method string
				input        map[string]any
				hook         bool
			}{
				{"create", http.MethodPost, map[string]any{"type": "project_update", "resourceId": f.project, "body": "created"}, false},
				{"update", http.MethodPatch, map[string]any{"type": "comment", "resourceId": f.issue, "body": "updated", "metadata": map[string]any{"resourceType": "issue"}}, false},
				{"private", http.MethodPost, map[string]any{"type": "project_update", "resourceId": "m2-unrelated"}, false},
				{"missing", http.MethodPost, map[string]any{"type": "comment", "resourceId": "foreign-issue"}, false},
				{"capture-after-decode", http.MethodPost, map[string]any{"type": "project_update", "resourceId": f.beforeDecode}, true},
			} {
				var expected any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					f.reset(reload)
					raw, _ := json.Marshal(test.input)
					request := f.literalRequest(f.data.Viewer, nil, nil)
					request.Method, request.URL.Path = test.method, "/api/drafts"
					if test.method == http.MethodPatch {
						request.URL.Path += "/" + f.draft
						request.SetPathValue("id", f.draft)
					}
					reader := io.Reader(bytes.NewReader(raw))
					if test.hook {
						reader = &scaleP21SW191ReadHook{Reader: reader, before: func() {
							if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw191_before_decode", "", nil, func(data *domain.Bootstrap) error {
								data.Projects = append(data.Projects, domain.Project{ID: f.beforeDecode, TeamIDs: []string{f.public}})
								return nil
							}); err != nil {
								t.Fatal(err)
							}
							if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
								t.Fatal(err)
							}
						}}
					}
					request.Body = io.NopCloser(reader)
					response := httptest.NewRecorder()
					started := time.Now().UTC()
					service := &server{store: source}
					if test.method == http.MethodPatch {
						service.updateDraft(response, request)
					} else {
						service.createDraft(response, request)
					}
					finished := time.Now().UTC()
					var draft domain.Draft
					generated := ""
					if response.Code == http.StatusCreated && json.Unmarshal(response.Body.Bytes(), &draft) == nil {
						generated = draft.ID
					}
					var drafts []json.RawMessage
					if err := repo.StreamMetadataRecords(t.Context(), workspace, "drafts", func(_ store.RecordKey, reader io.Reader) error {
						raw, err := io.ReadAll(reader)
						drafts = append(drafts, raw)
						return err
					}); err != nil {
						t.Fatal(err)
					}
					rows, _ := json.Marshal(drafts)
					actual := []any{response.Code, scaleP21SW191NormalizeDraft(t, response.Body.Bytes(), generated, started, finished), scaleP21SW191NormalizeDraft(t, rows, generated, started, finished)}
					if source == repo {
						expected = actual
					} else if !reflect.DeepEqual(expected, actual) {
						t.Fatalf("%s reload=%t legacy=%v cold=%v", test.name, reload, expected, actual)
					}
					if test.hook && response.Code != http.StatusCreated {
						t.Fatal("validator captured metadata before decoding the request", response.Code, response.Body.String())
					}
				}
			}
		}
		// The validator captures metadata before the mutation callback. A later
		// metadata edit must not silently change that captured input.
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			f.reset(false)
			request := f.literalRequest(f.data.Viewer, nil, nil)
			request.Method, request.URL.Path = http.MethodPost, "/api/drafts"
			err := source.WithColdMetadataRead(request.Context(), "server/advanced_features.go:createDraft", func(ctx context.Context, borrowed *store.SQLiteStore) error {
				validate, err := (&server{store: source}).draftRecordValidator(request.WithContext(ctx), borrowed)
				if err != nil {
					return err
				}
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw191_after_capture", "", nil, func(data *domain.Bootstrap) error {
					data.Projects = slices.DeleteFunc(data.Projects, func(item domain.Project) bool { return item.ID == f.project })
					return nil
				}); err != nil {
					return err
				}
				if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					return err
				}
				if err := validate(&domain.Bootstrap{}, domain.Draft{Type: "project_update", ResourceID: f.project}); err != nil {
					t.Fatal("validator lost its constructor-time metadata", err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("draft route escaped its cold lifetime")
		}
	}
}
