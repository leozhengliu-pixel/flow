package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
)

func scaleP21SW139RawStrings(width int) func(context.Context, string, func([]byte) error) error {
	return func(ctx context.Context, value string, visit func([]byte) error) error {
		var buffer [1024]byte
		if err := visit(nil); err != nil {
			return err
		}
		for offset := 0; offset < len(value); {
			if err := ctx.Err(); err != nil {
				return err
			}
			size := copy(buffer[:min(width, len(buffer))], value[offset:])
			if err := visit(buffer[:size]); err != nil {
				return err
			}
			for index := range buffer {
				buffer[index] = 0xff
			}
			offset += size
		}
		return visit([]byte{})
	}
}

func scaleP21SW139Loop(variant, contractKind, suggestionKind int) domain.Loop {
	texts := []string{
		"", " \t\r\n\u0085\u00a0\u3000", "plain",
		" # Heading:\n0123. **Do work**. Next sentence.",
		"# One:\n## Two:", "#\n##\n> -- 0123.",
		"Heading:\n  \u00a0# Keep hash\nlast",
		"foo.\u00a0bar", "foo.\x85bar", "foo.\xa0bar",
		"foo.\xffbar", "foo.\x00bar", "foo!\r\nnext",
		"\U0001f680\U0001f680text!\u2000more",
		"a**b***c****d", " \xff\xc0\xaf\xe2\x82 ",
		strings.Repeat("界", 140), strings.Repeat("界", 141),
		strings.Repeat("x", 137) + "\xff\xfe\xfdxy",
		strings.Repeat("x", 140) + "\xff",
		"# Heading:\n" + strings.Repeat(" \t", 80) + "**Selected text**",
	}
	at := time.Date(2026, 11, 15, 7, 8, 9, 123, time.UTC)
	loop := domain.Loop{
		ID: fmt.Sprintf("sw139-loop-%d", variant%3), SlugID: "stored-stale-slug",
		Name:             []string{"", " \u3000 Loop name \t", "Name\x00\xff", "same name"}[variant%4],
		Instructions:     texts[variant%len(texts)],
		InstructionsData: map[string]any{"native": int64(9007199254740993), "time": at, "empty": []string{}, "nil": []string(nil)},
		Status:           []string{"", "published", "draft", "Published", "archived"}[variant%5],
		Version:          []int{-1, 0, 2}[variant%3], VersionID: "stored-version-id",
		CodeAccess:  []string{"", "disabled", "readWrite", "unrecognized"}[(variant/35)%4],
		Description: []string{"", " ", "Kept description", "\x00\xff"}[(variant/7)%4],
		TriggerType: []string{"schedule", "issue", "cycle", "project", "initiative", "release", "unknown"}[(variant/5)%7],
		OwnerID:     "viewer", Creator: domain.User{ID: "viewer", Name: "Viewer"},
		Level: "team", TeamID: "team", TeamAccess: "selected", Enabled: variant%2 == 0,
		AllowChangesOutsideTrigger: true, AllowExternalSync: true, WebSearch: true,
		TemplateID: "template", SourcePrompt: "source prompt", Icon: "icon", Color: "color",
		EditPolicy: "owner", TrustedSourceKeys: []string{"one", "one", ""},
		RunCount30d: 123, LastRunAt: &at, NextRunAt: &at, PublishedAt: &at, CreatedAt: at, UpdatedAt: at,
		ExpectedOutputs: []string{"document", "none", "document"},
	}
	switch variant % 6 {
	case 0:
		loop.TriggerConfig = nil
	case 1:
		loop.TriggerConfig = map[string]any{}
	case 2:
		loop.TriggerConfig = map[string]any{"starting": "2026-11-15", "interval": nil, "action": "created", "timezone": "UTC", "unknown": int64(9007199254740993)}
	case 3:
		loop.TriggerConfig = map[string]any{"action": "created or updated", "filter": false, "filterField": "", "filterOperator": "isNot", "filterValue": " \u3000open "}
	case 4:
		loop.TriggerConfig = map[string]any{"event": 42, "action": "created", "filter": nil, "filterField": "label", "filterValue": []string{"one", "two"}, "filters": []any{map[string]any{"field": "priority", "value": "high"}}, "teamIds": []any{"team", ""}}
	case 5:
		loop.TriggerConfig = map[string]any{"event": "custom", "filter": true, "filterValue": nil, "filters": []any{}, "startDate": false, "weekdays": []any{"sun"}, "time": "invalid"}
	}
	if variant%3 == 1 {
		loop.ConnectorIDs = []string{}
	} else if variant%3 == 2 {
		loop.ConnectorIDs = []string{"one", "", "one"}
	}
	hash := loopContractHashOf(loop)
	if contractKind != 0 {
		loop.OutputContract = &domain.LoopOutputContract{
			Source:           []string{loopContractModel, loopContractPerson, loopContractFallback, "unknown"}[variant%4],
			InstructionsHash: []string{"", hash, "older", " " + hash + " "}[contractKind-1],
			ReadOnly:         variant%2 == 0, ReadOnlyEvidence: "native evidence\x00\xff", Stale: variant%2 == 0,
			Model: "model", ExtractedAt: &at, Error: "kept error", UpdatedBy: "viewer",
			FallbackReason: &domain.LoopContractFallbackReason{Class: "provider_error", Explanation: "Stored explanation"},
			SkipConditions: []domain.LoopContractCondition{},
		}
		if variant%3 == 1 {
			loop.OutputContract.Outputs = []domain.LoopContractOutput{}
		} else if variant%3 == 2 {
			loop.OutputContract.Outputs = []domain.LoopContractOutput{{
				Kind: "document", Operation: loopOpAppend, Required: true,
				Target:     domain.LoopContractTarget{Mode: "existing", DocumentID: "document", Ambiguous: true, TeamID: "team", Title: "Stored target"},
				Conditions: []string{"one", "one", ""}, Evidence: "Stored evidence", Reasoning: "Stored reasoning",
			}}
		}
	}
	if suggestionKind != 0 {
		loop.SuggestedOutputContract = &domain.LoopOutputContract{
			Source: loopContractModel, InstructionsHash: []string{hash, "older", ""}[suggestionKind-1],
			Stale: true, Outputs: []domain.LoopContractOutput{}, Error: "Stored suggestion",
		}
	}
	return loop
}

