package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW165Fixture(t *testing.T) (scaleP09Fixture, string) {
	t.Helper()
	scaleP21SW143Setup(t)
	f, issue := scaleP21IssuePersistenceFixture(t, nil, nil)
	prefix := idgen.New("sw165-")
	at := time.Date(2026, 11, 15, 4, 5, 6, 789, time.UTC)
	scaleP21SW63Seed(t, f, issue, at)
	scaleP21SW143Seed(t, f, prefix, "Small bootstrap body <>&")
	if err := f.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw165_activity", "", nil, func(data *domain.Bootstrap) error {
		data.Activities[issue.ID] = []domain.ActivityEvent{{
			ID: prefix + "activity", Type: "comment.created", Actor: data.Viewer,
			CreatedAt: at, Metadata: domain.ActivityMetadata{"commentId": issue.ID + "-root"},
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return f, prefix
}

func scaleP21SW165Compare(t *testing.T, ctx context.Context, want domain.Bootstrap, read *GeneralBootstrapRead) {
	t.Helper()
	// The general outline retains update maps, unlike the paged issue
	// projection. Only these five dead HTTP fields are omitted early.
	want.ProviderJobs, want.CommentActions = nil, nil
	want.LoopRuns, want.LoopVersions, want.LoopAttachments = nil, nil, nil
	// The HTTP encoder supplies these four collections from the retained SQL
	// snapshot. Keep the materialized issue outlines in this comparison too.
	want.Comments, want.Activities = map[string][]domain.Comment{}, map[string][]domain.ActivityEvent{}
	want.Notifications, want.NotificationDeliveries = []domain.Notification{}, []domain.NotificationDelivery{}
	var actual bytes.Buffer
	if err := read.WriteJSON(ctx, &actual); err != nil {
		t.Fatal("encode general bootstrap", err)
	}
	expected := scaleP21SW143Encode(t, want)
	if !bytes.Equal(expected, actual.Bytes()) {
		position := 0
		for position < min(len(expected), actual.Len()) && expected[position] == actual.Bytes()[position] {
			position++
		}
		t.Fatalf("general bootstrap outline differs at byte %d, lengths %d/%d: legacy=%q bounded=%q",
			position, len(expected), actual.Len(),
			expected[max(0, position-80):min(len(expected), position+140)],
			actual.Bytes()[max(0, position-80):min(actual.Len(), position+140)])
	}
}

func TestScaleP21SW165GeneralBootstrapSnapshotChoice(t *testing.T) {
	f, prefix := scaleP21SW165Fixture(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	for _, phase := range []string{"warm-exact", "stale-installed", "unknown-installed", "same-version-reloaded"} {
		t.Run(phase, func(t *testing.T) {
			switch phase {
			case "stale-installed":
				canonical, found := f.WorkspaceMetadata(workspace)
				if !found {
					t.Fatal("canonical source-choice fixture missing")
				}
				canonical.Settings[prefix+"source-choice"] = "canonical SQL metadata"
				// Deliberately leave the owner's installed snapshot and its
				// known revision behind this canonical write.
				writeFullMetadata(t, f.SQLiteStore, workspace, canonical)
			case "unknown-installed":
				f.mu.Lock()
				f.forgetCopyRevisionLocked(workspace)
				f.mu.Unlock()
			case "same-version-reloaded":
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			for _, userID := range []string{"", f.data.Viewer.ID} {
				t.Run(fmt.Sprint("authenticated=", userID != ""), func(t *testing.T) {
					literal, release, err := f.BeginWorkspaceRead(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
					want, err := f.BootstrapOutline(literal, workspace, userID)
					release()
					if err != nil {
						t.Fatal(err)
					}
					native, ok := want.Settings[prefix+"native"].(map[string]any)
					if !ok {
						t.Fatal("source-choice native map fixture missing")
					}
					if phase == "warm-exact" {
						if _, nativeInteger := native["integer"].(int64); !nativeInteger {
							t.Fatalf("warm oracle did not retain native integer: %T", native["integer"])
						}
					} else if want.Settings[prefix+"source-choice"] != "canonical SQL metadata" {
						t.Fatal("stale/unknown oracle did not choose canonical SQL metadata")
					}
					beforeWork, peakWork := view.MetadataWorkUsedForTesting(), view.MetadataWorkUsedForTesting()
					observed := WithSQLStatementObserver(t.Context(), func(string) {
						peakWork = max(peakWork, view.MetadataWorkUsedForTesting())
					})
					read, err := view.BeginGeneralBootstrapRead(observed, workspace, userID, nil)
					if err != nil {
						t.Fatalf("constructor: %v; before_work=%d SQL_observed_peak=%d after_work=%d limit=%d max_state=%d",
							err, beforeWork, peakWork, view.MetadataWorkUsedForTesting(), view.metadataResidency.work.limit, view.maxStateBytes)
					}
					defer read.Close()
					scaleP21SW165Compare(t, t.Context(), want, read)
					read.Close()
					read.Close()
					if !reflect.DeepEqual(read.Data, domain.Bootstrap{}) || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
						t.Fatal("general constructor retained work/data or decoded a full workspace")
					}
				})
			}
		})
	}
	if read, err := f.BeginGeneralBootstrapRead(t.Context(), workspace, "", nil); read != nil || !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("flag-off general constructor activated", err)
	}
}

type scaleP21SW165ContentRow struct {
	Resource string
	Raw      json.RawMessage
}

func TestScaleP21SW165GeneralBootstrapWalkersRetainSnapshot(t *testing.T) {
	f, _ := scaleP21SW165Fixture(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	literal, release, err := f.BeginWorkspaceRead(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	query := IssueRecordQuery{Workspace: workspace}
	var wantIssues []domain.Issue
	if err := f.WalkIssueRecords(literal, query, func(issue domain.Issue) error {
		wantIssues = append(wantIssues, issue)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	wantContent := map[string][]scaleP21SW165ContentRow{}
	for _, kind := range []string{"comment", "activity", "notification", "delivery"} {
		if err := f.WalkContentRecords(literal, workspace, kind, func(resource string, raw json.RawMessage) error {
			wantContent[kind] = append(wantContent[kind], scaleP21SW165ContentRow{resource, slices.Clone(raw)})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if len(wantContent[kind]) == 0 {
			t.Fatal("walker fixture has no rows", kind)
		}
	}
	if len(wantIssues) == 0 {
		t.Fatal("issue walker fixture has no rows")
	}
	read, err := view.BeginGeneralBootstrapRead(t.Context(), workspace, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	snapshot, ok := read.Context(t.Context()).Value(workspaceReadKey{}).(workspaceRead)
	if !ok || snapshot.database != f.db || snapshot.workspace != workspace || snapshot.reader == nil {
		t.Fatal("general read did not hand consumers its SQL snapshot")
	}
	checkOpen := func() {
		t.Helper()
		var one int
		if err := snapshot.reader.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
			t.Fatal("a general walker closed the shared read transaction", err)
		}
	}
	var gotIssues []domain.Issue
	if err := read.WalkIssueRecords(t.Context(), query, func(issue domain.Issue) error {
		gotIssues = append(gotIssues, issue)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotIssues, wantIssues) {
		t.Fatal("general issue walker differs from literal snapshot")
	}
	checkOpen()
	for _, kind := range []string{"comment", "activity", "notification", "delivery"} {
		var got []scaleP21SW165ContentRow
		if err := read.WalkContentRecords(t.Context(), workspace, kind, func(resource string, raw json.RawMessage) error {
			got = append(got, scaleP21SW165ContentRow{resource, slices.Clone(raw)})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, wantContent[kind]) {
			t.Fatal("general content walker differs from literal snapshot", kind)
		}
		checkOpen()
	}
	sentinel := errors.New("stop this borrowed walk")
	if err := read.WalkIssueRecords(t.Context(), query, func(domain.Issue) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal("issue walker lost callback error", err)
	}
	checkOpen()
	if err := read.WalkContentRecords(t.Context(), workspace, "comment", func(string, json.RawMessage) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal("content walker lost callback error", err)
	}
	checkOpen()
	read.Close()
	var one int
	if err := snapshot.reader.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); !errors.Is(err, sql.ErrTxDone) {
		t.Fatal("closing general read did not close its owned snapshot", err)
	}
	original := literal.Value(workspaceReadKey{}).(workspaceRead)
	if err := original.reader.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatal("general read closed an independent caller snapshot", err)
	}
}

func TestScaleP21SW165GeneralBootstrapRejectsNestedCallerSnapshot(t *testing.T) {
	f, prefix := scaleP21SW165Fixture(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	literal, release, err := f.BeginWorkspaceRead(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	caller := literal.Value(workspaceReadKey{}).(workspaceRead)
	for _, matching := range []bool{true, false} {
		t.Run(fmt.Sprint("matching=", matching), func(t *testing.T) {
			handed := caller
			handed.metadata = domain.Bootstrap{Workspace: domain.Workspace{ID: "poison", URLKey: workspace},
				Settings: map[string]any{prefix + "poison": "This is not BeginWorkspaceRead's fresh metadata"}}
			if !matching {
				handed.workspace = prefix + "other"
			}
			ctx := context.WithValue(t.Context(), workspaceReadKey{}, handed)
			_, nestedRelease, literalErr := f.BeginWorkspaceRead(ctx, workspace)
			if nestedRelease != nil {
				nestedRelease()
			}
			read, err := view.BeginGeneralBootstrapRead(ctx, workspace, "", nil)
			if read != nil {
				read.Close()
			}
			if literalErr == nil || read != nil || err == nil || reflect.TypeOf(literalErr) != reflect.TypeOf(err) || literalErr.Error() != err.Error() {
				t.Fatal("nested snapshot constructor changed the literal rejection", literalErr, err)
			}
			var one int
			if err := caller.reader.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
				t.Fatal("general close consumed independent caller transaction", err)
			}
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("caller-context constructor leaked work")
			}
		})
	}
}

func TestScaleP21SW165GeneralBootstrapCancellationQuotaRelease(t *testing.T) {
	f, _ := scaleP21SW165Fixture(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := view.BeginGeneralBootstrapRead(canceled, workspace, "", nil); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled general constructor escaped", err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	read, err := view.BeginGeneralBootstrapRead(t.Context(), workspace, "", nil)
	view.metadataResidency.work.limit = limit
	if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("unadmitted general constructor escaped or leaked", err)
	}
	read, err = view.BeginGeneralBootstrapRead(t.Context(), workspace, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	held := view.MetadataWorkUsedForTesting()
	if err := read.ReserveOutputWork(t.Context(), 4096); err != nil || view.MetadataWorkUsedForTesting() < held+4096 {
		t.Fatal("general output work was not retained through output", err)
	}
	view.metadataResidency.work.limit = view.MetadataWorkUsedForTesting()
	err = read.ReserveOutputWork(t.Context(), 1)
	view.metadataResidency.work.limit = limit
	if !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("general output crossed its work allowance", err)
	}
	var output bytes.Buffer
	view.releaseMetadataLogicalSources()
	if err := read.WriteJSON(t.Context(), &output); err != nil || output.Len() == 0 {
		t.Fatal("general reader borrowed installed source lifetime", err)
	}
	output.Reset()
	if err := read.WriteJSON(canceled, &output); !errors.Is(err, context.Canceled) || output.Len() != 0 {
		t.Fatal("canceled general encoder published output", err)
	}
	if err := read.WalkIssueRecords(canceled, IssueRecordQuery{Workspace: workspace}, func(domain.Issue) error {
		t.Fatal("canceled issue walk called visitor")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal("general issue walk lost cancellation", err)
	}
	if err := read.WalkContentRecords(canceled, workspace, "comment", func(string, json.RawMessage) error {
		t.Fatal("canceled content walk called visitor")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal("general content walk lost cancellation", err)
	}
	read.Close()
	read.Close()
	if !reflect.DeepEqual(read.Data, domain.Bootstrap{}) || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("general reader retained data/work or decoded complete metadata")
	}
	if err := read.WriteJSON(t.Context(), &bytes.Buffer{}); err == nil {
		t.Fatal("closed general reader still encoded output")
	}
}

func TestScaleP21SW165GeneralBootstrapRetainedNativeMaps(t *testing.T) {
	f, prefix := scaleP21SW165Fixture(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	priorWork, priorNative := 0, uint64(0)
	for _, copies := range []int{1, 10} {
		if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw165_native_maps", "", nil, func(data *domain.Bootstrap) error {
			native := map[string]any{
				"nativeInteger": int64(9007199254740993), "nativeNumber": json.Number("1e30"),
				"nil": []string(nil), "empty": []string{},
			}
			for index := range 64 * copies {
				native[fmt.Sprintf("native-%04d", index)] = map[string]any{
					"body": strings.Repeat("Retained scalar metadata ", 8), "ordinal": int64(index), "children": []string{"first", "second"},
				}
			}
			data.Settings[prefix+"retained-native-map"] = native
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
			t.Fatal(err)
		}
		read, err := view.BeginGeneralBootstrapRead(t.Context(), workspace, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		held := view.MetadataWorkUsedForTesting()
		native, err := metadataAggregateNativeCost(t.Context(), reflect.ValueOf(read.Data.Settings), 0)
		if err != nil {
			t.Fatal(err)
		}
		if uint64(held) < native || copies == 10 && (held <= priorWork || native <= priorNative) {
			t.Fatal("retained outline admission omitted growing native map values", copies, held, native, priorWork, priorNative)
		}
		t.Logf("native_map_copies=%d retained_work=%d native_map_cost=%d", copies, held, native)
		priorWork, priorNative = held, native
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("native-map outline retained allowance after close")
		}
	}
}

func TestScaleP21SW165GeneralBootstrapOutputTransfer(t *testing.T) {
	f, _ := scaleP21SW165Fixture(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	checkClosed := func(t *testing.T, read *GeneralBootstrapRead, tx *sqlTx) {
		t.Helper()
		if !reflect.DeepEqual(read.Data, domain.Bootstrap{}) || !reflect.DeepEqual(read.references, domain.Bootstrap{}) ||
			read.workspace != "" || read.tx != nil || read.owner != nil || read.body != nil || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("output transfer did not clear general data/source/work and SQL ownership")
		}
		var one int
		if err := tx.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); !errors.Is(err, sql.ErrTxDone) {
			t.Fatal("transferred general close left its SQL snapshot open", err)
		}
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("repeated transferred close changed released allowance")
		}
	}
	t.Run("accepted-live-registrar", func(t *testing.T) {
		read, err := view.BeginGeneralBootstrapRead(t.Context(), workspace, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		tx, held := read.tx, view.MetadataWorkUsedForTesting()
		var callback func()
		registrations := 0
		ended := false
		ctx := WithMetadataOutputOwner(t.Context(), func(closeChild func()) bool {
			if ended {
				return false
			}
			registrations++
			callback = closeChild
			return true
		})
		if err := read.TransferOutput(ctx); err != nil {
			t.Fatal(err)
		}
		if registrations != 1 || callback == nil || held == 0 || view.MetadataWorkUsedForTesting() != held ||
			read.tx != tx || read.owner == nil || read.owner.source == nil || reflect.DeepEqual(read.Data, domain.Bootstrap{}) {
			t.Fatal("accepted output transfer released or replaced its live general read")
		}
		var one int
		if err := tx.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
			t.Fatal("accepted transfer closed SQL before containing output ended", err)
		}
		var output bytes.Buffer
		if err := read.WriteJSON(ctx, &output); err != nil || output.Len() == 0 {
			t.Fatal("transferred general output lost its body source", err)
		}
		ended = true
		callback()
		checkClosed(t, read, tx)
		callback()
		checkClosed(t, read, tx)
	})
	for _, mode := range []string{"missing-registrar", "nil-registrar", "closed-registrar"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			switch mode {
			case "nil-registrar":
				ctx = WithMetadataOutputOwner(ctx, nil)
			case "closed-registrar":
				owner, err := view.BeginMetadataJSONRead(ctx)
				if err != nil {
					t.Fatal(err)
				}
				ctx = owner.Context(ctx)
				owner.Close()
			}
			read, err := view.BeginGeneralBootstrapRead(t.Context(), workspace, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close()
			tx := read.tx
			if err := read.TransferOutput(ctx); !errors.Is(err, ErrBoundedMetadataCoverage) {
				t.Fatal("invalid output registrar accepted general read", mode, err)
			}
			checkClosed(t, read, tx)
			if mode != "missing-registrar" && !errors.Is(metadataOutputError(ctx), ErrBoundedMetadataCoverage) {
				t.Fatal("invalid output transfer did not record containing operation failure", mode)
			}
		})
	}
	t.Run("nil-reader", func(t *testing.T) {
		var read *GeneralBootstrapRead
		ctx := WithMetadataOutputOwner(t.Context(), func(func()) bool {
			t.Fatal("nil general reader registered output")
			return false
		})
		if err := read.TransferOutput(ctx); !errors.Is(err, ErrBoundedMetadataCoverage) {
			t.Fatal("nil general reader transfer did not refuse safely", err)
		}
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("nil general transfer retained work")
		}
	})
}
