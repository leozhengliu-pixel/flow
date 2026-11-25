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
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW19ChatTeamPolicySnapshot(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams", "teamSettings", "workspaceSettings"), workspace, "team.updated", "", nil, func(data *domain.Bootstrap) error {
		for i := range 75 {
			id := fmt.Sprintf("sw19-header-%03d", i)
			data.Teams = append(data.Teams, domain.Team{ID: id, Name: strings.Repeat("unused name ", 100), Private: i%2 == 0, ArchivedAt: &stamp, RetiredAt: &stamp})
			data.TeamSettings[id] = domain.TeamSettings{TeamID: id, ParentTeamID: data.Teams[0].ID, Access: " private ", MembershipRestriction: " Owners ", Description: strings.Repeat("unused policy description ", 100)}
		}
		data.WorkspaceSettings.HIPAACompliance = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Large unused fields in the selected resident source must not be cloned
	// or charged as selected input. No canonical data is changed by the read.
	view.mu.Lock()
	source := view.workspaces[workspace]
	source.Teams[0].Name = strings.Repeat("unused large team name ", 20000)
	settings := source.TeamSettings[source.Teams[0].ID]
	settings.Description = strings.Repeat("unused large settings body ", 20000)
	source.TeamSettings[source.Teams[0].ID] = settings
	view.workspaces[workspace] = source
	view.mu.Unlock()
	want := domain.Bootstrap{Teams: make([]domain.Team, len(source.Teams)), TeamSettings: map[string]domain.TeamSettings{}}
	want.WorkspaceSettings.HIPAACompliance = true
	for index, team := range source.Teams {
		want.Teams[index] = domain.Team{ID: team.ID, Private: team.Private}
		if team.ArchivedAt != nil {
			want.Teams[index].ArchivedAt = new(time.Time)
		}
		if team.RetiredAt != nil {
			want.Teams[index].RetiredAt = new(time.Time)
		}
	}
	for id, item := range source.TeamSettings {
		want.TeamSettings[id] = domain.TeamSettings{ParentTeamID: item.ParentTeamID, Access: item.Access, MembershipRestriction: item.MembershipRestriction}
	}
	read, found, err := view.BeginChatTeamPolicyRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal("policy read", found, err)
	}
	if !reflect.DeepEqual(read.Metadata(), want) || view.metadataResidency.work.used == 0 {
		t.Fatal("policy fields/order/charge differ")
	}
	got := read.Metadata()
	got.Teams[1].ID = "caller-owned"
	*got.Teams[1].ArchivedAt = stamp.Add(time.Hour)
	delete(got.TeamSettings, source.Teams[0].ID)
	read.Close()
	read.Close()
	again, found, err := view.BeginChatTeamPolicyRead(t.Context(), "")
	if err != nil || !found || !reflect.DeepEqual(again.Metadata(), want) {
		t.Fatal("snapshot aliases source or default workspace changed", found, err)
	}
	again.Close()
	for _, missing := range []string{workspace + "-missing", strings.ToUpper(workspace), workspace + " ", workspace + "\x00"} {
		if read, found, err := view.BeginChatTeamPolicyRead(t.Context(), missing); read != nil || found || err != nil {
			t.Fatalf("nonexistent exact workspace=%q found=%v err=%v", missing, found, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if read, _, err := view.BeginChatTeamPolicyRead(ctx, workspace); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	budget := view.metadataResidency.work
	previous := budget.limit
	budget.limit = 1
	if read, found, err := view.BeginChatTeamPolicyRead(t.Context(), workspace); read != nil || !found || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("admission budget found=%v err=%v", found, err)
	}
	budget.limit = previous
	_, held, err := view.admitMetadataWork(t.Context(), previous)
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	if read, _, err := view.BeginChatTeamPolicyRead(short, workspace); read != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued cancellation=%v", err)
	}
	cancel()
	held()
	// Required policy strings are accounted before any snapshot allocation.
	view.mu.Lock()
	source = view.workspaces[workspace]
	settings = source.TeamSettings[source.Teams[0].ID]
	settings.Access = strings.Repeat("required policy ", (scaleP21SW19ChatPolicyMaxWork/16)+1)
	source.TeamSettings[source.Teams[0].ID] = settings
	view.workspaces[workspace] = source
	view.mu.Unlock()
	if read, found, err := view.BeginChatTeamPolicyRead(t.Context(), workspace); read != nil || !found || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("selected string budget found=%v err=%v", found, err)
	}
	view.mu.Lock()
	settings.Access = want.TeamSettings[source.Teams[0].ID].Access
	source.TeamSettings[source.Teams[0].ID] = settings
	view.workspaces[workspace] = source
	view.mu.Unlock()
	read, found, err = view.BeginChatTeamPolicyRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal("budget recovery", found, err)
	}
	read.Close()
	if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("policy read leaked work or decoded complete metadata")
	}
	if source.Teams[1].ArchivedAt == nil || *source.Teams[1].ArchivedAt != stamp {
		t.Fatal("header construction or caller mutation changed the source date")
	}
}

