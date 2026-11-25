package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"flow/api/internal/boundedcrdt"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type boundedMentionWork struct {
	work     domain.CollaborationWork
	releases []func()
}

func (w *boundedMentionWork) close() {
	for _, release := range w.releases {
		release()
	}
	w.releases = nil
}

func (w *boundedMentionWork) collect(value any, result *[]string) error {
	if err := w.work.Context.Err(); err != nil {
		return err
	}
	// Each recursive frame is admitted before descending; deep supported
	// documents consume the same process budget as their decoded tree.
	release, err := w.work.Admit(4096)
	if err != nil {
		return err
	}
	defer release()
	switch node := value.(type) {
	case map[string]any:
		attrs, _ := node["attrs"].(map[string]any)
		mentionLike := node["type"] == "mention" && domain.IsUserMention(attrs) || node["type"] == "userMention" || node["type"] == "user"
		if mentionLike {
			for _, properties := range []map[string]any{node, attrs} {
				for _, key := range []string{"userId", "id"} {
					id, ok := properties[key].(string)
					if !ok || id == "" || slices.Contains(*result, id) {
						continue
					}
					// appendUnique uses ordinary slice growth. Explicit
					// capacity makes that growth and ownership predictable.
					if len(*result) == cap(*result) {
						capacity := max(1, cap(*result)*2)
						release, err := w.work.Admit(uint64(capacity)*16 + 64)
						if err != nil {
							return err
						}
						w.releases = append(w.releases, release)
						next := make([]string, len(*result), capacity)
						copy(next, *result)
						*result = next
					}
					*result = append(*result, id)
				}
			}
		}
		for _, child := range node {
			if err := w.collect(child, result); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range node {
			if err := w.collect(child, result); err != nil {
				return err
			}
		}
	case json.RawMessage:
		decoded, release, err := boundedcrdt.DecodeJSON(w.work.Context, w.work.Admit, node)
		if err != nil {
			if errors.Is(err, boundedcrdt.ErrInvalidUpdate) {
				return nil
			}
			return err
		}
		defer release()
		return w.collect(decoded, result)
	}
	return nil
}

func decodeBoundedMentionJSON(work domain.CollaborationWork, value string) (any, func(), error) {
	if value == "" {
		return nil, nil, boundedcrdt.ErrInvalidUpdate
	}
	capacity := uint64(1)
	for capacity < uint64(len(value)) {
		capacity *= 2
	}
	if capacity > uint64(int(^uint(0)>>1))-1024 {
		return nil, nil, store.ErrMetadataWorkBudget
	}
	release, err := work.Admit(capacity + 1024)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	raw := make([]byte, len(value), int(capacity))
	copy(raw, value)
	return boundedcrdt.DecodeJSON(work.Context, work.Admit, raw)
}

func refuseBoundedFormerMemberDescriptionMentions(data *domain.Bootstrap, issue *domain.Issue, input domain.IssueUpdateInput) error {
	if input.DescriptionData == nil && input.DescriptionState == nil && input.ContentState == nil {
		return nil
	}
	work := data.CollaborationWork()
	var previous any
	state, documentID := "", ""
	if issue != nil {
		documentID = "document_content_" + issue.ID
		if issue.DocumentContent != nil {
			previous, state = issue.DocumentContent.ContentData, issue.DocumentContent.ContentState
			documentID = firstNonEmpty(issue.DocumentContent.ID, documentID)
		}
		document, release, err := decodeBoundedMentionJSON(work, issue.DescriptionState)
		if err == nil {
			defer release()
			previous = []any{previous, document}
		} else if !errors.Is(err, boundedcrdt.ErrInvalidUpdate) {
			return err
		}
	}
	if input.ContentState != nil {
		if err := refuseFormerMemberStateMentions(data, previous, state, *input.ContentState, documentID); err != nil {
			return err
		}
	}
	if input.DescriptionState != nil {
		document, release, err := decodeBoundedMentionJSON(work, *input.DescriptionState)
		if err == nil {
			defer release()
			if err := refuseFormerMemberRichMentions(data, previous, state, document, documentID); err != nil {
				return err
			}
		} else if !errors.Is(err, boundedcrdt.ErrInvalidUpdate) {
			return err
		}
	}
	return refuseFormerMemberRichMentions(data, previous, state, input.DescriptionData, documentID)
}

func boundedMentionError(err error) error {
	if errors.Is(err, boundedcrdt.ErrInvalidState) {
		return fmt.Errorf("%w: invalid collaboration state", errInvalid)
	}
	if errors.Is(err, boundedcrdt.ErrInvalidUpdate) {
		return fmt.Errorf("%w: invalid collaboration update", errInvalid)
	}
	return err
}

func refuseBoundedFormerMemberStateMentions(data *domain.Bootstrap, beforeData any, beforeState, afterState, documentID string) error {
	if afterState == "" || data.FormerMembers == nil || len(data.FormerMembers()) == 0 {
		return nil
	}
	work := data.CollaborationWork()
	after, err := boundedcrdt.NewDoc(work.Context, work.Admit)
	if err != nil {
		return err
	}
	defer after.Close()
	if err := after.ApplyState(afterState); err != nil {
		return boundedMentionError(err)
	}
	tree, err := after.MentionDocument()
	if err != nil {
		return boundedMentionError(err)
	}
	return refuseBoundedFormerMemberRichMentions(data, beforeData, beforeState, tree, documentID)
}

func refuseBoundedFormerMemberRichMentions(data *domain.Bootstrap, beforeData any, beforeState string, after any, documentID string) error {
	work := &boundedMentionWork{work: data.CollaborationWork()}
	defer work.close()
	var mentioned []string
	if err := work.collect(after, &mentioned); err != nil {
		return err
	}
	if len(mentioned) == 0 || data.FormerMembers == nil || len(data.FormerMembers()) == 0 {
		return nil
	}
	before, err := boundedcrdt.NewDoc(work.work.Context, work.work.Admit)
	if err != nil {
		return err
	}
	defer func() { before.Close() }()
	if err := before.ApplyState(beforeState); err != nil {
		if !errors.Is(err, boundedcrdt.ErrInvalidUpdate) {
			return err
		}
		// Legacy permits replacing unreadable old content. Admission and
		// cancellation are never evidence that the old content is invalid.
		before.Close()
		before, err = boundedcrdt.NewDoc(work.work.Context, work.work.Admit)
		if err != nil {
			return err
		}
	}
	if data.CollaborationUpdates != nil && documentID != "" {
		updates, err := data.CollaborationUpdates(documentID)
		if err != nil {
			return err
		}
		for _, update := range updates {
			if err := before.ApplyUpdate(update); err != nil {
				return boundedMentionError(err)
			}
		}
	}
	tree, err := before.MentionDocument()
	if err != nil {
		return boundedMentionError(err)
	}
	var kept []string
	if err := work.collect(beforeData, &kept); err != nil {
		return err
	}
	if err := work.collect(tree, &kept); err != nil {
		return err
	}
	for _, id := range mentioned {
		if !slices.Contains(kept, id) && formerMember(data, id) {
			return errFormerMemberMention
		}
	}
	return nil
}
