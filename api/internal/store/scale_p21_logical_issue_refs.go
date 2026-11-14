package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

// This is the installed-source counterpart of refreshIssueReferences. Lookup
// retains the last duplicate directory ID, without constructing directory maps.
func refreshMetadataLogicalIssueReferences(ctx context.Context, source *metadataLogicalSource) (*metadataLogicalSource, error) {
	return source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		base := metadataAggregateStoredNode{node}
		if name == "issues" {
			return metadataLogicalIssueCollection{source, base, false}, true, nil
		}
		return base, false, nil
	})
}

type metadataLogicalIssueCollection struct {
	source *metadataLogicalSource
	base   metadataAggregateNode
	labels bool
}

func (n metadataLogicalIssueCollection) nodeType() reflect.Type { return n.base.nodeType() }
func (n metadataLogicalIssueCollection) logicalBase() metadataLogicalNode {
	return n.base.(metadataAggregateStoredNode).value
}
func (n metadataLogicalIssueCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n metadataLogicalIssueCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
		if n.labels {
			replacement, err := metadataLogicalResolveReference(ctx, n.source, "labels", child, false)
			if err != nil {
				return err
			}
			return visit(key, replacement)
		}
		return visit(key, metadataLogicalIssueRecord{n.source, child})
	})
}

type metadataLogicalIssueRecord struct {
	source *metadataLogicalSource
	base   metadataAggregateNode
}

func (n metadataLogicalIssueRecord) nodeType() reflect.Type { return n.base.nodeType() }
func (n metadataLogicalIssueRecord) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n metadataLogicalIssueRecord) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.nodeChildren(ctx, func(key reflect.Value, child metadataAggregateNode) error {
		field, summary := "", false
		switch key.String() {
		case "Team":
			field = "teams"
		case "State":
			field = "states"
		case "Creator", "Assignee", "Delegate":
			field = "users"
		case "Project":
			field, summary = "projects", true
		case "Labels":
			return visit(key, metadataLogicalIssueCollection{n.source, child, true})
		}
		if field != "" {
			replacement, err := metadataLogicalResolveReference(ctx, n.source, field, child, summary)
			if err != nil {
				return err
			}
			child = replacement
		}
		return visit(key, child)
	})
}

func metadataLogicalResolveReference(ctx context.Context, source *metadataLogicalSource, field string, original metadataAggregateNode, summary bool) (metadataAggregateNode, error) {
	target, err := metadataAggregateUnwrap(ctx, original, true)
	if err != nil || target == nil {
		return original, err
	}
	id, err := metadataAggregateChild(ctx, target, "ID")
	if err != nil || id == nil {
		return original, err
	}
	var found metadataAggregateNode
	err = source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		if name != field {
			return nil
		}
		return node.Children(ctx, func(_ reflect.Value, item metadataLogicalNode) error {
			candidate := metadataAggregateStoredNode{item}
			candidateID, err := metadataAggregateChild(ctx, candidate, "ID")
			if err != nil {
				return err
			}
			match, err := metadataAggregateStringsEqual(ctx, id, candidateID)
			if match {
				found = candidate
			}
			return err
		})
	})
	if err != nil || found == nil {
		return original, err
	}
	if summary {
		found = metadataLogicalProjectSummary{found}
	}
	if original.nodeType().Kind() == reflect.Pointer {
		return metadataAggregatePointerNode{original.nodeType(), found}, nil
	}
	return found, nil
}

type metadataLogicalProjectSummary struct{ project metadataAggregateNode }

func (n metadataLogicalProjectSummary) nodeType() reflect.Type {
	return reflect.TypeFor[domain.ProjectSummary]()
}
func (n metadataLogicalProjectSummary) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n metadataLogicalProjectSummary) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	typ := n.nodeType()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		child, err := metadataAggregateChild(ctx, n.project, field.Name)
		if err != nil {
			return err
		}
		if child == nil || child.nodeType() != field.Type {
			return ErrMetadataRecordQuery
		}
		if err := visit(reflect.ValueOf(field.Name), child); err != nil {
			return err
		}
	}
	return nil
}