func scaleP21SW139Equal(t *testing.T, expected, actual any) {
	t.Helper()
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("literal function values differ\nexpected=%#v\nactual=%#v", expected, actual)
	}
	want, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, got) {
		t.Fatal("literal function JSON differs", string(want), string(got))
	}
}

func TestScaleP21SW139PresentLoopDifferential(t *testing.T) {
	pairs := 0
	for variant := range 140 {
		for contractKind := range 5 {
			for suggestionKind := range 4 {
				for _, width := range []int{1, 7, 1024} {
					input := scaleP21SW139Loop(variant, contractKind, suggestionKind)
					var counts map[string]int
					if variant%2 != 0 {
						counts = map[string]int{input.ID: -7, "other": 31}
					}
					hash, description, err := scaleP21LoopText(t.Context(), input.Name, func(visit func([]byte) error) error {
						return scaleP21SW139RawStrings(width)(t.Context(), input.Instructions, visit)
					})
					if err != nil {
						t.Fatal(variant, contractKind, suggestionKind, width, err)
					}
					expected := presentLoop(counts, scaleP21SW139Loop(variant, contractKind, suggestionKind))
					actual := scaleP21PresentLoop(counts, input, hash, description)
					scaleP21SW139Equal(t, expected, actual)
					if actual.Instructions != input.Instructions || input.OutputContract != nil && actual.OutputContract == input.OutputContract {
						t.Fatal("presenter changed instruction bytes or reused the mutable contract shell")
					}
					pairs++
				}
			}
		}
	}
	t.Logf("exact_present_loop_pairs=%d", pairs)
}

