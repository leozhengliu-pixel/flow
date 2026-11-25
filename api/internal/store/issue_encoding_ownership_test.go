package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW224IssueEncodingOwnershipTransition(t *testing.T) {
	issue := domain.Issue{ID: "sw224-issue", Description: strings.Repeat("x", 4<<20)}
	previous, err := json.Marshal(issue)
	if err != nil {
		t.Fatal(err)
	}
	repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
	full, err := reserveIssueRecordEncoding(t.Context(), repo, issue, previous)
	if err != nil {
		t.Fatal(err)
	}
	envelope := repo.MetadataWorkUsedForTesting()
	full()
	if envelope == 0 || repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("complete comparison envelope has no independent owner")
	}
	repo.metadataResidency.work.limit = envelope
	columns, err := reserveIssueRecordEncoding(t.Context(), repo, issue, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer columns()
	initial := repo.MetadataWorkUsedForTesting()
	repo.metadataResidency.work.limit = envelope - 1
	_, err = reserveIssueRecordComparisonEncoding(t.Context(), repo, issue, previous)
	if !errors.Is(err, ErrMetadataWorkBudget) || repo.MetadataWorkUsedForTesting() != initial {
		t.Fatal("failed growth changed the existing column owner", err)
	}
	repo.metadataResidency.work.limit = envelope
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = reserveIssueRecordComparisonEncoding(cancelled, repo, issue, previous)
	if !errors.Is(err, context.Canceled) || repo.MetadataWorkUsedForTesting() != initial {
		t.Fatal("cancelled growth changed the existing column owner", err)
	}
	comparison, err := reserveIssueRecordComparisonEncoding(t.Context(), repo, issue, previous)
	if err != nil {
		t.Fatal("non-overlapping ownership refused the complete envelope", err)
	}
	if repo.MetadataWorkUsedForTesting() != envelope {
		t.Fatal("column and comparison owners changed the complete accounting bound")
	}
	comparison()
	if repo.MetadataWorkUsedForTesting() != initial {
		t.Fatal("comparison cleanup released the live columns")
	}
	columns()
	if repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("encoding ownership leaked")
	}
}
