package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW202OrdinalKernelValidatesBeforeWork(t *testing.T) {
	repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
	owner := &ResourceRead{store: repo}
	defer owner.Close()
	ctx := owner.metadataOutputContext(t.Context())
	var factoryCalls, callbackCalls int
	factory := func(context.Context, *metadataLogicalSource) (metadataOrdinalProfile, error) {
		factoryCalls++
		return nil, errors.New("must not start factory")
	}
	callback := func(*domain.Bootstrap) error { callbackCalls++; return nil }
	for _, scenario := range []struct {
		name, event string
		ctx         context.Context
	}{
		{"content-event", "issue.updated", ctx},
		{"field-scope", "integration_delivery.claimed", WithMetadataFields(ctx, "integrationDeliveries")},
		{"record-scope", "integration_delivery.claimed", WithMetadataRecords(ctx, MetadataRecordScope{})},
		{"missing-output", "integration_delivery.claimed", t.Context()},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			err := repo.mutateMetadataOrdinalProfile(scenario.ctx, "missing", scenario.event, "delivery", nil, factory, callback)
			if !errors.Is(err, ErrBoundedMetadataCoverage) || factoryCalls != 0 || callbackCalls != 0 ||
				repo.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatalf("invalid profile started work: err=%v factory=%d callback=%d", err, factoryCalls, callbackCalls)
			}
		})
	}
}

func TestScaleP21SW202OrdinalKernelCapturesErrorsAndRestoresServices(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, mode := range []string{"success", "callback-error", "actor-error", "cancel", "merge-error", "output-error"} {
		t.Run(mode, func(t *testing.T) {
			f := scaleP09Database(t)
			workspace := f.data.Workspace.URLKey
			id, connection := "sw202-kernel-"+mode, "sw202-kernel-connection-"+mode
			at := time.Date(2026, 11, 16, 3, 4, 5, 6, time.UTC)
			if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw202_kernel_seed", "", nil, func(data *domain.Bootstrap) error {
				*data = scaleP21SW202Data(*data, id, connection, at)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			repo := scaleP21IssuePersistenceView(t, f, true, false)
			before := scaleP21SW193Field(t, repo, workspace, "integrationDeliveries")
			prior := scaleP21SW63EventIDs(t, repo, workspace)
			sentinel := errors.New("sw202 ordinal callback/merge error")
			var profile *scaleP21SW202CheckedOrdinalProfile
			var outputCloses []func()
			work, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx := WithMetadataOutputOwner(work, func(close func()) bool {
				if mode == "output-error" {
					return false
				}
				outputCloses = append(outputCloses, close)
				return true
			})
			actor := f.data.Viewer
			actor.Via = &domain.ActorBot{ID: "sw202-kernel-proxy", Name: "Callback attribution", Type: domain.ActorBotTypeOAuthApplication}
			if mode == "actor-error" {
				actor.ID = "usr_sw202_absent_" + f.data.Workspace.ID
				actor.Name = "Absent callback actor"
			}
			ctx = ContextWithActor(ctx, actor)
			callbacks, publications := 0, 0
			repo.SetRealtimeSink(func(string, domain.RealtimeEvent) {
				publications++
				if profile == nil || profile.closed.Load() != 0 || len(profile.Input().IntegrationDeliveries) != 1 {
					t.Error("callback profile closed before the publication sink returned")
				}
			})
			factory := func(ctx context.Context, source *metadataLogicalSource) (metadataOrdinalProfile, error) {
				value, err := repo.prepareIntegrationDeliveryProfile(ctx, source, id, "integration_delivery.claimed")
				if err != nil {
					return nil, err
				}
				profile = &scaleP21SW202CheckedOrdinalProfile{metadataOrdinalProfile: value, nativeRole: value.Input().ViewerRole}
				if mode == "merge-error" {
					profile.mergeErr = sentinel
				}
				return profile, nil
			}
			err := repo.mutateMetadataOrdinalProfile(ctx, workspace, "integration_delivery.claimed", id, nil, factory, func(data *domain.Bootstrap) error {
				callbacks++
				if data.FormerMembers == nil || data.CollaborationUpdates == nil || data.Viewer.ID != actor.ID {
					t.Fatal("callback actor/services missing")
				}
				// The transient role/team/lookups must be restored before the
				// profile's closed Merge validation, including error returns.
				data.ViewerRole, data.ViewerTeamIDs = "callback transient role", []string{"callback transient team"}
				if mode == "callback-error" || mode == "actor-error" {
					return sentinel
				}
				if mode == "cancel" {
					cancel()
					return context.Canceled
				}
				return scaleP21SW202Mutation(id, "integration_delivery.claimed", at.Add(time.Hour))(data)
			})
			repo.SetRealtimeSink(nil)
			if mode == "success" {
				if err != nil || callbacks != 1 || publications != 1 || profile.merges.Load() != 1 {
					t.Fatalf("successful shared attempt changed: err=%v callbacks=%d pubs=%d merges=%d", err, callbacks, publications, profile.merges.Load())
				}
			} else {
				want := sentinel
				if mode == "cancel" {
					want = context.Canceled
				} else if mode == "output-error" {
					want = ErrBoundedMetadataCoverage
				}
				if !errors.Is(err, want) || publications != 0 {
					t.Fatalf("failure changed: err=%v want=%v pubs=%d", err, want, publications)
				}
				if mode == "output-error" {
					if callbacks != 0 || profile == nil || profile.closed.Load() != 1 || repo.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("rejected output registration leaked or invoked the callback")
					}
				} else if callbacks != 1 || profile == nil || profile.closed.Load() != 0 ||
					profile.Input().FormerMembers != nil || profile.Input().CollaborationUpdates != nil ||
					profile.Input().ViewerRole != profile.nativeRole || profile.Input().ViewerTeamIDs != nil ||
					repo.MetadataWorkUsedForTesting() == 0 {
					t.Fatal("callback/actor failure lost captured output ownership or transient restoration")
				}
				after := scaleP21SW193Field(t, repo, workspace, "integrationDeliveries")
				if !reflect.DeepEqual(before, after) || len(scaleP21SW63NewEvents(t, repo, workspace, prior)) != 0 {
					t.Fatal("failed attempt persisted its callback or event")
				}
			}
			for _, close := range outputCloses {
				close()
				close()
			}
			if profile == nil || profile.closed.Load() != 1 || repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("outer output release leaked the ordinal profile or full-hydrated metadata")
			}
		})
	}
}

