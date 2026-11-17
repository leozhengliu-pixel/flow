package store

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

type legacySessionOccurrence struct {
	node    metadataLogicalNode
	ordinal int
}

func legacySessionFirstOccurrences(ctx context.Context, owner *ResourceRead, field metadataLogicalNode) (map[string]legacySessionOccurrence, error) {
	result := map[string]legacySessionOccurrence{}
	err := field.Children(ctx, func(index reflect.Value, session metadataLogicalNode) error {
		idNode, err := issueHotField(ctx, session, "ID")
		if err != nil {
			return err
		}
		value, err := owner.node(ctx, idNode, false)
		if err != nil {
			return err
		}
		id := value.String()
		if _, found := result[id]; !found {
			if err := owner.reserve(ctx, uint64(len(id))*8+512); err != nil {
				return err
			}
			result[id] = legacySessionOccurrence{session, int(index.Int())}
		}
		return nil
	})
	return result, err
}

// A full inline/duplicate collection still rebases only its first occurrence
// per ID. This mirrors newMetadataCollection; later duplicate edits survive
// unchanged unless the literal first-record rebase itself replaces/drops one.
func (p *legacyMetadataWritePlan) rebaseAgentSessionLifecycleCollection(ctx context.Context, field *legacyMetadataWriteField) (bool, error) {
	if !legacySessionLifecycleWrite(ctx) || field.field.name != "agentSessions" || !field.full {
		return false, nil
	}
	owner := &ResourceRead{store: p.store}
	defer owner.Close()
	if err := owner.reserve(ctx, 2<<20); err != nil {
		return true, err
	}
	left, err := legacySessionFirstOccurrences(ctx, owner, field.before)
	if err != nil {
		return true, err
	}
	right, err := legacySessionFirstOccurrences(ctx, owner, field.next)
	if err != nil {
		return true, err
	}
	keys := make([]string, 0, len(right))
	for id := range right {
		if _, found := left[id]; found {
			keys = append(keys, id)
		}
	}
	slices.Sort(keys)
	beforePatch := legacyDiscussionSlicePatch{
		base: field.before, shape: field.before,
		replacements: map[int]metadataLogicalNode{}, dropped: map[int]bool{},
	}
	nextPatch := legacyDiscussionSlicePatch{
		base: field.next, shape: field.next,
		replacements: map[int]metadataLogicalNode{}, dropped: map[int]bool{},
	}
	for _, id := range keys {
		a, b := left[id], right[id]
		same, err := legacyMetadataWriteEqual(ctx, a.node, b.node, 0)
		if err != nil {
			return true, err
		}
		if same {
			continue
		}
		selected := &legacyMetadataWriteField{
			field: field.field, before: field.before, next: field.next,
			rows: []legacyMetadataWriteRow{{id: id, before: a.node, next: b.node, changed: true}},
		}
		handled, err := p.rebaseAgentSessionLifecycleRow(ctx, selected, 0)
		if err != nil {
			return true, err
		}
		if !handled {
			// Small canonical rows use the existing generic kernel; a
			// genuinely inline field remains unchanged in that kernel.
			if err := p.rebaseRow(ctx, selected, 0); err != nil {
				return true, err
			}
		}
		row := selected.rows[0]
		if row.deleted {
			beforePatch.dropped[a.ordinal], nextPatch.dropped[b.ordinal] = true, true
		} else {
			beforePatch.replacements[a.ordinal] = row.before
			nextPatch.replacements[b.ordinal] = row.next
		}
	}
	field.before, err = p.frames.writeAggregateNode(beforePatch, 0)
	if err != nil {
		return true, err
	}
	field.next, err = p.frames.writeAggregateNode(nextPatch, 0)
	return true, err
}

type legacySessionSplitImage struct {
	valid bool
	items []domain.Issue
	nodes map[string]metadataLogicalNode
}

// splitWorkspaceMetadata decides shape from JSON-decoded IDs. Reproduce that
// decision without reading any transcript into a []json.RawMessage.
func legacySessionSplit(ctx context.Context, owner *ResourceRead, field metadataLogicalNode) (legacySessionSplitImage, error) {
	result := legacySessionSplitImage{nodes: map[string]metadataLogicalNode{}}
	nilField, err := field.isNil(ctx)
	if err != nil || nilField {
		return result, err
	}
	result.valid = true
	err = field.Children(ctx, func(index reflect.Value, session metadataLogicalNode) error {
		idNode, err := issueHotField(ctx, session, "ID")
		if err != nil {
			return err
		}
		value, err := owner.node(ctx, idNode, false)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(value.String())
		if err != nil {
			return err
		}
		var id string
		if err := json.Unmarshal(raw, &id); err != nil {
			return err
		}
		_, duplicate := result.nodes[id]
		if id == "" || len(id) > 191 || duplicate {
			result.valid = false
		}
		if err := owner.reserve(ctx, uint64(len(id))*8+1024); err != nil {
			return err
		}
		result.items = append(result.items, domain.Issue{ID: id, Number: int(index.Int())})
		result.nodes[id] = session
		return nil
	})
	if !result.valid {
		result.items, result.nodes = nil, nil
	}
	return result, err
}
