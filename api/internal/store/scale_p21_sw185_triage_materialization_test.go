package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW185CandidateSpoolOwnership(t *testing.T) {
	s := &SQLiteStore{metadataResidency: newMetadataResidency()}
	read, err := s.BeginIssueSuggestionCandidates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	path := read.file.Name()
	issues := []domain.Issue{
		{ID: "duplicate", Title: "First snapshot", Number: 7, Team: domain.Team{ID: "team"}},
		{ID: "duplicate", Title: "Later snapshot", Number: 8, Team: domain.Team{ID: "team"}},
	}
	if err := read.Start(t.Context(), "requested"); err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if err := read.Append(t.Context(), issue); err != nil {
			t.Fatal(err)
		}
	}
	if err := read.Finish(); err != nil {
		t.Fatal(err)
	}
	scores, err := read.BeginScores(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var got []domain.Issue
	if err := read.Walk(t.Context(), "requested", func(issue domain.Issue) (bool, error) {
		if err := scores.Keep(t.Context(), issue); err != nil {
			return false, err
		}
		got = append(got, issue)
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(issues)
	after, _ := json.Marshal(got)
	if string(before) != string(after) || read.Number("requested") != 8 || !reflect.DeepEqual(read.Teams([]string{"requested"}), []string{"team"}) {
		t.Fatal("spooling changed duplicate encounter order, numbers, or teams")
	}
	projectInput := domain.Issue{
		ID: "projected", Identifier: "PROJ-9", Title: "Unused display fallback", Description: strings.Repeat("Unused after numeric scoring.", 10000),
		Team: domain.Team{ID: "team", Name: "Unused name"}, Project: &domain.ProjectSummary{ID: "project", Name: "Unused name"},
		Assignee: &domain.User{ID: "user", Name: "Unused name"}, Labels: []domain.IssueLabel{{ID: "label", Description: "Unused body"}},
	}
	used := s.MetadataWorkUsedForTesting()
	projected, err := scores.Project(t.Context(), projectInput)
	if err != nil || projected.ID != projectInput.ID || projected.Identifier != projectInput.Identifier ||
		projected.Description != "" || projected.Title != "" || projected.Team.ID != "team" ||
		projected.Project.ID != "project" || projected.Assignee.ID != "user" || projected.Labels[0].ID != "label" {
		t.Fatal("score projection lost a downstream input or retained unused bodies", err)
	}
	if delta := s.MetadataWorkUsedForTesting() - used; delta <= 0 || delta > 64<<10 {
		t.Fatalf("scoring projection retained unrelated body decoding: %d", delta)
	}
	scores.Close()
	if err := scores.Keep(t.Context(), issues[0]); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("closed scoring owner accepted another value", err)
	}
	mutation := issues[0]
	mutation.Relations = []domain.IssueRelation{{ID: "relation", RelatedIssueID: "scoped"}}
	var held int
	for index := 0; index < 20; index++ {
		if err := read.SetMutation(t.Context(), mutation); err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			held = s.MetadataWorkUsedForTesting()
		} else if s.MetadataWorkUsedForTesting() != held {
			t.Fatal("replaced candidate mutation accumulated admission")
		}
	}
	if value, found := read.Mutation("duplicate"); !found || !reflect.DeepEqual(value, mutation) {
		t.Fatal("candidate first-occurrence mutation was lost")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := read.Walk(cancelled, "requested", func(domain.Issue) (bool, error) {
		t.Fatal("cancelled candidate walk reached visitor")
		return false, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled walk", err)
	}
	read.Close()
	read.Close()
	if err := read.Walk(t.Context(), "requested", func(domain.Issue) (bool, error) { return true, nil }); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("closed candidate spool accepted another walk", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) || s.MetadataWorkUsedForTesting() != 0 {
		t.Fatalf("candidate spool or admission survived Close: stat=%v held=%d", err, s.MetadataWorkUsedForTesting())
	}
}

func TestScaleP21SW185InlineFieldMaterializationOwnership(t *testing.T) {
	before := domain.Bootstrap{IssueSuggestions: []domain.IssueSuggestion{{ID: "duplicate"}, {ID: "duplicate"}}}
	after := cloneBootstrap(before)
	for index := 0; index < 1000; index++ {
		after.IssueSuggestions = append(after.IssueSuggestions, domain.IssueSuggestion{
			ID: fmt.Sprintf("suggestion-%d", index), IssueID: fmt.Sprintf("issue-%d", index), Type: "label",
			Metadata: map[string]any{"reasons": []string{strings.Repeat("<>&\x00", 256)}, "rank": index},
		})
	}
	left, err := captureMetadataLogicalSource(t.Context(), before, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Release()
	right, err := captureMetadataLogicalSource(t.Context(), after, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Release()
	field, found := metadataFieldByName("issueSuggestions")
	if !found {
		t.Fatal("suggestions field missing")
	}
	f := &legacyMetadataWriteField{field: field, full: true, before: left.fields[field.index], next: right.fields[field.index]}
	s := &SQLiteStore{metadataResidency: newMetadataResidency()}
	plan := &legacyMetadataWritePlan{store: s}
	if (f.before.cost+f.next.cost)*64 <= metadataWorkBytes {
		t.Fatal("fixture does not exceed the previous full-field blanket admission")
	}
	work := &ResourceRead{store: s}
	a, b, err := plan.materializeField(t.Context(), f, work)
	if err != nil {
		work.Close()
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Interface(), before.IssueSuggestions) || !reflect.DeepEqual(b.Interface(), after.IssueSuggestions) {
		t.Fatal("materialization changed duplicate IDs, escaping, or native metadata values")
	}
	if held := s.MetadataWorkUsedForTesting(); held == 0 || held >= metadataWorkBytes {
		t.Fatalf("materialized values lost their allowance: %d", held)
	} else {
		t.Logf("old_blanket_bytes=%d live_admitted_bytes=%d", (f.before.cost+f.next.cost)*64, held)
	}
	work.Close()
	if s.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("materialized field retained admission after release")
	}
	for _, test := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"cancelled", func() context.Context { ctx, cancel := context.WithCancel(t.Context()); cancel(); return ctx }(), context.Canceled},
		{"saturated", t.Context(), ErrMetadataWorkBudget},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "saturated" {
				s.metadataResidency.work.limit = 1
			}
			work := &ResourceRead{store: s}
			a, b, err := plan.materializeField(test.ctx, f, work)
			work.Close()
			if !errors.Is(err, test.want) || a.IsValid() || b.IsValid() || s.MetadataWorkUsedForTesting() != 0 {
				t.Fatalf("failure escaped owned lifetime: a=%t b=%t error=%v held=%d", a.IsValid(), b.IsValid(), err, s.MetadataWorkUsedForTesting())
			}
		})
	}
}