func TestScaleP21SW19ChatTeamPolicyAdmissionRecheck(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	workspace := f.data.Workspace.URLKey
	_, held, err := view.admitMetadataWork(t.Context(), view.metadataResidency.work.limit)
	if err != nil {
		t.Fatal(err)
	}
	defer held()
	type outcome struct {
		read  *ChatTeamPolicyRead
		found bool
		err   error
	}
	result := make(chan outcome, 1)
	go func() {
		read, found, err := view.BeginChatTeamPolicyRead(t.Context(), workspace)
		result <- outcome{read, found, err}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		view.metadataResidency.work.mu.Lock()
		waiting := view.metadataResidency.work.waiting
		view.metadataResidency.work.mu.Unlock()
		if waiting != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("policy read did not queue for admission")
		}
		runtime.Gosched()
	}
	view.mu.Lock()
	source := view.workspaces[workspace]
	source.Teams = append(slices.Clone(source.Teams), domain.Team{ID: "sw19-grew-while-queued", Private: true})
	view.workspaces[workspace] = source
	view.mu.Unlock()
	held()
	got := <-result
	if got.err != nil || !got.found || !slices.ContainsFunc(got.read.Metadata().Teams, func(team domain.Team) bool { return team.ID == "sw19-grew-while-queued" }) {
		t.Fatal("queued read did not snapshot the current source", got.found, got.err)
	}
	cost, err := scaleP21SW19ChatPolicyCost(t.Context(), source)
	if err != nil || view.metadataResidency.work.used != cost {
		t.Fatal("grown source used a smaller admission allowance", err)
	}
	got.read.Close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("policy admission recheck leaked work")
	}
}

func TestScaleP21SW19ChatTeamPolicyEncodedIDs(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	workspace := f.data.Workspace.URLKey
	empty := domain.Bootstrap{Teams: []domain.Team{{}}}
	base, err := scaleP21SW19ChatPolicyCost(t.Context(), empty)
	if err != nil {
		t.Fatal(err)
	}
	maximum := (scaleP21SW19ChatPolicyMaxWork - base) / 33
	for _, spec := range []struct{ name, fragment, escaped string }{
		{"html-quotes", "<&>\\\"", `\u003c`},
		{"control", "\x00\x01\x1f\n\t", `\u0000`},
		{"invalid-utf8", "\xff\xfe\x80", `\ufffd`},
	} {
		t.Run(spec.name, func(t *testing.T) {
			id := strings.Repeat(spec.fragment, maximum/len(spec.fragment))
			source := domain.Bootstrap{Teams: []domain.Team{{ID: id}}}
			view.mu.Lock()
			view.workspaces[workspace] = source
			view.mu.Unlock()
			read, found, err := view.BeginChatTeamPolicyRead(t.Context(), workspace)
			if err != nil || !found || read.Metadata().Teams[0].ID != id {
				t.Fatal("large raw ID was truncated or changed before encoding", found, err)
			}
			defer read.Close()
			cost, err := scaleP21SW19ChatPolicyCost(t.Context(), source)
			if err != nil || view.metadataResidency.work.used != cost {
				t.Fatal("encoded ID allowance not held", err)
			}
			encoded, err := json.Marshal([]string{read.Metadata().Teams[0].ID})
			if err != nil || !bytes.Contains(encoded, []byte(spec.escaped)) {
				t.Fatal("adversarial ID did not exercise JSON expansion", err)
			}
			if cost-base < len(id)+4*len(encoded) {
				t.Fatal("allowance cannot cover cloned ID and expanded encoding buffers")
			}
			read.Close()
			oversized := strings.Repeat(spec.fragment, maximum/len(spec.fragment)+1)
			view.mu.Lock()
			source.Teams[0].ID = oversized
			view.workspaces[workspace] = source
			view.mu.Unlock()
			if read, found, err := view.BeginChatTeamPolicyRead(t.Context(), workspace); read != nil || !found || !errors.Is(err, ErrMetadataWorkBudget) {
				t.Fatalf("expanded-output budget should reject before snapshot: found=%v err=%v", found, err)
			}
			if view.metadataResidency.work.used != 0 {
				t.Fatal("expanded ID output leaked admission")
			}
		})
	}
	if view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("encoded ID accounting decoded complete metadata")
	}
}

