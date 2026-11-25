package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW209FingerprintDecision(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	fixture := scaleP09Database(t)
	bounded, err := fixture.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer bounded.releaseMetadataLogicalSources()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	for _, mode := range []string{"unchanged", "health", "update-due", "invalid-time-health", "invalid-trash-retained", "invalid-trash-expired"} {
		for _, size := range []int{32, 5 << 20} {
			t.Run(mode+map[bool]string{false: "/small", true: "/large"}[size > 32], func(t *testing.T) {
				original := domain.Bootstrap{Workspace: fixture.data.Workspace, Viewer: fixture.data.Viewer,
					Projects:       []domain.Project{{ID: "sw209", Description: strings.Repeat("p", size)}},
					ProjectUpdates: map[string][]domain.ProjectUpdate{"sw209": {{ID: "sw209-update", Body: strings.Repeat("u", size)}}}}
				if mode == "invalid-time-health" {
					original.Projects[0].UpdatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
				}
				if strings.HasPrefix(mode, "invalid-trash") {
					expiry := now.Add(time.Hour)
					if mode == "invalid-trash-expired" {
						expiry = now.Add(-time.Hour)
					}
					original.Trash = []domain.TrashEntry{{Payload: json.RawMessage(`{"incomplete":`), ExpiresAt: expiry}}
				}
				source, err := captureMetadataLogicalSource(t.Context(), original, copyStamp{workspaceID: original.Workspace.ID})
				if err != nil {
					t.Fatal(err)
				}
				defer source.Release()
				ctx := ContextWithAdvancedScheduleMaintenance(t.Context(), now, 30*24*time.Hour)
				owner := &ResourceRead{store: bounded}
				defer owner.Close()
				profile := advancedScheduleImageProfile{}
				projected, err := profile.inputs(ctx, owner, source)
				if err != nil {
					t.Fatal(err)
				}
				if err := profile.reserveCallback(ctx, owner, projected, nil); err != nil {
					t.Fatal(err)
				}
				fingerprint := func(data domain.Bootstrap) []byte {
					raw, _ := json.Marshal([]any{data.Projects, data.ProjectUpdates, data.Notifications,
						data.NotificationDeliveries, data.Trash, data.IssueSLAs, data.SLAEvents})
					return raw
				}
				literalBefore := fingerprint(original)
				boundedBefore := AdvancedScheduleFingerprint(ctx, projected)
				literalAfter := cloneBootstrap(original)
				switch mode {
				case "health", "invalid-time-health":
					literalAfter.Projects[0].Health, projected.Projects[0].Health = "noUpdate", "noUpdate"
				case "update-due":
					literalAfter.ProjectUpdates["sw209"][0].DueAt, projected.ProjectUpdates["sw209"][0].DueAt = &now, &now
					literalAfter.ProjectUpdates["sw209"][0].Missing, projected.ProjectUpdates["sw209"][0].Missing = true, true
				case "invalid-trash-expired":
					literalAfter.Trash, projected.Trash = []domain.TrashEntry{}, []domain.TrashEntry{}
				}
				boundedAfter := AdvancedScheduleFingerprint(ctx, projected)
				if state := advancedScheduleMaintenance(ctx); state.err != nil {
					t.Fatal("fingerprint changed a literal marshal error into a write error", state.err)
				}
				if bytes.Equal(literalBefore, fingerprint(literalAfter)) != bytes.Equal(boundedBefore, boundedAfter) {
					t.Fatal("opaque omitted fields changed literal no-op decision", mode, size)
				}
				// Verify unchanged bodies and every untouched native occurrence
				// after reconstructing the actual ordinal overlay.
				image, err := profile.image(ctx, owner, source, advancedScheduleMaintenance(ctx).before, projected)
				if err != nil {
					t.Fatal(err)
				}
				defer image.Release()
				index, _, _ := metadataLogicalField("projects")
				var project domain.Project
				if err := image.fields[index].Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
					value, err := node.Value(ctx, int(node.cost))
					if err == nil {
						project = value.Interface().(domain.Project)
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(project, literalAfter.Projects[0]) {
					t.Fatal("ordinal patch replaced omitted immutable project fields")
				}
				owner.Close()
				if bounded.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("fingerprint retained operation work", bounded.MetadataWorkUsedForTesting())
				}
			})
		}
	}
}

