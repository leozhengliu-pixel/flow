package store

import (
	"context"
	"crypto/sha256"
	"io"
	"reflect"

	"flow/api/internal/domain"
)

type legacyIssueFrameRows struct {
	base   metadataLogicalNode
	fields map[string][]string
}

func (n legacyIssueFrameRows) nodeType() reflect.Type           { return n.base.typ }
func (n legacyIssueFrameRows) logicalBase() metadataLogicalNode { return n.base }
func (n legacyIssueFrameRows) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyIssueFrameRows) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.Children(ctx, func(key reflect.Value, item metadataLogicalNode) error {
		return visit(key, legacyIssueColumnHeaderNode{
			base: metadataAggregateStoredNode{item}, fields: n.fields,
		})
	})
}

type legacyIssueFrameInputNode struct {
	base metadataLogicalNode
}

func (n legacyIssueFrameInputNode) nodeType() reflect.Type { return n.base.typ }
func (n legacyIssueFrameInputNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyIssueFrameInputNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.Children(ctx, func(key reflect.Value, item metadataLogicalNode) error {
		var fields map[string][]string
		switch key.String() {
		case "Labels":
			fields = map[string][]string{"ID": nil}
		case "Attachments":
			fields = map[string][]string{"URL": nil}
		case "Relations":
			fields = map[string][]string{"Type": nil}
		case "Permissions":
			fields = map[string][]string{"SubjectType": nil, "SubjectID": nil, "Role": nil}
		}
		if fields != nil {
			return visit(key, legacyIssueFrameRows{base: item, fields: fields})
		}
		return visit(key, metadataAggregateStoredNode{item})
	})
}

// Index inputs retain the native strings read by the literal index writer.
// In particular Description reaches search before SQL JSON NUL stripping.
// The editor snapshots, reactions and embedded display bodies remain frames.
func legacyIssueFrameIndexInputs(ctx context.Context, owner *ResourceRead, frames *metadataLogicalBuilder, node metadataLogicalNode) (domain.Issue, error) {
	projected, err := frames.writeAggregateNode(legacyIssueColumnHeaderNode{
		base: legacyIssueFrameInputNode{node},
		fields: map[string][]string{
			"ID": nil, "Identifier": nil, "Title": nil, "Description": nil,
			"Labels": nil, "SubscriberIDs": nil, "Permissions": nil,
			"PreviousIdentifiers": nil, "Attachments": nil,
			"ProjectMilestoneID": nil, "DueDate": nil, "CompletedAt": nil,
			"CanceledAt": nil, "StartedAt": nil, "AutoClosedAt": nil,
			"Estimate": nil, "TemplateID": nil, "ExternalSource": nil,
			"Delegate": {"ID"}, "AgentSessionID": nil, "AddedToCycle": nil,
			"AutoClosed": nil, "RecurrenceSeriesID": nil, "RecurrenceOccurrence": nil,
			"TriagedAt": nil, "StatusChangedAt": nil, "NextOccurrenceAt": nil,
			"SnoozedUntil": nil, "Relations": nil,
			"SuggestedLabelIDs": nil, "SuggestedAssigneeIDs": nil,
			"SuggestedProjectIDs": nil, "SuggestedTeamIDs": nil,
			"SuggestedDuplicateIDs": nil, "SuggestedRelatedIDs": nil,
		},
	}, 0)
	if err != nil {
		return domain.Issue{}, err
	}
	value, err := owner.node(ctx, projected, false)
	if err != nil {
		return domain.Issue{}, err
	}
	return value.Interface().(domain.Issue), nil
}

// The CAS header supplies every native issueCASValues input except description.
// Its labels retain only IDs: display snapshots and IssueCount never enter CAS.
func legacyIssueFrameCASInputs(ctx context.Context, owner *ResourceRead, frames *metadataLogicalBuilder, node metadataLogicalNode) (domain.Issue, error) {
	if node.typ == nil {
		return domain.Issue{}, nil
	}
	projected, err := frames.writeAggregateNode(legacyIssueColumnHeaderNode{
		base: legacyIssueFrameInputNode{node},
		fields: map[string][]string{
			"ID": nil, "UpdatedAt": nil, "Version": nil, "Title": nil,
			"Team": {"ID"}, "State": {"ID"}, "Priority": nil, "Estimate": nil,
			"Assignee": {"ID"}, "Delegate": {"ID"}, "Project": {"ID"},
			"ProjectMilestoneID": nil, "CycleID": nil, "DueDate": nil,
			"SLABreachesAt": nil, "SLAType": nil, "Labels": nil,
			"SubscriberIDs": nil, "ArchivedAt": nil, "ParentID": nil,
			"SortOrder": nil, "Icon": nil, "Recurrence": nil,
			"NextOccurrenceAt": nil, "SnoozedUntil": nil,
		},
	}, 0)
	if err != nil {
		return domain.Issue{}, err
	}
	value, err := owner.node(ctx, projected, false)
	if err != nil {
		return domain.Issue{}, err
	}
	issue := value.Interface().(domain.Issue)
	normalizeIssueRecord(&issue)
	return issue, nil
}

func legacyIssueFrameDescriptionEqual(ctx context.Context, frames *metadataLogicalBuilder, before, next metadataLogicalNode) (bool, error) {
	if before.typ == nil {
		var err error
		before, err = frames.writeValue(reflect.Zero(reflect.TypeFor[domain.Issue]()), 0, false)
		if err != nil {
			return false, err
		}
	}
	for _, name := range []string{"Description", "DescriptionState", "DocumentContent"} {
		left, err := issueHotField(ctx, before, name)
		if err != nil {
			return false, err
		}
		right, err := issueHotField(ctx, next, name)
		if err != nil {
			return false, err
		}
		// CAS uses reflect.DeepEqual, including ignored native fields, typed
		// interface values, nil/empty maps and native time locations.
		same, err := legacyMetadataWriteEqualMode(ctx, left, right, 0, true)
		if err != nil || !same {
			return false, err
		}
	}
	return true, nil
}

// Hash the same normalized stored bytes as issueRecordValues, without asking
// the native generic visitor to materialize all editor bodies first.
func (b *legacyRecordBase) fingerprintLoadedIssueFrames(ctx context.Context, source *metadataLogicalSource) error {
	index, _, _ := metadataLogicalField("issues")
	return source.fields[index].Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
		owner := &ResourceRead{store: b.store}
		defer owner.Close()
		if err := owner.reserve(ctx, logicalJSONStreamWork); err != nil {
			return err
		}
		frames, err := newMetadataLogicalBuilder(ctx)
		if err != nil {
			return err
		}
		defer frames.Close()
		id, err := owner.projectNode(ctx, node, "ID")
		if err != nil {
			return err
		}
		spool, err := legacyIssueStoredJSON(ctx, owner, frames, node)
		if err != nil {
			return err
		}
		defer spool.Close()
		hash := sha256.New()
		if _, err := io.CopyBuffer(hash, io.NewSectionReader(spool, 0, spool.bytes), make([]byte, metadataRecordChunkBytes)); err != nil {
			return err
		}
		idValue := id.Interface().(domain.Issue).ID
		_, err = b.stage.exec(ctx, int64(len(idValue)+256),
			`UPDATE legacy_base SET loaded=? WHERE kind=? AND resource=? AND id=?`,
			hash.Sum(nil), "issue", []byte{}, []byte(idValue))
		return err
	})
}