func scaleP21SW139Bootstrap(role, shape string) domain.Bootstrap {
	at := time.Date(2026, 11, 15, 7, 8, 9, 123, time.UTC)
	viewer := domain.User{ID: "viewer", Name: "Viewer", Email: "viewer@example.test", Active: true}
	other := domain.User{ID: "other", Name: "Other", Email: "other@example.test", Active: true}
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "workspace", URLKey: "workspace"}, Viewer: viewer, ViewerRole: role,
		Users: []domain.User{viewer, other}, Members: []domain.WorkspaceMember{{User: viewer, Role: "owner"}, {User: other, Role: "member"}},
		DepartedMembers: []domain.WorkspaceMember{{User: domain.User{ID: "departed", Name: "Departed"}}, {User: domain.User{ID: "app_0123456789abcdef0123456789abcdef", Name: "Former app"}}},
		Teams:           []domain.Team{{ID: "team", Key: "TEAM"}, {ID: "retired", Key: "OLD", ArchivedAt: &at}},
		TeamMembers:     []domain.TeamMember{{TeamID: "team", UserID: viewer.ID}, {TeamID: "retired", UserID: other.ID}},
		TeamSettings:    map[string]domain.TeamSettings{"team": {}, "retired": {}},
		CycleSettings:   map[string]domain.CycleSettings{"team": {}, "retired": {}},
		UserSettings:    map[string]domain.UserSettings{"viewer": {UserID: "viewer", JobTitle: "Stored viewer title", Username: "view"}, "other": {UserID: "other", JobTitle: "Stored other title"}},
		Settings: map[string]any{
			"providerJobs": []any{"private"}, "applicationPolicies": map[string]any{"private": true}, "pulseDeliveryCursors": "private",
			"calendarAvailability": map[string]any{"viewer": "2100-01-02T03:04:05Z", "other": "invalid"},
			dashboardsSettingsKey:  []any{"private"}, postsSettingsKey: []any{"private"}, feedSettingsKey: []any{"private"}, meetingsSettingsKey: []any{"private"},
			"kept": map[string]any{"integer": int64(9007199254740993), "time": at, "nil": []string(nil), "empty": []string{}},
		},
		ProviderJobs: []domain.ProviderJob{{ID: "private"}},
		LoopRuns:     []domain.LoopRun{{ID: "private"}}, LoopVersions: []domain.LoopVersion{{ID: "private"}}, LoopAttachments: []domain.LoopAttachment{{ID: "private"}},
		Invitations:                   []domain.Invitation{{ID: "invitation", Token: "private"}},
		DocumentContentDrafts:         []domain.DocumentContentDraft{{ID: "own", UserID: viewer.ID}, {ID: "foreign", UserID: other.ID}},
		AgentSessions:                 []domain.AgentSession{{ID: "own", UserID: viewer.ID, Messages: []domain.AgentMessage{}}, {ID: "foreign", UserID: other.ID}, {ID: "deleted", UserID: viewer.ID, DeletedAt: &at}},
		AgentSkills:                   []domain.PersonalAgentSkill{{ID: "own", UserID: viewer.ID}, {ID: "foreign", UserID: other.ID}},
		Favorites:                     []domain.Favorite{{ID: "own", UserID: viewer.ID}, {ID: "foreign", UserID: other.ID}},
		FavoriteFolders:               []domain.FavoriteFolder{{ID: "own", UserID: viewer.ID}, {ID: "foreign", UserID: other.ID}},
		Passkeys:                      []domain.Passkey{{ID: "own", UserID: viewer.ID, CredentialJSON: "private"}, {ID: "foreign", UserID: other.ID}},
		PasskeyRegistrationChallenges: []domain.PasskeyRegistrationChallenge{{ID: "private"}},
		OAuthAuthorizations:           []domain.OAuthAuthorization{{ID: "own", UserID: viewer.ID}, {ID: "foreign", UserID: other.ID}},
		Cycles:                        []domain.Cycle{{ID: "cycle", CalendarToken: "private"}},
		APIKeys:                       []domain.APIKey{{ID: "own", CreatorID: viewer.ID, SecretHash: "private", ClientSecretHash: "private", Scopes: []string{"read", "issues:read"}}, {ID: "foreign", CreatorID: other.ID}},
		OAuthApplications:             []domain.OAuthApplication{{ID: "application", ClientSecret: "private"}},
		Webhooks:                      []domain.Webhook{{ID: "own", CreatorID: viewer.ID}, {ID: "foreign", CreatorID: other.ID}, {ID: "audit", CreatorID: viewer.ID, AuditLog: true}},
		WebhookFailureEvents:          []domain.WebhookFailureEvent{{ID: "private"}},
		IntegrationDeliveries:         []domain.IntegrationDelivery{{ID: "delivery", Payload: json.RawMessage(`{"private":true}`)}},
	}
	switch shape {
	case "empty":
		data.Loops = []domain.Loop{}
	case "duplicates":
		for index := range 12 {
			loop := scaleP21SW139Loop(index, index%5, index%4)
			loop.ID = "duplicate-loop"
			data.Loops = append(data.Loops, loop)
		}
	}
	return data
}