func TestScaleP21SW19ChatTeamPolicyParentLease(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	workspace := f.data.Workspace.URLKey
	view.mu.RLock()
	source := view.workspaces[workspace]
	want, err := scaleP21SW19ChatPolicyCost(t.Context(), source)
	view.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	const parentBytes = 8 << 20
	parent, releaseParent, err := view.admitMetadataWork(t.Context(), parentBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseParent()
	read, found, err := view.BeginChatTeamPolicyRead(parent, workspace)
	if err != nil || !found {
		t.Fatal("nested retained policy", found, err)
	}
	defer read.Close()
	if used := view.metadataResidency.work.used; used != parentBytes+want {
		t.Fatal("retained policy borrowed the parent allowance", used, want)
	}
	expected := slices.Clone(read.Metadata().Teams)
	releaseParent()
	if used := view.metadataResidency.work.used; used != want || !reflect.DeepEqual(read.Metadata().Teams, expected) {
		t.Fatal("parent release revoked the live output allowance", used, want)
	}
	read.Close()
	read.Close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("owned policy release leaked admission")
	}
	if read, found, err := view.BeginChatTeamPolicyRead(parent, workspace); read != nil || !found || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("released parent lease was revived", found, err)
	}
	full, releaseFull, err := view.admitMetadataWork(t.Context(), view.metadataResidency.work.limit)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFull()
	short, cancel := context.WithTimeout(full, 10*time.Millisecond)
	if read, found, err := view.BeginChatTeamPolicyRead(short, workspace); read != nil || !found || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("nested saturated policy waited behind its parent", found, err)
	}
	cancel()
	releaseFull()
	read, found, err = view.BeginChatTeamPolicyRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal("policy admission did not recover", found, err)
	}
	read.Close()
	if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("parent-policy regression leaked work or decoded metadata")
	}
}

func TestScaleP21SW19ChatTeamPolicyTimestampPresence(t *testing.T) {
	dst, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	zones := []*time.Location{time.UTC, time.Local, dst, time.FixedZone(strings.Repeat("retired-zone", 150000), 3600)}
	for index, zone := range zones {
		when := time.Date(2026, 7, 1, 12, 30, 0, 0, zone)
		original := domain.Team{ID: fmt.Sprintf("sw19-presence-%d", index), ArchivedAt: &when, RetiredAt: &when}
		source := &SQLiteStore{
			workspaces:        map[string]domain.Bootstrap{"physical": {Teams: []domain.Team{original}}},
			lastWorkspaceKey:  "physical",
			metadataResidency: newMetadataResidency(),
		}
		read, found, err := source.BeginChatTeamPolicyRead(t.Context(), "physical")
		if err != nil || !found {
			t.Fatal("presence read", found, err)
		}
		headers := read.Metadata().Teams
		if len(headers) != 1 || headers[0].ArchivedAt == nil || headers[0].RetiredAt == nil {
			t.Fatal("archive/retire presence lost")
		}
		for _, sentinel := range []*time.Time{headers[0].ArchivedAt, headers[0].RetiredAt} {
			if sentinel == &when || !sentinel.IsZero() || sentinel.Location() != time.UTC {
				t.Fatal("header retained a source date or timezone location")
			}
		}
		if headers[0].ArchivedAt == headers[0].RetiredAt {
			t.Fatal("caller-owned header pointers alias each other")
		}
		source.mu.Lock()
		if source.workspaces["physical"].Teams[0].ArchivedAt != &when || when.Location() != zone {
			t.Fatal("source timestamp was normalized by header construction")
		}
		source.workspaces["physical"] = domain.Bootstrap{Teams: []domain.Team{{ID: "replacement"}}}
		source.mu.Unlock()
		if !headers[0].ArchivedAt.IsZero() || headers[0].ArchivedAt.Location() != time.UTC || !headers[0].RetiredAt.IsZero() || headers[0].RetiredAt.Location() != time.UTC {
			t.Fatal("retained header pinned the replaced source timezone")
		}
		read.Close()
		if source.metadataResidency.work.used != 0 {
			t.Fatal("presence header leaked admission")
		}
	}
}
