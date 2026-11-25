package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"

	"github.com/go-sql-driver/mysql"
)

type scaleP21SW301Case struct {
	name, field string
	body, deep  int
	marker      bool
}

type scaleP21SW301Image struct {
	Workspace domain.Workspace
	Documents []domain.Document
	Projects  []domain.Project
	stamp     copyStamp
	owner     *ResourceRead
}

func (image *scaleP21SW301Image) Close() {
	owner := image.owner
	*image = scaleP21SW301Image{}
	if owner != nil {
		owner.Close()
	}
}

func scaleP21SW301Read(t *testing.T, source *SQLiteStore, workspace string) scaleP21SW301Image {
	t.Helper()
	if !source.BoundedMetadataEnabled() {
		data, found := source.storedWorkspace(workspace)
		if !found {
			t.Fatal("literal retry source missing")
		}
		return scaleP21SW301Image{Workspace: data.Workspace, Documents: data.Documents, Projects: data.Projects}
	}
	logical, err := source.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	owner := &ResourceRead{store: source, source: logical}
	result := scaleP21SW301Image{stamp: logical.Stamp(), owner: owner}
	if err := owner.reserve(t.Context(), 64<<10); err != nil {
		result.Close()
		t.Fatal(err)
	}
	for _, name := range []string{"workspace", "documents", "projects"} {
		index, _, _ := metadataLogicalField(name)
		node := logical.fields[index]
		if err := owner.reserve(t.Context(), node.cost); err != nil {
			result.Close()
			t.Fatal(err)
		}
		value, err := node.Value(t.Context(), metadataWorkBytes)
		if err != nil {
			result.Close()
			t.Fatal(err)
		}
		switch name {
		case "workspace":
			result.Workspace = value.Interface().(domain.Workspace)
		case "documents":
			result.Documents = value.Interface().([]domain.Document)
		case "projects":
			result.Projects = value.Interface().([]domain.Project)
		}
	}
	return result
}

func scaleP21SW301LogicalStack() bool {
	var callers [128]uintptr
	count := runtime.Callers(2, callers[:])
	frames := runtime.CallersFrames(callers[:count])
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, "refreshConflictedLogicalMetadata") {
			return true
		}
		if !more {
			return false
		}
	}
}

