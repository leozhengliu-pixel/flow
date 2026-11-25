package store

import (
	"bufio"
	"context"
	"io"
	"reflect"
	"sync"
	"unicode"

	"flow/api/internal/domain"
)

type legacyIssueStoredRecordNode struct{ base metadataLogicalNode }

func (n legacyIssueStoredRecordNode) nodeType() reflect.Type { return n.base.typ }
func (n legacyIssueStoredRecordNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyIssueStoredRecordNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
		var child metadataAggregateNode = metadataAggregateStoredNode{value}
		switch key.String() {
		case "AgentSessionState":
			child = metadataAggregateNativeNode{reflect.Zero(value.typ)}
		case "Version":
			version, err := value.Value(ctx, 4096)
			if err != nil {
				return err
			}
			if version.IsZero() {
				version = reflect.New(value.typ).Elem()
				version.SetInt(1)
				child = metadataAggregateNativeNode{version}
			}
		case "Reactions", "Labels", "SubscriberIDs", "SubIssueIDs", "Relations", "Attachments":
			absent, err := value.isNil(ctx)
			if err != nil {
				return err
			}
			if absent {
				var empty reflect.Value
				if value.typ.Kind() == reflect.Map {
					empty = reflect.MakeMap(value.typ)
				} else {
					empty = reflect.MakeSlice(value.typ, 0, 0)
				}
				child = metadataAggregateNativeNode{empty}
			} else if key.String() == "Labels" {
				child = legacyIssueStoredLabelsNode{value}
			}
		}
		return visit(key, child)
	})
}

type legacyIssueStoredLabelsNode struct{ base metadataLogicalNode }

func (n legacyIssueStoredLabelsNode) nodeType() reflect.Type           { return n.base.typ }
func (n legacyIssueStoredLabelsNode) logicalBase() metadataLogicalNode { return n.base }
func (n legacyIssueStoredLabelsNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyIssueStoredLabelsNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.Children(ctx, func(key reflect.Value, label metadataLogicalNode) error {
		return visit(key, legacyIssueStoredLabelNode{label})
	})
}

type legacyIssueStoredLabelNode struct{ base metadataLogicalNode }

func (n legacyIssueStoredLabelNode) nodeType() reflect.Type { return n.base.typ }
func (n legacyIssueStoredLabelNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyIssueStoredLabelNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
		var child metadataAggregateNode = metadataAggregateStoredNode{value}
		if key.String() == "IssueCount" {
			child = metadataAggregateNativeNode{reflect.Zero(value.typ)}
		}
		return visit(key, child)
	})
}

// Only SQL scalar columns enter a native Issue. Nested display fields and all
// body, membership and reaction values stay in their original typed frames.
type legacyIssueColumnHeaderNode struct {
	base   metadataAggregateNode
	fields map[string][]string
}

func (n legacyIssueColumnHeaderNode) nodeType() reflect.Type { return n.base.nodeType() }
func (n legacyIssueColumnHeaderNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyIssueColumnHeaderNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
		if n.nodeType().Kind() == reflect.Pointer {
			return visit(key, legacyIssueColumnHeaderNode{child, n.fields})
		}
		nested, keep := n.fields[key.String()]
		if !keep {
			child = metadataAggregateNativeNode{reflect.Zero(child.nodeType())}
		} else if nested != nil {
			fields := make(map[string][]string, len(nested))
			for _, name := range nested {
				fields[name] = nil
			}
			child = legacyIssueColumnHeaderNode{child, fields}
		}
		return visit(key, child)
	})
}

type legacyIssueListRecordNode struct {
	base          metadataLogicalNode
	hasContent    bool
	reactions     metadataLogicalNode
	reactionCount int
}

func (n legacyIssueListRecordNode) nodeType() reflect.Type { return n.base.typ }
func (n legacyIssueListRecordNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyIssueListRecordNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
		var child metadataAggregateNode = metadataAggregateStoredNode{value}
		switch key.String() {
		case "HasContent":
			child = metadataAggregateNativeNode{reflect.ValueOf(n.hasContent)}
		case "IsSummary":
			child = metadataAggregateNativeNode{reflect.ValueOf(true)}
		case "Description", "DescriptionState", "DocumentContent":
			child = metadataAggregateNativeNode{reflect.Zero(value.typ)}
		case "Reactions":
			child = metadataAggregateNativeNode{reflect.MakeMap(value.typ)}
		case "ReactionCounts":
			if n.reactionCount > 0 {
				child = legacyIssueReactionCountsNode{n.reactions, value.typ}
			}
		}
		return visit(key, child)
	})
}

type legacyIssueReactionCountsNode struct {
	base metadataLogicalNode
	typ  reflect.Type
}