func TestScaleP21SW209FingerprintCancellationAndRelease(t *testing.T) {
	if advancedScheduleFingerprintJSONError(io.EOF) || advancedScheduleFingerprintJSONError(io.ErrUnexpectedEOF) {
		t.Fatal("backing IO failure was classified as a literal JSON marshal failure")
	}
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	fixture := scaleP09Database(t)
	bounded, err := fixture.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer bounded.releaseMetadataLogicalSources()
	original := domain.Bootstrap{Workspace: fixture.data.Workspace, Viewer: fixture.data.Viewer,
		Projects: []domain.Project{{ID: "sw209", Description: strings.Repeat("x", 5<<20)}}}
	source, err := captureMetadataLogicalSource(t.Context(), original, copyStamp{workspaceID: original.Workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	ctx := ContextWithAdvancedScheduleMaintenance(t.Context(), time.Now().UTC(), 30*24*time.Hour)
	owner := &ResourceRead{store: bounded}
	defer owner.Close()
	profile := advancedScheduleImageProfile{}
	data, err := profile.inputs(ctx, owner, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.reserveCallback(ctx, owner, data, nil); err != nil {
		t.Fatal(err)
	}
	if token := AdvancedScheduleFingerprint(ctx, data); len(token) == 0 {
		t.Fatal("initial complete fingerprint failed", advancedScheduleMaintenance(ctx).err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if token := AdvancedScheduleFingerprint(cancelled, data); token != nil ||
		!errors.Is(advancedScheduleMaintenance(ctx).err, context.Canceled) {
		t.Fatal("cancelled fingerprint became a literal no-op", token, advancedScheduleMaintenance(ctx).err)
	}
	owner.Close()
	if bounded.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("cancelled fingerprint retained work", bounded.MetadataWorkUsedForTesting())
	}
}

func TestScaleP21SW209CallbackRecipientAdmission(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	fixture := scaleP09Database(t)
	bounded, err := fixture.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer bounded.releaseMetadataLogicalSources()
	owner := &ResourceRead{store: bounded}
	defer owner.Close()
	data := domain.Bootstrap{
		Viewer:   domain.User{ID: "viewer"},
		Projects: []domain.Project{{ID: "project", MemberIDs: []string{"member"}}},
	}
	base, err := advancedScheduleCallbackWork(t.Context(), owner, data)
	if err != nil || base == 0 {
		t.Fatal("missing project candidate allowance", base, err)
	}
	unrelated := cloneBootstrap(data)
	unrelated.Users = make([]domain.User, 2000)
	for index := range unrelated.Users {
		unrelated.Users[index] = domain.User{ID: fmt.Sprintf("unrelated-%d", index), Name: "Unrelated user"}
	}
	if got, err := advancedScheduleCallbackWork(t.Context(), owner, unrelated); err != nil || got != base {
		t.Fatal("unrelated users multiplied reminder output admission", base, got, err)
	}
	fallback := cloneBootstrap(data)
	fallback.Projects[0].MemberIDs = nil
	if got, err := advancedScheduleCallbackWork(t.Context(), owner, fallback); err != nil || got != base {
		t.Fatal("empty project audience did not admit fallback viewer", base, got, err)
	}
	duplicates := cloneBootstrap(data)
	duplicates.Projects[0].MemberIDs = []string{"member", "member", "member"}
	duplicates.Projects[0].Lead = &domain.User{ID: "leader"}
	floor, err := advancedScheduleCallbackWork(t.Context(), owner, domain.Bootstrap{})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := advancedScheduleCallbackWork(t.Context(), owner, duplicates); err != nil || got-floor < 4*(base-floor) {
		t.Fatal("literal member occurrences and lead lack output admission", base, got, err)
	}
	sla := domain.Bootstrap{
		SLARules: []domain.SLARule{{ID: "same"}, {ID: "same"}},
		Issues:   []domain.Issue{{ID: "issue"}},
	}
	single, err := advancedScheduleCallbackWork(t.Context(), owner, sla)
	if err != nil {
		t.Fatal(err)
	}
	sla.Issues[0].SubscriberIDs = []string{"member", "member", "leader"}
	withSubscribers, err := advancedScheduleCallbackWork(t.Context(), owner, sla)
	if err != nil || withSubscribers <= single {
		t.Fatal("SLA subscriber fanout has no separately owned output allowance", single, withSubscribers, err)
	}
	sla.Settings = map[string]any{"sla": map[string]any{"enabled": false}}
	disabled, err := advancedScheduleCallbackWork(t.Context(), owner, sla)
	if err != nil || disabled >= single {
		t.Fatal("disabled SLA callback retained rule/subscriber fanout allowance", single, disabled, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := advancedScheduleCallbackWork(cancelled, owner, data); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled callback output admission changed error", err)
	}
	if bounded.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("callback candidate counting retained temporary admission")
	}
}

func TestScaleP21SW209CallbackRuleMultiplicityAdmission(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	fixture := scaleP09Database(t)
	bounded, err := fixture.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer bounded.releaseMetadataLogicalSources()
	owner := &ResourceRead{store: bounded}
	defer owner.Close()
	data := domain.Bootstrap{
		SLARules: []domain.SLARule{{ID: "first"}},
		Issues:   []domain.Issue{{ID: "issue", SubscriberIDs: []string{"member", "member"}}},
	}
	single, err := advancedScheduleCallbackWork(t.Context(), owner, data)
	if err != nil {
		t.Fatal(err)
	}
	many := cloneBootstrap(data)
	many.SLARules = make([]domain.SLARule, 2000)
	for index := range many.SLARules {
		many.SLARules[index].ID = fmt.Sprintf("rule-%d", index)
	}
	if got, err := advancedScheduleCallbackWork(t.Context(), owner, many); err != nil || got != single {
		t.Fatal("unique nonwinning rule IDs multiplied callback output admission", single, got, err)
	}
	for range 3 {
		many.SLARules = append(many.SLARules, domain.SLARule{ID: "rule-0"})
	}
	floor, err := advancedScheduleCallbackWork(t.Context(), owner, domain.Bootstrap{})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := advancedScheduleCallbackWork(t.Context(), owner, many); err != nil || got-floor != 4*(single-floor) {
		t.Fatal("duplicate winning rule occurrences lost their independent output bound", single, got, err)
	}
	limit := bounded.metadataResidency.work.limit
	bounded.metadataResidency.work.limit = 64 << 10
	_, err = advancedScheduleCallbackWork(t.Context(), owner, many)
	bounded.metadataResidency.work.limit = limit
	if !errors.Is(err, ErrMetadataWorkBudget) || bounded.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("rule count scratch bypassed admission or leaked after refusal", err)
	}
	if _, err := advancedScheduleCallbackWork(t.Context(), owner, many); err != nil || bounded.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("rule count scratch did not recover and release", err)
	}
}