func scaleP21SW301Raw(t *testing.T, source *SQLiteStore, workspace, field, key string) []byte {
	t.Helper()
	var raw []byte
	if err := source.db.QueryRowContext(t.Context(),
		"SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?",
		workspace, field, key).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func scaleP21SW301Deep(t *testing.T, textBytes int, phase string, blocks int) map[string]any {
	t.Helper()
	raw := `{"type":"doc","content":[` + strings.Repeat(`{"type":"blockquote","content":[`, blocks) +
		`{"type":"paragraph","content":[{"type":"text","text":"` + strings.Repeat(phase, textBytes) + `"}]}` +
		strings.Repeat(`]}`, blocks) + `]}`
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestScaleP21SW301MySQLNativeJSONBoundary(t *testing.T) {
	if os.Getenv("FLOW_TEST_DATABASE_DRIVER") != "mysql" {
		t.Skip("MySQL native JSON producer boundary")
	}
	f := scaleP09Database(t)
	for _, blocks := range []int{40, 256} {
		raw, err := json.Marshal(scaleP21SW301Deep(t, 32, "A", blocks))
		if err != nil {
			t.Fatal(err)
		}
		var encoded []byte
		err = f.db.QueryRowContext(t.Context(), `SELECT JSON_EXTRACT(?, '$')`, string(raw)).Scan(&encoded)
		if blocks == 40 {
			if err != nil || !json.Valid(encoded) {
				t.Fatal("required-success MySQL nested producer value", err)
			}
		} else {
			var native *mysql.MySQLError
			if !errors.As(err, &native) || native.Number != 3157 {
				t.Fatal("deep vector must reproduce the literal MySQL native depth refusal", err)
			}
		}
	}
}

// The conflict is produced by the real three-way rebase, not by constructing
// metadataConflictError or by a callback returning ErrConcurrentWrite.
func scaleP21SW301Conflict(t *testing.T, owner *SQLiteStore, workspace, field, key string) error {
	t.Helper()
	stale, found := owner.storedWorkspace(workspace)
	if !found {
		t.Fatal("stale conflict owner missing")
	}
	before, after := cloneCallbackInput(stale), cloneCallbackInput(stale)
	if field == "documents" {
		after.Documents[0].Title = "local-edit"
	} else {
		after.Projects[0].Name = "local-edit"
	}
	tx, err := owner.db.BeginTx(t.Context(), owner.recordTxOptions())
	if err != nil {
		t.Fatal(err)
	}
	metadata, ok := metadataFieldByName(field)
	if !ok {
		t.Fatal("invalid fixture field")
	}
	_, _, conflict := owner.rebaseMetadata(t.Context(), tx, workspace, &before, &after, []metadataField{metadata}, nil)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	keys, typed := conflictedMetadataKeys(conflict)
	if !errors.Is(conflict, ErrConcurrentWrite) || !typed || !reflect.DeepEqual(keys, []metadataRecordKey{{field, key}}) {
		t.Fatal("fixture did not produce its actual single-key metadata conflict", keys, conflict)
	}
	return conflict
}

func TestScaleP21SW301SharedRetryFramingAdversaries(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "false")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	if metadataWorkBytes != 256<<20 {
		t.Fatal("retry adversaries changed the production work allowance")
	}
	for driver, pair := range peerStoreSets(t) {
		t.Run(driver, func(t *testing.T) {
			blocks := 256
			if driver == "mysql" {
				// The native SQL producer refuses the deeper vector before
				// either reader runs; the boundary test pins that refusal.
				blocks = 40
			}
			for _, row := range []scaleP21SW301Case{
				{name: "document-4MiB", field: "documents", body: 4 << 20},
				{name: "project-4MiB", field: "projects", body: 4 << 20},
				{name: "document-deep-large", field: "documents", deep: 512 << 10},
				{name: "document-deep-small", field: "documents", deep: 32},
				{name: "document-current-body-marker", field: "documents", body: 48 << 10, marker: true},
			} {
				for _, reload := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/reload=%t", row.name, reload), func(t *testing.T) {
						peer, owner := pair[0], pair[1]
						user, token, err := peer.Register(t.Context(), "SW301 owner", idgen.New("sw301-")+"@example.test", "sw301-password")
						if err != nil {
							t.Fatal(err)
						}
						if err := peer.VerifyEmail(t.Context(), token); err != nil {
							t.Fatal(err)
						}
						created, err := peer.CreateWorkspace(ContextWithActor(t.Context(), user), "SW301 owned", idgen.New("sw301-workspace-"), "us")
						if err != nil {
							t.Fatal(err)
						}
						workspace := created.Workspace.URLKey
						t.Cleanup(func() {
							ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
							defer cancel()
							if err := peer.DeleteWorkspace(ctx, workspace); err != nil {
								t.Error(err)
							}
							if err := peer.EraseAccount(ctx, user.ID); err != nil {
								t.Error(err)
							}
						})
						at := time.Date(2026, 11, 17, 1, 2, 3, 123456789, time.UTC)
						doc := domain.Document{ID: "sw301-document", SlugID: "sw301-document", Title: "stale",
							Content: "seed", Creator: user, ProjectIDs: []string{}, TeamIDs: []string{}, CreatedAt: at, UpdatedAt: at}
						project := domain.Project{ID: "sw301-project", Name: "stale", Description: "seed",
							TeamIDs: []string{created.Teams[0].ID}, CreatedAt: at, UpdatedAt: at}
						key := doc.ID
						if row.field == "projects" {
							key = project.ID
						}
						edit := func(t *testing.T, phase string) {
							metadataEdit(t, peer, workspace, "scale_p21.sw301_peer", key, func(data *domain.Bootstrap) error {
								if row.field == "documents" {
									target := &data.Documents[0]
									target.Title, target.Content = "peer-"+phase, strings.Repeat(phase, row.body)
									if row.deep != 0 {
										target.ContentData = scaleP21SW301Deep(t, row.deep, phase, blocks)
									}
									if row.marker {
										target.Revisions = []domain.DocumentRevision{{ID: "sw301-revision", DocumentID: doc.ID,
											Title: "Revision", Content: target.Content, ContentData: target.ContentData,
											Author: user, CreatedAt: at}}
									}
									data.Documents[1].Title = "unrequested-" + phase
								} else {
									data.Projects[0].Name = "peer-" + phase
									data.Projects[0].Description = strings.Repeat(phase, row.body)
									data.Projects[1].Name = "unrequested-" + phase
								}
								return nil
							})
						}
						var literalDocuments []domain.Document
						var literalProjects []domain.Project
						for _, bounded := range []bool{false, true} {
							t.Run(fmt.Sprintf("bounded=%t", bounded), func(t *testing.T) {
								if err := peer.ReloadWorkspace(t.Context(), workspace); err != nil {
									t.Fatal(err)
								}
								metadataEdit(t, peer, workspace, "scale_p21.sw301_seed", key, func(data *domain.Bootstrap) error {
									data.Workspace.Name = "SW301 seed"
									siblingDoc, siblingProject := doc, project
									siblingDoc.ID, siblingDoc.SlugID = "sw301-doc-sibling", "sw301-doc-sibling"
									siblingProject.ID = "sw301-project-sibling"
									data.Documents, data.Projects = []domain.Document{doc, siblingDoc}, []domain.Project{project, siblingProject}
									return nil
								})
								if err := owner.ReloadWorkspace(t.Context(), workspace); err != nil {
									t.Fatal(err)
								}
								base, found := owner.storedWorkspace(workspace)
								if !found {
									t.Fatal("owner fixture missing")
								}
								base = cloneCallbackInput(base)
								source := owner
								disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
								if bounded {
									source, err = owner.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
									if err != nil {
										t.Fatal(err)
									}
									if source.metadataResidency.work.limit != metadataWorkBytes {
										t.Fatal("required retry success is not using the default work allowance")
									}
									defer func() {
										source.releaseMetadataLogicalSources()
										if source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0 ||
											metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors || recordSpoolBytes.Load() != spools {
											t.Error("retry framing retained work/disk/descriptors or fully decoded metadata")
										}
									}()
								}
								if reload {
									metadataEdit(t, peer, workspace, "scale_p21.sw301_reload", "", func(data *domain.Bootstrap) error {
										data.Workspace.Name = "SW301 actual SQL reload"
										return nil
									})
									before := source.WorkspaceMetadataDecodes()
									if err := source.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
										t.Fatal(err)
									}
									if err := source.ReloadWorkspace(t.Context(), workspace); err != nil {
										t.Fatal(err)
									}
									image := scaleP21SW301Read(t, source, workspace)
									name := image.Workspace.Name
									image.Close()
									if name != "SW301 actual SQL reload" || bounded && source.WorkspaceMetadataDecodes() != before ||
										!bounded && source.WorkspaceMetadataDecodes() <= before {
										t.Fatal("reload leg did not replace the real SQL source without bounded hydration", name)
									}
								}
								// Deep content is introduced only after shallow source capture.
								edit(t, "A")
								rawA := scaleP21SW301Raw(t, peer, workspace, row.field, key)
								large := row.body >= 4<<20 || row.deep >= 512<<10
								if (len(rawA) > MetadataRecordPageMaxBytes) != large ||
									row.marker && !bytes.Contains(rawA, []byte(`"body":"document"`)) {
									t.Fatal("size/marker fixture did not select the intended retry path", len(rawA), row)
								}
								resolved, err := resolveDocumentMarkers(row.field, rawA)
								if err != nil {
									t.Fatal(err)
								}
								expected := cloneCallbackInput(base)
								old := reflect.ValueOf(base.Documents[0])
								kind := reflect.TypeFor[domain.Document]()
								if row.field == "projects" {
									old, kind = reflect.ValueOf(base.Projects[0]), reflect.TypeFor[domain.Project]()
								}
								record, err := decodeMetadataRecord(kind, resolved, old)
								if err != nil {
									t.Fatal(err)
								}
								keepImportInputs(row.field, record, old)
								if row.field == "documents" {
									expected.Documents[0] = record.Interface().(domain.Document)
								} else {
									expected.Projects[0] = record.Interface().(domain.Project)
								}
								conflict := scaleP21SW301Conflict(t, owner, workspace, row.field, key)
								if bounded && row.name == "document-4MiB" && !reload {
									for _, exit := range []string{"canceled-before", "canceled-stream", "denied"} {
										t.Run(exit, func(t *testing.T) {
											before := scaleP21SW301Read(t, source, workspace)
											stamp := before.stamp
											before.Close()
											disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
											ctx, cancel := context.WithCancel(t.Context())
											defer cancel()
											chunks := 0
											ctx = WithSQLStatementObserver(ctx, func(query string) {
												if strings.HasPrefix(query, "SELECT SUBSTR(") && strings.Contains(query, "workspace_metadata_records") && scaleP21SW301LogicalStack() {
													chunks++
													if exit == "canceled-stream" && chunks == 2 {
														cancel()
													}
												}
											})
											if exit == "canceled-before" {
												cancel()
											}
											run := func() error { return source.refreshConflictedLogicalMetadata(ctx, workspace)(conflict) }
											var err error
											if exit == "denied" {
												_, release, holdErr := source.tryAdmitAdditionalMetadataWork(t.Context(), metadataWorkBytes-(1<<20))
												if holdErr != nil {
													t.Fatal(holdErr)
												}
												func() { defer release(); err = run() }()
											} else {
												err = run()
											}
											want := error(context.Canceled)
											if exit == "denied" {
												want = ErrMetadataWorkBudget
											}
											if !errors.Is(err, want) || exit == "canceled-stream" && chunks < 2 {
												t.Fatal("retry failure boundary changed", exit, chunks, err)
											}
											after := scaleP21SW301Read(t, source, workspace)
											unchanged := after.stamp == stamp && reflect.DeepEqual(after.Documents, base.Documents) && reflect.DeepEqual(after.Projects, base.Projects)
											after.Close()
											if !unchanged || source.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk ||
												metadataLogicalDescriptorsUsed.Load() != descriptors || recordSpoolBytes.Load() != spools {
												t.Fatal("failed retry installed a partial field or leaked ownership")
											}
										})
									}
								}
								chunks, cappedSelects, writes := 0, 0, 0
								late := false
								var rawB []byte
								lateEdit := func() {
									if late {
										t.Fatal("snapshot peer edit ran twice")
									}
									late = true
									edit(t, "B")
									rawB = scaleP21SW301Raw(t, peer, workspace, row.field, key)
								}
								ctx := WithSQLStatementObserver(t.Context(), func(query string) {
									if !scaleP21SW301LogicalStack() {
										return
									}
									if strings.HasPrefix(query, "SELECT record_key,") &&
										strings.Contains(query, ",CASE WHEN ") &&
										strings.Contains(query, " FROM workspace_metadata_records WHERE ") {
										cappedSelects++
									}
									lower := strings.ToLower(query)
									if (strings.HasPrefix(lower, "insert") || strings.HasPrefix(lower, "update") || strings.HasPrefix(lower, "delete")) &&
										(strings.Contains(lower, "workspace_metadata_records") || strings.Contains(lower, "workspace_states")) {
										writes++
									}
									if strings.HasPrefix(query, "SELECT SUBSTR(") && strings.Contains(query, "workspace_metadata_records") {
										chunks++
										if large && chunks == 2 {
											lateEdit()
										}
									}
								})
								refresh := source.refreshConflictedMetadata(ctx, workspace)
								if bounded {
									refresh = source.refreshConflictedLogicalMetadata(ctx, workspace)
								}
								if err := refresh(conflict); err != nil {
									t.Fatal("supported retry framing must succeed", row.name, bounded, reload, err)
								}
								if bounded && large && (!late || chunks < 2) || bounded && !large && (chunks != 0 || cappedSelects != 1) || writes != 0 {
									t.Fatal("retry changed its field snapshot or small-query contract", late, chunks, cappedSelects, writes)
								}
								if !late {
									// Literal rows and small frames are captured atomically.
									lateEdit()
								}
								if len(rawA) != len(rawB) || bytes.Equal(rawA, rawB) || !bytes.Equal(rawB, scaleP21SW301Raw(t, peer, workspace, row.field, key)) {
									t.Fatal("genuine later peer edit was absent or overwritten")
								}
								image := scaleP21SW301Read(t, source, workspace)
								if !reflect.DeepEqual(image.Documents, expected.Documents) || !reflect.DeepEqual(image.Projects, expected.Projects) {
									image.Close()
									t.Fatal("retry mixed SQL generations, skipped marker expansion, or refreshed an unrequested sibling")
								}
								if !bounded {
									literalDocuments, literalProjects = image.Documents, image.Projects
								} else if !reflect.DeepEqual(image.Documents, literalDocuments) || !reflect.DeepEqual(image.Projects, literalProjects) {
									image.Close()
									t.Fatal("complete native retry fields differ from literal refresh")
								}
								image.Close()
								if bounded {
									resident, _ := source.storedWorkspace(workspace)
									if len(resident.Documents) != 0 || len(resident.Projects) != 0 || source.MetadataWorkUsedForTesting() != 0 {
										t.Fatal("retry installed native bodies in resident policy or retained output work")
									}
								}
								t.Logf("bounded=%t field=%s bytes=%d chunks=%d capped_selects=%d later_peer=%t", bounded, row.field, len(rawA), chunks, cappedSelects, late)
							})
						}
					})
				}
			}
		})
	}
}