func TestScaleP21SW139SanitizeBootstrapDifferential(t *testing.T) {
	pairs := 0
	for _, role := range []string{"owner", "ADMIN", "member", "guest", "unknown"} {
		for _, shape := range []string{"nil", "empty", "duplicates"} {
			for _, width := range []int{1, 7, 1024} {
				expected, actual := scaleP21SW139Bootstrap(role, shape), scaleP21SW139Bootstrap(role, shape)
				sanitizeBootstrap(&expected)
				calls := 0
				var markers []string
				for _, loop := range actual.Loops {
					markers = append(markers, loop.Instructions)
				}
				err := scaleP21SanitizeBootstrap(t.Context(), &actual, func(ctx context.Context, marker string, visit func([]byte) error) error {
					if calls >= len(markers) || marker != markers[calls] {
						t.Fatal("duplicate loop text resolved by identity instead of source occurrence")
					}
					calls++
					return scaleP21SW139RawStrings(width)(ctx, marker, visit)
				})
				if err != nil || calls != len(markers) {
					t.Fatal(role, shape, width, calls, err)
				}
				scaleP21SW139Equal(t, expected, actual)
				pairs++
			}
		}
	}
	t.Logf("exact_whole_sanitizer_pairs=%d", pairs)
}

func TestScaleP21SW139LoopInstructionTokens(t *testing.T) {
	const marker = "\x00sw139:instruction-token\xff"
	const raw = " # Heading:\n**Do the actual work**. Another sentence."
	loop := domain.Loop{ID: "token", Name: "Token loop", Instructions: marker, OutputContract: &domain.LoopOutputContract{InstructionsHash: "older"}, SuggestedOutputContract: &domain.LoopOutputContract{InstructionsHash: loopContractHash("Token loop", raw)}}
	data := domain.Bootstrap{Loops: []domain.Loop{loop}}
	calls := 0
	err := scaleP21SanitizeBootstrap(t.Context(), &data, func(ctx context.Context, value string, visit func([]byte) error) error {
		if value != marker {
			t.Fatal("instruction marker changed before reading")
		}
		calls++
		return scaleP21SW139RawStrings(1)(ctx, raw, visit)
	})
	if err != nil || calls != 1 || len(data.Loops) != 1 {
		t.Fatal(calls, err)
	}
	if data.Loops[0].Instructions != marker || data.Loops[0].Description != fallbackLoopDescription(raw) ||
		!data.Loops[0].OutputContract.Stale || data.Loops[0].SuggestedOutputContract == nil {
		t.Fatal("token text was materialized or used for presentation", data.Loops[0])
	}
}