func (n legacyIssueReactionCountsNode) nodeType() reflect.Type           { return n.typ }
func (n legacyIssueReactionCountsNode) logicalBase() metadataLogicalNode { return n.base }
func (n legacyIssueReactionCountsNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyIssueReactionCountsNode) logicalMapEntries(ctx context.Context, visit func(metadataAggregateNode, metadataAggregateNode) error) error {
	return n.base.MapEntries(ctx, func(key, users metadataLogicalNode) error {
		count := 0
		if err := users.Children(ctx, func(_ reflect.Value, _ metadataLogicalNode) error {
			count++
			return nil
		}); err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
		return visit(metadataAggregateStoredNode{key}, metadataAggregateNativeNode{reflect.ValueOf(count)})
	})
}
func (n legacyIssueReactionCountsNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.logicalMapEntries(ctx, func(key, count metadataAggregateNode) error {
		value, err := key.nodeValue(ctx, 256<<10)
		if err != nil {
			return err
		}
		return visit(value, count)
	})
}

func legacyIssueFrameHasText(ctx context.Context, node metadataLogicalNode) (bool, error) {
	found := false
	err := node.StreamBytes(ctx, func(input io.Reader) error {
		reader := bufio.NewReaderSize(input, metadataRecordChunkBytes)
		for {
			r, _, err := reader.ReadRune()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if !unicode.IsSpace(r) {
				found = true
				return nil
			}
		}
	})
	return found, err
}

// legacyIssueRecordFrameValues mirrors issueRecordColumns with streamed detail
// and list encoders. The returned lease owns every SQL argument until release.
func legacyIssueRecordFrameValues(ctx context.Context, s *SQLiteStore, frames *metadataLogicalBuilder, node metadataLogicalNode, workspace string) (result []any, release func(), err error) {
	if s == nil || frames == nil || node.typ != reflect.TypeFor[domain.Issue]() {
		return nil, nil, ErrMetadataRecordQuery
	}
	owner := &ResourceRead{store: s}
	var releases []func()
	var once sync.Once
	release = func() {
		once.Do(func() {
			for _, close := range releases {
				close()
			}
			releases = nil
			owner.Close()
		})
	}
	defer func() {
		if err != nil {
			release()
			result = nil
			release = nil
		}
	}()
	if err := owner.reserve(ctx, logicalJSONStreamWork); err != nil {
		return nil, release, err
	}
	stored, err := frames.writeAggregateNode(legacyIssueStoredRecordNode{node}, 0)
	if err != nil {
		return nil, release, err
	}
	header, err := frames.writeAggregateNode(legacyIssueColumnHeaderNode{
		base: metadataAggregateStoredNode{stored},
		fields: map[string][]string{
			"ID": nil, "Identifier": nil, "Team": {"ID"}, "State": {"ID", "Type"},
			"Project": {"ID"}, "Assignee": {"ID"}, "Creator": {"ID"},
			"CycleID": nil, "ParentID": nil, "Priority": nil, "SortOrder": nil,
			"Title": nil, "CreatedAt": nil, "UpdatedAt": nil, "ArchivedAt": nil, "Version": nil,
		},
	}, 0)
	if err != nil {
		return nil, release, err
	}
	native, err := owner.node(ctx, header, false)
	if err != nil {
		return nil, release, err
	}
	values, err := s.issueRecordColumns(workspace, native.Interface().(domain.Issue))
	if err != nil {
		return nil, release, err
	}
	has, err := owner.projectNode(ctx, stored, "HasContent")
	if err != nil {
		return nil, release, err
	}
	hasContent := has.Interface().(domain.Issue).HasContent
	description, err := issueHotField(ctx, stored, "Description")
	if err != nil {
		return nil, release, err
	}
	if !hasContent {
		hasContent, err = legacyIssueFrameHasText(ctx, description)
		if err != nil {
			return nil, release, err
		}
	}
	document, err := issueHotField(ctx, stored, "DocumentContent")
	if err != nil {
		return nil, release, err
	}
	if !hasContent {
		err = document.Children(ctx, func(_ reflect.Value, content metadataLogicalNode) error {
			body, err := issueHotField(ctx, content, "Content")
			if err != nil {
				return err
			}
			hasContent, err = legacyIssueFrameHasText(ctx, body)
			return err
		})
		if err != nil {
			return nil, release, err
		}
	}
	reactions, err := issueHotField(ctx, stored, "Reactions")
	if err != nil {
		return nil, release, err
	}
	reactionCount := 0
	if err := reactions.MapEntries(ctx, func(_, _ metadataLogicalNode) error {
		reactionCount++
		return nil
	}); err != nil {
		return nil, release, err
	}
	encode := func(value metadataAggregateNode) ([]byte, error) {
		raw, close, err := s.metadataAggregateEncodeOwned(ctx, value)
		if err != nil {
			return nil, err
		}
		releases = append(releases, close)
		// StripJSONNUL may allocate one output copy while raw is still live.
		if err := owner.reserve(ctx, ((uint64(len(raw))+8191)&^uint64(8191))+64); err != nil {
			return nil, err
		}
		return StripJSONNUL(raw), nil
	}
	detail, err := encode(metadataAggregateStoredNode{stored})
	if err != nil {
		return nil, release, err
	}
	list, err := encode(legacyIssueListRecordNode{stored, hasContent, reactions, reactionCount})
	if err != nil {
		return nil, release, err
	}
	values[len(values)-2], values[len(values)-1] = list, detail
	return values, release, ctx.Err()
}
