package store

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"

	"flow/api/internal/domain"
)

// This is the installed-source counterpart of refreshIssueReferences. Lookup
// retains the last duplicate directory ID (metadataLogicalReferenceDirectory).
func refreshMetadataLogicalIssueReferences(ctx context.Context, source *metadataLogicalSource) (*metadataLogicalSource, error) {
	directory := &metadataLogicalReferenceDirectory{source: source, fields: map[string]*metadataLogicalReferenceField{}}
	defer directory.release()
	ctx = context.WithValue(ctx, metadataLogicalReferenceDirectoryKey{}, directory)
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
	if directory, ok := ctx.Value(metadataLogicalReferenceDirectoryKey{}).(*metadataLogicalReferenceDirectory); ok && directory.source == source {
		found, indexed, err := directory.lookup(ctx, field, id)
		if err != nil {
			return original, err
		}
		if indexed {
			if found == nil {
				return original, nil
			}
			if summary {
				found = metadataLogicalProjectSummary{found}
			}
			if original.nodeType().Kind() == reflect.Pointer {
				return metadataAggregatePointerNode{original.nodeType(), found}, nil
			}
			return found, nil
		}
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

// One reference refresh resolves every issue's team, state, users, project
// and labels against the directories of the same immutable source. Scanning a
// directory per reference read every project once per issue. The refresh
// indexes each directory's string IDs once (the last duplicate wins, as in
// the scan), within a global budget; past it a reference is scanned.
type metadataLogicalReferenceDirectoryKey struct{}

const metadataLogicalReferenceLimit = 32 << 20

var metadataLogicalReferenceBytes atomic.Int64

type metadataLogicalReferenceDirectory struct {
	source   *metadataLogicalSource
	fields   map[string]*metadataLogicalReferenceField
	reserved int64
}

type metadataLogicalReferenceField struct {
	ids     map[string]metadataLogicalNode
	indexed bool
}

func (d *metadataLogicalReferenceDirectory) release() {
	metadataLogicalReferenceBytes.Add(-d.reserved)
	d.reserved, d.fields = 0, nil
}

func (d *metadataLogicalReferenceDirectory) reserve(size int64) bool {
	if !reserveMetadataLogicalBytes(&metadataLogicalReferenceBytes, size, metadataLogicalReferenceLimit) {
		return false
	}
	d.reserved += size
	return true
}

// lookup reports the last record of field whose ID equals id. indexed is
// false when the caller must scan instead.
func (d *metadataLogicalReferenceDirectory) lookup(ctx context.Context, field string, id metadataAggregateNode) (metadataAggregateNode, bool, error) {
	var want string
	switch value := id.(type) {
	case metadataAggregateNativeNode:
		if value.nodeType().Kind() != reflect.String {
			return nil, false, nil
		}
		want = value.value.String()
	case metadataAggregateStoredNode:
		if value.nodeType().Kind() != reflect.String {
			return nil, false, nil
		}
		text, ok, err := metadataLogicalReferenceID(ctx, value.value)
		if err != nil || !ok {
			return nil, false, err
		}
		want = text
	default:
		return nil, false, nil
	}
	entry, err := d.field(ctx, field)
	if err != nil || !entry.indexed {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	node, found := entry.ids[want]
	if !found {
		return nil, true, nil
	}
	return metadataAggregateStoredNode{node}, true, nil
}

func (d *metadataLogicalReferenceDirectory) field(ctx context.Context, field string) (*metadataLogicalReferenceField, error) {
	if entry, found := d.fields[field]; found {
		return entry, nil
	}
	entry := &metadataLogicalReferenceField{ids: map[string]metadataLogicalNode{}, indexed: true}
	var reserved int64
	err := d.source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		if name != field {
			return nil
		}
		return node.Children(ctx, func(_ reflect.Value, item metadataLogicalNode) error {
			candidateID, err := metadataAggregateChild(ctx, metadataAggregateStoredNode{item}, "ID")
			if err != nil {
				return err
			}
			stored, ok := candidateID.(metadataAggregateStoredNode)
			if candidateID == nil || candidateID.nodeType().Kind() != reflect.String {
				return nil
			}
			if !ok {
				return errMetadataAggregateStop
			}
			text, ok, err := metadataLogicalReferenceID(ctx, stored.value)
			if err != nil {
				return err
			}
			if !ok {
				return errMetadataAggregateStop
			}
			if _, found := entry.ids[text]; !found {
				if !d.reserve(int64(len(text)) + 128) {
					return errMetadataAggregateStop
				}
				reserved += int64(len(text)) + 128
			}
			entry.ids[text] = item
			return nil
		})
	})
	if errors.Is(err, errMetadataAggregateStop) {
		metadataLogicalReferenceBytes.Add(-reserved)
		d.reserved -= reserved
		entry = &metadataLogicalReferenceField{}
		err = nil
	}
	if err != nil {
		metadataLogicalReferenceBytes.Add(-reserved)
		d.reserved -= reserved
		return nil, err
	}
	d.fields[field] = entry
	return entry, nil
}

// metadataLogicalReferenceID reads a stored ID string of bounded size.
func metadataLogicalReferenceID(ctx context.Context, node metadataLogicalNode) (string, bool, error) {
	if node.generic || node.blocked || node.unavailable {
		return "", false, nil
	}
	value, err := node.Value(ctx, 64<<10)
	if errors.Is(err, ErrMetadataWorkBudget) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value.String(), true, nil
}
