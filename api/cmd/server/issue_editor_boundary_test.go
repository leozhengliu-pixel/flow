package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"flow/api/internal/domain"
)

// A DescriptionState-only request still reconstructs the old CRDT tree when
// deciding whether a former-member mention is retained. It cannot be admitted
// solely by accounting for the new JSON description.
func TestScaleP21IssueEditorDescriptionStateReadsCRDT(t *testing.T) {
	state := mentioningCollaborationState("departed-without-directory-entry")
	update, err := base64.StdEncoding.DecodeString(state)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(mentioningDocument("Retained ", "departed-without-directory-entry"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		beforeState string
		updates     [][]byte
		former      map[string]bool
		want        error
		wantCalls   int
	}{
		{"new-mention", "", nil, map[string]bool{"departed-without-directory-entry": true}, errFormerMemberMention, 1},
		{"retained-in-old-crdt", state, nil, map[string]bool{"departed-without-directory-entry": true}, nil, 1},
		{"retained-in-pending-crdt", "", [][]byte{update}, map[string]bool{"departed-without-directory-entry": true}, nil, 1},
		{"malformed-pending-crdt", "", [][]byte{[]byte("malformed")}, map[string]bool{"departed-without-directory-entry": true}, errInvalid, 1},
		{"empty-former-map", "", [][]byte{[]byte("malformed")}, map[string]bool{}, nil, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			data := domain.Bootstrap{
				FormerMembers: func() map[string]bool { return test.former },
				CollaborationUpdates: func(documentID string) ([][]byte, error) {
					calls++
					if documentID != "old-generation" {
						t.Fatal("request checked a replacement generation", documentID)
					}
					return test.updates, nil
				},
			}
			issue := domain.Issue{ID: "issue", DocumentContent: &domain.DocumentContent{ID: "old-generation", ContentState: test.beforeState}}
			input := domain.IssueUpdateInput{DescriptionState: new(string(after))}
			if input.ContentState != nil {
				t.Fatal("fixture unexpectedly supplied CRDT state")
			}
			err := refuseNewFormerMemberDescriptionMentions(&data, &issue, input)
			if !errors.Is(err, test.want) || calls != test.wantCalls {
				t.Fatal("DescriptionState-only request lost old/pending CRDT semantics", err, calls)
			}
		})
	}
}
