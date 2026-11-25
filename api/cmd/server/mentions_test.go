package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"

	"github.com/Deln0r/ygo"
)

type scaleP21SW189Work struct {
	ctx        context.Context
	used, peak uint64
	limit      uint64
}

func (w *scaleP21SW189Work) admit(cost uint64) (func(), error) {
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	if cost > w.limit-w.used {
		return nil, store.ErrMetadataWorkBudget
	}
	w.used += cost
	w.peak = max(w.peak, w.used)
	live := true
	return func() {
		if live {
			live = false
			w.used -= cost
		}
	}, nil
}

func TestScaleP21SW189MentionDifferential(t *testing.T) {
	former := "sw189-former"
	state := mentioningCollaborationState(former)
	pending, err := base64.StdEncoding.DecodeString(state)
	if err != nil {
		t.Fatal(err)
	}
	doc := ygo.NewDoc()
	fragment := ygo.NewXmlFragment(doc, "prosemirror")
	txn := doc.WriteTxn()
	mention := fragment.InsertXmlElement(txn, 0, "mention")
	mention.SetAttribute(txn, "id", former)
	mention.SetAttribute(txn, "mentionType", "user")
	txn.Commit()
	txn = doc.WriteTxn()
	fragment.Delete(txn, 0, 1)
	txn.Commit()
	deleted := base64.StdEncoding.EncodeToString(ygo.EncodeStateAsUpdate(doc))
	for _, test := range []struct {
		name, before, after string
		data                any
		updates             [][]byte
		want                error
	}{
		{name: "new", after: state, want: errFormerMemberMention},
		{name: "retained-state", before: state, after: state},
		{name: "retained-rich", data: mentioningDocument("before", former), after: state},
		{name: "retained-raw", data: json.RawMessage(fmt.Sprintf(`{"type":"mention","attrs":{"id":%q,"mentionType":"user"}}`, former)), after: state},
		{name: "retained-pending", updates: [][]byte{pending}, after: state},
		{name: "old-invalid", before: "invalid", after: state, want: errFormerMemberMention},
		{name: "new-invalid", after: "invalid", want: errInvalid},
		{name: "queued-invalid", updates: [][]byte{{255}}, after: state, want: errInvalid},
		{name: "deleted-history", after: deleted},
		{name: "empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := domain.Bootstrap{
				FormerMembers:        func() map[string]bool { return map[string]bool{former: true} },
				CollaborationUpdates: func(string) ([][]byte, error) { return test.updates, nil },
			}
			old := refuseFormerMemberStateMentions(&original, test.data, test.before, test.after, "document")
			work := &scaleP21SW189Work{ctx: t.Context(), limit: 256 << 20}
			bounded := original
			bounded.CollaborationWork = func() domain.CollaborationWork {
				return domain.CollaborationWork{Context: work.ctx, Admit: work.admit}
			}
			got := refuseFormerMemberStateMentions(&bounded, test.data, test.before, test.after, "document")
			if (old == nil) != (got == nil) || old != nil && old.Error() != got.Error() {
				t.Fatalf("legacy=%v bounded=%v", old, got)
			}
			if test.want != nil && !errors.Is(got, test.want) {
				t.Fatalf("got %v want %v", got, test.want)
			}
			if work.used != 0 {
				t.Fatalf("document check retained %d bytes", work.used)
			}
			t.Logf("peak admitted=%d", work.peak)
		})
	}
}