func TestScaleP21SW139BootstrapLoopFailures(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	data := scaleP21SW139Bootstrap("owner", "duplicates")
	err := scaleP21SanitizeBootstrap(canceled, &data, func(context.Context, string, func([]byte) error) error {
		t.Fatal("initially canceled sanitizer read instructions")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	scaleP21SW139Equal(t, scaleP21SW139Bootstrap("owner", "duplicates"), data)
	if err := scaleP21SanitizeBootstrap(t.Context(), &data, nil); err == nil {
		t.Fatal("nonempty loop input accepted a nil reader")
	}
	scaleP21SW139Equal(t, scaleP21SW139Bootstrap("owner", "duplicates"), data)
	for _, shape := range []string{"nil", "empty"} {
		expected, actual := scaleP21SW139Bootstrap("owner", shape), scaleP21SW139Bootstrap("owner", shape)
		sanitizeBootstrap(&expected)
		if err := scaleP21SanitizeBootstrap(t.Context(), &actual, nil); err != nil {
			t.Fatal(err)
		}
		scaleP21SW139Equal(t, expected, actual)
	}
	failure := errors.New("loop instruction source failed")
	for _, stop := range []int{1, 2} {
		data := scaleP21SW139Bootstrap("member", "duplicates")
		originalLoops := data.Loops
		calls := 0
		err := scaleP21SanitizeBootstrap(t.Context(), &data, func(ctx context.Context, marker string, visit func([]byte) error) error {
			calls++
			if calls == stop {
				if err := visit([]byte("partial")); err != nil {
					return err
				}
				return failure
			}
			return scaleP21SW139RawStrings(7)(ctx, marker, visit)
		})
		if !errors.Is(err, failure) || calls != stop || &data.Loops[0] != &originalLoops[0] || !reflect.DeepEqual(data.Loops, originalLoops) {
			t.Fatal("reader failure exposed a partially presented prefix", stop, calls, err)
		}
	}
	active, cancel := context.WithCancel(t.Context())
	defer cancel()
	data = scaleP21SW139Bootstrap("member", "duplicates")
	originalLoops := data.Loops
	err = scaleP21SanitizeBootstrap(active, &data, func(_ context.Context, _ string, visit func([]byte) error) error {
		_ = visit([]byte("partial"))
		cancel()
		_ = visit([]byte("producer ignores callback cancellation"))
		return nil
	})
	if !errors.Is(err, context.Canceled) || &data.Loops[0] != &originalLoops[0] || !reflect.DeepEqual(data.Loops, originalLoops) {
		t.Fatal("canceled producer exposed partial loop output", err)
	}
}

func TestScaleP21SW139BootstrapLoopTextScratchBound(t *testing.T) {
	block := []byte(strings.Repeat("Alpha ", 128))
	measure := func(repeats int) uint64 {
		data := domain.Bootstrap{Loops: []domain.Loop{{ID: "bounded", Name: "Bounded text", Instructions: "marker"}}}
		reader := func(ctx context.Context, marker string, visit func([]byte) error) error {
			if marker != "marker" {
				t.Fatal("body marker was replaced")
			}
			for range repeats {
				if err := visit(block); err != nil {
					return err
				}
			}
			return ctx.Err()
		}
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		err := scaleP21SanitizeBootstrap(t.Context(), &data, reader)
		runtime.ReadMemStats(&after)
		if err != nil || data.Loops[0].Instructions != "marker" || data.Loops[0].Description == "" || utf8.RuneCountInString(data.Loops[0].Description) > 140 {
			t.Fatal("bounded stream failed", err)
		}
		return after.TotalAlloc - before.TotalAlloc
	}
	small, large := measure(4), measure(16384)
	if large > 64<<10 || large > small+16<<10 {
		t.Fatal("loop instruction size changed sanitizer scratch", small, large)
	}
	t.Logf("3KiB text allocations=%d; 12MiB text allocations=%d; required loop output count=1", small, large)
}
