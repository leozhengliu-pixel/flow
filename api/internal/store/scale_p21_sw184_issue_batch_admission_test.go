package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW184IssueBatchEncodingOwnership(t *testing.T) {
	s := &SQLiteStore{metadataResidency: newMetadataResidency()}
	issue := domain.Issue{ID: "sw184", Description: strings.Repeat("\x00<&", 8192)}
	check := func(t *testing.T, reserve func(context.Context) (func(), error)) {
		t.Helper()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := reserve(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled admission: %v", err)
		}
		if s.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("cancelled admission retained work")
		}
		for range 1000 {
			release, err := reserve(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if s.MetadataWorkUsedForTesting() == 0 {
				t.Fatal("live encoding work was not admitted")
			}
			release()
			release()
			if s.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("released encoding work remained charged")
			}
		}
		_, exhausted, err := s.tryAdmitAdditionalMetadataWork(t.Context(), metadataWorkBytes)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reserve(t.Context()); !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatalf("exhausted admission: %v", err)
		}
		exhausted()
		if s.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("budget refusal retained work")
		}
	}
	t.Run("row", func(t *testing.T) {
		check(t, func(ctx context.Context) (func(), error) {
			return reserveIssueRecordEncoding(ctx, s, issue, []byte(`{"id":"sw184","description":"<&"}`))
		})
	})
	t.Run("callback", func(t *testing.T) {
		check(t, func(ctx context.Context) (func(), error) {
			return reserveIssueBatchCallbackEncoding(ctx, s, domain.Bootstrap{Issues: []domain.Issue{issue}}, domain.IssueUpdateInput{})
		})
	})
	t.Run("content", func(t *testing.T) {
		check(t, func(ctx context.Context) (func(), error) {
			return reserveIssueBatchContentEncoding(ctx, s, domain.Bootstrap{
				Comments: map[string][]domain.Comment{"sw184": {{ID: "sw184-comment", Body: issue.Description}}},
			})
		})
	})
}

func TestScaleP21SW184IssueBatchEncodingFlush(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	at := time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
	prefix := idgen.New("sw184-flush-")
	var issues []domain.Issue
	for i := range 5 {
		issues = append(issues, domain.Issue{
			ID: fmt.Sprintf("%s%d", prefix, i), Identifier: fmt.Sprintf("SW184-%d", i), Number: 19000 + i,
			Version: 2, Title: "Flushed <>& encoding", Description: strings.Repeat("owned <>& ", 256),
			Team: f.data.Teams[0], State: domain.WorkflowState{ID: "state_backlog", Type: "backlog"},
			Creator: f.data.Viewer, CreatedAt: at, UpdatedAt: at,
		})
	}
	view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency()}
	release, err := reserveIssueRecordEncoding(t.Context(), view, issues[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	one := view.MetadataWorkUsedForTesting()
	release()
	view.metadataResidency.work.limit = one + one/2
	var want map[string]storedIssueRecord
	for _, bounded := range []bool{false, true} {
		s := f.SQLiteStore
		ctx := t.Context()
		if bounded {
			s, ctx = view, withIssueRecordWriteAdmission(ctx, view)
		}
		inserts := 0
		ctx = WithSQLStatementObserver(ctx, func(query string) {
			if strings.HasPrefix(query, issueRecordInsert) {
				inserts++
			}
		})
		tx, err := f.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = s.importIssueRecordBatch(ctx, tx, f.data.Workspace.URLKey, issues)
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		var ids []string
		for _, issue := range issues {
			ids = append(ids, issue.ID)
		}
		got, err := f.loadIssueRecordsByID(t.Context(), tx, f.data.Workspace.URLKey, ids, false)
		closeErr := tx.Rollback()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
		if !bounded {
			want = got
			if inserts != 1 {
				t.Fatalf("legacy insert batches=%d, want 1", inserts)
			}
		} else if !reflect.DeepEqual(got, want) || inserts != len(issues) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatalf("bounded flush changed rows or leaked work: inserts=%d work=%d", inserts, view.MetadataWorkUsedForTesting())
		}
		t.Logf("bounded=%v insert batches=%d row allowance=%d work limit=%d", bounded, inserts, one, view.metadataResidency.work.limit)
	}
}