func TestScaleP21SW189ContentStatePermissions(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	state := mentioningCollaborationState(f.data.Viewer.ID)
	users := []domain.User{f.data.Viewer, f.principals[1].user, f.principals[2].user, f.principals[3].user, f.principals[4].user}
	keys := []*domain.APIKey{
		nil,
		{ID: "sw189-unrestricted", Scopes: []string{"read", "write", "admin"}},
		{ID: "sw189-empty", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{}},
		{ID: "sw189-selected", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}},
	}
	var cases []scaleP21IssueUpdateCase
	for principal, user := range users {
		for credential, key := range keys {
			for _, issue := range []string{f.triage.ID, f.hidden.ID} {
				cases = append(cases, scaleP21IssueUpdateCase{
					name: fmt.Sprintf("principal-%d/key-%d/%s", principal, credential, issue),
					user: user, key: key, issue: issue, body: map[string]any{"contentState": state},
				})
			}
		}
	}
	cases = append(cases, scaleP21IssueUpdateCase{
		name: "old-identifier", user: f.data.Viewer, issue: f.triage.Identifier, body: map[string]any{"contentState": state},
	})
	f.runCases(t, cases)
}

func TestScaleP21SW189MentionFailureIsNotInvalidOldState(t *testing.T) {
	state := mentioningCollaborationState("former")
	for _, failure := range []error{store.ErrMetadataWorkBudget, context.Canceled} {
		t.Run(failure.Error(), func(t *testing.T) {
			data := domain.Bootstrap{FormerMembers: func() map[string]bool { return map[string]bool{"former": true} }}
			data.CollaborationWork = func() domain.CollaborationWork {
				return domain.CollaborationWork{
					Context: t.Context(),
					Admit:   func(uint64) (func(), error) { return nil, failure },
				}
			}
			if err := refuseFormerMemberRichMentions(&data, nil, state, mentioningDocument("before", "former"), ""); !errors.Is(err, failure) {
				t.Fatalf("old-state work failure treated as malformed: %v", err)
			}
		})
	}
}

func TestScaleP21SW189ContentStateRouteDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	former := mentioningCollaborationState(f.former.ID)
	active := mentioningCollaborationState(f.data.Viewer.ID)
	f.runCases(t, []scaleP21IssueUpdateCase{
		{name: "content-state-new-former", user: f.data.Viewer, issue: f.triage.ID, body: map[string]any{"contentState": former}, code: 400},
		{name: "content-state-active", user: f.data.Viewer, issue: f.triage.ID, body: map[string]any{"contentState": active}, code: 200},
		{name: "content-state-retained", user: f.data.Viewer, issue: f.triage.ID, body: map[string]any{"contentState": former}, code: 200, edit: func(issue *domain.Issue) {
			issue.DocumentContent = &domain.DocumentContent{ID: "document_content_" + issue.ID, ContentState: former}
		}},
		{name: "content-state-malformed", user: f.data.Viewer, issue: f.triage.ID, body: map[string]any{"contentState": "invalid"}, code: 400},
	})
	f.runBatchCases(t, []scaleP21IssueBatchCase{
		{name: "content-state-active", user: f.data.Viewer, ids: []string{f.triage.ID, f.visible.ID}, update: map[string]any{"contentState": active}, code: 200},
		{name: "content-state-new-former-rollback", user: f.data.Viewer, ids: []string{f.triage.ID, f.visible.ID}, update: map[string]any{"contentState": former}, code: 400},
	})
}

func BenchmarkScaleP21SW189BoundedMentions(b *testing.B) {
	for _, count := range []int{100, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			ids := make([]string, count)
			for index := range ids {
				ids[index] = fmt.Sprintf("former-%d", index)
			}
			state := mentioningCollaborationState(ids...)
			work := &scaleP21SW189Work{ctx: b.Context(), limit: 256 << 20}
			data := domain.Bootstrap{FormerMembers: func() map[string]bool { return map[string]bool{ids[0]: true} }}
			data.CollaborationWork = func() domain.CollaborationWork {
				return domain.CollaborationWork{Context: work.ctx, Admit: work.admit}
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(state)))
			b.ResetTimer()
			for range b.N {
				if err := refuseFormerMemberStateMentions(&data, nil, state, state, ""); err != nil {
					b.Fatal(err)
				}
				if work.used != 0 {
					b.Fatal("document work leaked")
				}
			}
		})
	}
}