func TestScaleP21SW202OrdinalKernelDisplayBeforeActorAndCompleteFactory(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, reload := range []bool{false, true} {
		t.Run(fmt.Sprintf("reload=%t", reload), func(t *testing.T) {
			f := scaleP09Database(t)
			workspace, id, connection := f.data.Workspace.URLKey, "sw202-kernel-display", "sw202-kernel-display-connection"
			at := time.Date(2026, 11, 16, 3, 4, 5, 6, time.UTC)
			actor := f.data.Viewer
			storedActor := actor
			storedActor.Name = "Stored directory name"
			if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw202_kernel_display_seed", "", nil, func(data *domain.Bootstrap) error {
				*data = scaleP21SW202Data(*data, id, connection, at)
				index := slices.IndexFunc(data.Users, func(user domain.User) bool { return user.ID == actor.ID })
				if index < 0 {
					data.Users = append(data.Users, storedActor)
				} else {
					data.Users[index] = storedActor
				}
				staleLead := storedActor
				staleLead.Name = "Stale embedded name"
				data.Projects = []domain.Project{{ID: "project_sw202_factory", Name: "Factory retains unrelated metadata", Lead: &staleLead}}
				data.Documents = []domain.Document{{ID: "document_sw202_factory", ContentData: map[string]any{"id": id, "marker": "earlier aggregate winner"}}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			repo := scaleP21IssuePersistenceView(t, f, true, reload)
			owner := &ResourceRead{store: repo}
			defer owner.Close()
			actor.Name = "Request actor name"
			ctx := ContextWithActor(owner.metadataOutputContext(t.Context()), actor)
			factoryCalls := 0
			factory := func(ctx context.Context, source *metadataLogicalSource) (metadataOrdinalProfile, error) {
				factoryCalls++
				complete, err := source.Select(ctx, MetadataRecordScope{Fields: []string{"users", "projects", "documents", "integrationConnections"}}, 8<<20)
				if err != nil {
					return nil, err
				}
				index := slices.IndexFunc(complete.Users, func(user domain.User) bool { return user.ID == actor.ID })
				if complete.Viewer.Name != actor.Name || index < 0 || complete.Users[index].Name != actor.Name ||
					len(complete.Projects) != 1 || complete.Projects[0].Lead == nil || complete.Projects[0].Lead.Name != storedActor.Name ||
					len(complete.Documents) != 1 || len(complete.IntegrationConnections) != 3 {
					return nil, errors.New("factory did not receive complete display-then-actor image")
				}
				return repo.prepareIntegrationDeliveryProfile(ctx, source, id, "integration_delivery.claimed")
			}
			var snapshot json.RawMessage
			repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { snapshot = append(json.RawMessage(nil), event.Payload...) })
			if err := repo.mutateMetadataOrdinalProfile(ctx, workspace, "integration_delivery.claimed", id, nil, factory,
				scaleP21SW202Mutation(id, "integration_delivery.claimed", at.Add(time.Hour))); err != nil {
				t.Fatal(err)
			}
			repo.SetRealtimeSink(nil)
			var output struct {
				Entity map[string]any `json:"entity"`
			}
			if err := json.Unmarshal(snapshot, &output); err != nil || output.Entity["marker"] != "earlier aggregate winner" || factoryCalls != 1 {
				t.Fatal("complete-image nested aggregate precedence changed", string(snapshot), err)
			}
			owner.Close()
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("complete factory profile leaked")
			}
		})
	}
}

func TestScaleP21SW202OrdinalKernelRetriesWithCapturedLargeOutput(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	id, connection := "sw202-kernel-retry-"+f.data.Workspace.ID, "sw202-kernel-retry-connection"
	at := time.Date(2026, 11, 16, 4, 5, 6, 7, time.UTC)
	payload, err := json.Marshal(map[string]string{"body": strings.Repeat("x", 1<<20)})
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		Inputs                     []string
		Captured                   []domain.IntegrationDelivery
		State                      []json.RawMessage
		Events, Webhooks, Realtime []any
	}
	var outcomes []any
	for _, bounded := range []bool{false, true} {
		f.SetWorkspaceCoordinator(nil)
		f.SetWebhookSink(nil)
		f.SetRealtimeSink(nil)
		if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := f.MutateWorkspace(t.Context(), workspace, "integration.adapter_configured", "", nil, func(data *domain.Bootstrap) error {
			*data = scaleP21SW202Data(*data, id, connection, at)
			data.IntegrationDeliveries = data.IntegrationDeliveries[:2]
			data.IntegrationDeliveries[1].Status = "delivering"
			data.IntegrationDeliveries[1].Payload = payload
			data.IntegrationConnections = data.IntegrationConnections[:2]
			data.IntegrationConnections[1].Config = map[string]string{"deliveryURL": "https://example.invalid/retry"}
			// This supported warm value differs from SQL's NUL-stripped
			// row, producing the same real scalar conflict as SW36.
			data.IntegrationConnections[1].LastError = "warm\x00before"
			data.Documents = []domain.Document{{ID: "document_sw202_retry", ContentData: map[string]any{"id": id, "marker": "earlier retry aggregate"}}}
			data.Webhooks = []domain.Webhook{{ID: "hook_sw202_retry", Enabled: true, URL: "https://example.invalid/hook"}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		warm, found := f.WorkspaceMetadata(workspace)
		if !found || warm.IntegrationConnections[1].LastError != "warm\x00before" {
			t.Fatal("seed did not retain the supported warm conflict")
		}
		known, exact := f.copyRevisionOf(workspace)
		canonical, err := readMetadataStamp(t.Context(), f.db, workspace)
		if err != nil || !exact || known.workspaceID != canonical.workspaceID || known.revision != canonical.revision {
			t.Fatal("warm seed did not retain an exact generation", known, exact, canonical, err)
		}
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='integrationConnections' AND record_key=?`, workspace, connection).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var stored domain.IntegrationConnection
		if err := json.Unmarshal(raw, &stored); err != nil || stored.LastError != "warmbefore" {
			t.Fatal("canonical row did not exercise the NUL boundary", stored.LastError, err)
		}
		repo := scaleP21IssuePersistenceView(t, f, bounded, false)
		t.Cleanup(func() {
			repo.SetWorkspaceCoordinator(nil)
			repo.SetWebhookSink(nil)
			repo.SetRealtimeSink(nil)
		})
		prior := scaleP21SW63EventIDs(t, repo, workspace)
		got := outcome{}
		var profiles []*scaleP21SW202CheckedOrdinalProfile
		var outputCloses []func()
		releaseConcurrent := func() {}
		defer func() { releaseConcurrent() }()
		t.Cleanup(func() {
			for _, close := range outputCloses {
				close()
			}
		})
		var diagnosticMu sync.Mutex
		phase, lastSQL, lastSQLPhase := "before-attempt", "", ""
		lastSQLWork, lastSQLLimit := 0, 0
		lastAdmission, lastAdmissionPhase, lastAdmissionWork := "", "", 0
		markPhase := func(value string) {
			diagnosticMu.Lock()
			phase = value
			diagnosticMu.Unlock()
		}
		ctx := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
			outputCloses = append(outputCloses, close)
			return true
		})
		ctx = WithSQLStatementObserver(ctx, func(query string) {
			used, limit := 0, 0
			if bounded {
				budget := repo.metadataResidency.work
				budget.mu.Lock()
				used, limit = budget.used, budget.limit
				budget.mu.Unlock()
			}
			diagnosticMu.Lock()
			lastSQL, lastSQLPhase = query, phase
			lastSQLWork, lastSQLLimit = used, limit
			diagnosticMu.Unlock()
		})
		if bounded {
			ctx = scaleP21HotWebhookContext{Context: ctx, check: func() {
				var callers [48]uintptr
				count := runtime.Callers(2, callers[:])
				admission := false
				for _, caller := range callers[:count] {
					if function := runtime.FuncForPC(caller); function != nil && strings.Contains(function.Name(), "tryAdmitAdditionalMetadataWork") {
						admission = true
						break
					}
				}
				if !admission {
					return
				}
				budget := repo.metadataResidency.work
				if !budget.mu.TryLock() {
					return
				}
				used := budget.used
				budget.mu.Unlock()
				var trace []string
				frames := runtime.CallersFrames(callers[:count])
				for {
					frame, more := frames.Next()
					trace = append(trace, fmt.Sprintf("%s:%d %s", frame.File, frame.Line, frame.Function))
					if !more {
						break
					}
				}
				diagnosticMu.Lock()
				lastAdmission, lastAdmissionPhase, lastAdmissionWork = strings.Join(trace, " <- "), phase, used
				diagnosticMu.Unlock()
			}}
		}
		locks, callbacks, webhooks, realtime := 0, 0, 0, 0
		repo.SetWorkspaceCoordinator(scaleP21ColdWriteCoordinator(func(_ context.Context, key string, apply func() error) error {
			locks++
			markPhase(fmt.Sprintf("coordinator-%d-before-apply", locks))
			if key != workspace {
				t.Fatal("retry changed workspace")
			}
			if locks == 2 {
				if len(got.Captured) != 1 || !bytes.Equal(got.Captured[0].Payload, payload) {
					t.Fatal("failed callback lost its captured large output")
				}
				if bounded {
					if len(profiles) != 1 || profiles[0].closed.Load() != 0 ||
						!bytes.Equal(profiles[0].Input().IntegrationDeliveries[0].Payload, payload) {
						t.Fatal("failed attempt closed its output before coordinator reload")
					}
					if used := repo.MetadataWorkUsedForTesting(); used <= metadataWorkBytes-(metadataPolicyBytes+2<<20) {
						t.Fatal("retry does not distinguish actual reload work from the former maximum allowance", used)
					}
				}
			}
			err := apply()
			markPhase(fmt.Sprintf("coordinator-%d-after-apply", locks))
			return err
		}))
		repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
			webhooks++
			got.Webhooks = append(got.Webhooks, []any{event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues, event.Snapshot})
		})
		repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
			realtime++
			got.Realtime = append(got.Realtime, []any{event.Type, event.AggregateID, event.ActorID, event.Payload})
			if bounded {
				if len(profiles) != 2 || profiles[0].closed.Load() != 0 || profiles[1].closed.Load() != 0 {
					t.Error("retry output profiles closed before publication returned")
				}
			}
		})
		factory := func(ctx context.Context, source *metadataLogicalSource) (metadataOrdinalProfile, error) {
			markPhase(fmt.Sprintf("factory-%d", len(profiles)+1))
			value, err := repo.prepareIntegrationDeliveryProfile(ctx, source, id, "integration_delivery.retry")
			if err != nil {
				return nil, err
			}
			profile := &scaleP21SW202CheckedOrdinalProfile{metadataOrdinalProfile: value, nativeRole: value.Input().ViewerRole}
			profiles = append(profiles, profile)
			markPhase(fmt.Sprintf("factory-%d-ready", len(profiles)))
			return profile, nil
		}
		mutate := scaleP21SW202Mutation(id, "integration_delivery.retry", at.Add(time.Hour))
		callback := func(data *domain.Bootstrap) error {
			callbacks++
			markPhase(fmt.Sprintf("callback-%d", callbacks))
			index := slices.IndexFunc(data.IntegrationConnections, func(value domain.IntegrationConnection) bool { return value.ID == connection })
			if index < 0 {
				t.Fatal("retry connection missing")
			}
			got.Inputs = append(got.Inputs, data.IntegrationConnections[index].LastError)
			if err := mutate(data); err != nil {
				return err
			}
			index = slices.IndexFunc(data.IntegrationDeliveries, func(value domain.IntegrationDelivery) bool { return value.ID == id })
			if index < 0 {
				t.Fatal("retry delivery missing")
			}
			got.Captured = append(got.Captured, data.IntegrationDeliveries[index])
			if bounded && callbacks == 1 {
				// Model concurrent work arriving after the first reload.
				// Keep it live through conflict refresh, retry and sinks.
				_, release, err := repo.tryAdmitAdditionalMetadataWork(t.Context(), 64<<20)
				if err != nil {
					return err
				}
				releaseConcurrent = release
			}
			markPhase(fmt.Sprintf("callback-%d-complete", callbacks))
			return nil
		}
		if bounded {
			err = repo.mutateMetadataOrdinalProfile(withIntegrationDeliveryStream(ctx, "integration_delivery.retry"), workspace, "integration_delivery.retry", id, nil, factory, callback)
		} else {
			err = repo.MutateWorkspace(ctx, workspace, "integration_delivery.retry", id, nil, callback)
		}
		repo.SetWorkspaceCoordinator(nil)
		repo.SetWebhookSink(nil)
		repo.SetRealtimeSink(nil)
		if err != nil || locks != 2 || callbacks != 2 || webhooks != 1 || realtime != 1 {
			if bounded {
				budget := repo.metadataResidency.work
				budget.mu.Lock()
				used, limit := budget.used, budget.limit
				budget.mu.Unlock()
				repo.metadataResidency.mu.Lock()
				hasLogical := repo.metadataResidency.logical[workspace] != nil
				repo.metadataResidency.mu.Unlock()
				diagnosticMu.Lock()
				t.Logf("admission diagnostic phase=%s currentWork=%d limit=%d hasLogical=%t lastSQLPhase=%s lastSQLWork=%d lastSQLLimit=%d lastSQL=%q", phase, used, limit, hasLogical, lastSQLPhase, lastSQLWork, lastSQLLimit, lastSQL)
				t.Logf("last independent admission phase=%s work=%d stack=%s", lastAdmissionPhase, lastAdmissionWork, lastAdmission)
				diagnosticMu.Unlock()
			}
			t.Fatalf("required retry/publication success changed: bounded=%t err=%v locks=%d callbacks=%d hooks=%d realtime=%d", bounded, err, locks, callbacks, webhooks, realtime)
		}
		if !reflect.DeepEqual(got.Inputs, []string{"warm\x00before", "warmbefore"}) {
			t.Fatal("real conflict did not refresh canonical inputs", got.Inputs)
		}
		got.State = []json.RawMessage{
			scaleP21SW193Field(t, repo, workspace, "integrationDeliveries"),
			scaleP21SW193Field(t, repo, workspace, "integrationConnections"),
		}
		got.Events = scaleP21SW63NewEvents(t, repo, workspace, prior)
		if len(got.Events) != 1 || len(got.Captured) != 2 ||
			!bytes.Equal(got.Captured[0].Payload, payload) || !bytes.Equal(got.Captured[1].Payload, payload) {
			t.Fatal("failed attempt committed an event or lost native output")
		}
		outcomes = append(outcomes, scaleP21SW63JSON(t, got))
		for _, close := range outputCloses {
			close()
			close()
		}
		if bounded {
			if len(profiles) != 2 || profiles[0].closed.Load() != 1 || profiles[1].closed.Load() != 1 ||
				repo.MetadataWorkUsedForTesting() != 64<<20 || repo.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("output release retained profiles, borrowed concurrent work or full-decoded metadata")
			}
			releaseConcurrent()
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("retry retained work after all owners closed")
			}
		}
	}
	if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
		t.Fatal("literal/bounded retry changed callbacks, SQL, events or synchronous sinks")
	}
}

type scaleP21SW202CheckedOrdinalProfile struct {
	metadataOrdinalProfile
	nativeRole string
	mergeErr   error
	merges     atomic.Int32
	closed     atomic.Int32
	once       sync.Once
}

func (p *scaleP21SW202CheckedOrdinalProfile) Merge(ctx context.Context) (*metadataLogicalSource, error) {
	p.merges.Add(1)
	data := p.Input()
	if data.ViewerRole != p.nativeRole || data.ViewerTeamIDs != nil || data.FormerMembers != nil || data.CollaborationUpdates != nil {
		return nil, errors.New("ordinal profile received transient actor services")
	}
	if p.mergeErr != nil {
		return nil, p.mergeErr
	}
	return p.metadataOrdinalProfile.Merge(ctx)
}

func (p *scaleP21SW202CheckedOrdinalProfile) Close() {
	p.once.Do(func() {
		p.closed.Add(1)
		p.metadataOrdinalProfile.Close()
	})
}
