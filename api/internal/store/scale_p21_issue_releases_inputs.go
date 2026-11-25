package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// issueReleaseInputs owns the release occurrences the literal issue-release
// callback can read or edit: every occurrence whose ID is requested (trimmed
// as normalizedStrings does) or whose IssueIDs contain the issue, in their
// original order with their native ordinals, plus every pipeline occurrence
// those releases name (ID and FrozenStages only). First-match lookups by ID
// over this subset equal the same lookups over the whole collection.
type issueReleaseInputs struct {
	read       *issueHotInputs
	data       *domain.Bootstrap
	ordinals   []int
	releases   []domain.Release
	pipelines  []domain.ReleasePipeline
	activities map[string][]domain.ActivityEvent
}

func (s *SQLiteStore) loadIssueReleaseInputs(ctx context.Context, source *metadataLogicalSource, data *domain.Bootstrap, issueID string, requested []string) (_ *issueReleaseInputs, err error) {
	if source == nil || data == nil || issueID == "" || len(data.Releases) != 0 || len(data.ReleasePipelines) != 0 {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := source.Retain(); err != nil {
		return nil, err
	}
	read := &issueHotInputs{store: s, source: source}
	r := &issueReleaseInputs{read: read, data: data}
	defer func() {
		if err != nil {
			r.close()
		}
	}()
	keys, err := read.keys(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range requested {
		if id = strings.TrimSpace(id); id != "" {
			if err := read.addKey(ctx, keys, id); err != nil {
				return nil, err
			}
		}
	}
	var releases []domain.Release
	index, _, _ := metadataLogicalField("releases")
	err = read.source.fields[index].Children(ctx, func(ordinal reflect.Value, record metadataLogicalNode) error {
		selected, err := issueHotMatches(ctx, record, "ID", keys)
		if err != nil {
			return err
		}
		if !selected {
			ids, err := issueHotField(ctx, record, "IssueIDs")
			if err != nil {
				return err
			}
			err = ids.Children(ctx, func(_ reflect.Value, value metadataLogicalNode) error {
				match, err := value.StringEqual(ctx, issueID)
				if err != nil {
					return err
				}
				if match {
					selected = true
					return ErrMetadataIterationDone
				}
				return nil
			})
			if err != nil && !errors.Is(err, ErrMetadataIterationDone) {
				return err
			}
		}
		if !selected {
			return nil
		}
		var value reflect.Value
		if record.cost > largeRowNodeCost {
			// A large release (a long description, many linked issues)
			// is decoded member by member at its native cost. The
			// validation snapshot and the callback's IssueIDs edit copy
			// its containers, never its strings' bytes (largeRowCloneBytes).
			owner := &ResourceRead{store: s}
			read.release = append(read.release, owner.Close)
			if value, err = largeRowRecordValue(ctx, owner, record); err != nil {
				return err
			}
			copies, err := largeRowCloneBytes(ctx, value, 0)
			if err != nil {
				return err
			}
			if copies > (metadataWorkBytes-(64<<10))/2 {
				return ErrMetadataWorkBudget
			}
			if err := owner.reserve(ctx, copies*2+64<<10); err != nil {
				return err
			}
		} else {
			cost, err := issueHotProjectionCost(ctx, record, nil)
			if err != nil {
				return err
			}
			if err := read.reserve(ctx, cost); err != nil {
				return err
			}
			if value, err = issueHotProjection(ctx, record, nil, cost); err != nil {
				return err
			}
		}
		releases = append(releases, value.Interface().(domain.Release))
		r.ordinals = append(r.ordinals, int(ordinal.Int()))
		return nil
	})
	if err != nil {
		return nil, err
	}
	pipelines, err := read.keys(ctx)
	if err != nil {
		return nil, err
	}
	for _, release := range releases {
		if err := read.addKey(ctx, pipelines, release.PipelineID); err != nil {
			return nil, err
		}
	}
	if err := read.selectSlice(ctx, "releasePipelines", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "ID", pipelines)
	}, "ID", "FrozenStages"); err != nil {
		return nil, err
	}
	// The callback and the validation snapshot each hold the selected
	// records; the projection reservation above covers both copies.
	r.releases = cloneSnapshotValue(reflect.ValueOf(releases)).Interface().([]domain.Release)
	r.pipelines = cloneSnapshotValue(reflect.ValueOf(read.data.ReleasePipelines)).Interface().([]domain.ReleasePipeline)
	activities := map[string][]domain.ActivityEvent{}
	for key, items := range data.Activities {
		activities[key] = slices.Clone(items)
	}
	r.activities = activities
	data.Releases, data.ReleasePipelines = releases, read.data.ReleasePipelines
	return r, ctx.Err()
}

func (r *issueReleaseInputs) close() {
	if r != nil && r.read != nil {
		r.read.close()
		r.read, r.data, r.ordinals, r.releases, r.pipelines, r.activities = nil, nil, nil, nil, nil, nil
	}
}

// detach validates the release part of the callback result: only IssueIDs
// and UpdatedAt of the selected occurrences change, pipelines stay intact, and
// the activities gain exactly one issue.releases_updated entry on the issue
// while every earlier entry is kept. It returns the edited occurrences by
// native ordinal and clears the selected collections from the callback data.
func (r *issueReleaseInputs) detach(ctx context.Context, issueID string) (map[int]domain.Release, error) {
	if r == nil || r.read == nil || r.data == nil {
		return nil, errMetadataLogicalClosed
	}
	next := r.data
	if len(r.releases) != len(next.Releases) || !reflect.DeepEqual(r.pipelines, next.ReleasePipelines) {
		return nil, ErrBoundedMetadataCoverage
	}
	edits := map[int]domain.Release{}
	for index, before := range r.releases {
		after := next.Releases[index]
		allowed := before
		allowed.IssueIDs, allowed.UpdatedAt = after.IssueIDs, after.UpdatedAt
		if !reflect.DeepEqual(allowed, after) {
			return nil, ErrBoundedMetadataCoverage
		}
		if !reflect.DeepEqual(before, after) {
			edits[r.ordinals[index]] = after
		}
	}
	added := 0
	for key := range r.activities {
		if _, present := next.Activities[key]; !present {
			return nil, ErrBoundedMetadataCoverage
		}
	}
	for key, current := range next.Activities {
		previous := r.activities[key]
		if len(current) < len(previous) || len(previous) != 0 && !reflect.DeepEqual(previous, current[:len(previous)]) {
			return nil, ErrBoundedMetadataCoverage
		}
		for _, activity := range current[len(previous):] {
			if key != issueID || activity.Type != "issue.releases_updated" {
				return nil, ErrBoundedMetadataCoverage
			}
			added++
		}
	}
	if len(edits) == 0 || added != 1 {
		return nil, ErrBoundedMetadataCoverage
	}
	next.Releases, next.ReleasePipelines = nil, nil
	return edits, ctx.Err()
}

// issueReleasePatch replaces the edited occurrences of the installed release
// collection by ordinal; every other occurrence and field keeps its stored node.
func issueReleasePatch(ctx context.Context, source *metadataLogicalSource, inputs *issueReleaseInputs, edits map[int]domain.Release) (*metadataLogicalSource, error) {
	if inputs == nil || inputs.read == nil {
		return nil, errMetadataLogicalClosed
	}
	index, _, _ := metadataLogicalField("releases")
	base := source.fields[index]
	frames, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, source))
	if err != nil {
		return nil, err
	}
	defer frames.Close()
	for _, storage := range source.stores {
		if err := frames.inheritLocations(storage); err != nil {
			return nil, err
		}
	}
	if err := frames.inheritTimestampInputs(ctx); err != nil {
		return nil, err
	}
	nilValue, err := base.isNil(ctx)
	if err != nil {
		return nil, err
	}
	shape, err := legacyDiscussionShape(frames, base.typ, nilValue)
	if err != nil {
		return nil, err
	}
	patch := legacyDiscussionSlicePatch{base: base, shape: shape, replacements: map[int]metadataLogicalNode{}}
	seen := 0
	if err := base.Children(ctx, func(key reflect.Value, record metadataLogicalNode) error {
		ordinal := int(key.Int())
		edit, selected := edits[ordinal]
		if !selected {
			return nil
		}
		changed := map[string]reflect.Value{
			"IssueIDs":  reflect.ValueOf(edit.IssueIDs),
			"UpdatedAt": reflect.ValueOf(edit.UpdatedAt),
		}
		if record.cost > largeRowNodeCost {
			// A large release's edited members are already admitted with
			// the decoded inputs; the builder streams them to its spool, so
			// only their containers' size is admitted as frame scratch.
			for _, native := range changed {
				size, err := largeRowCloneBytes(ctx, native, 0)
				if err != nil {
					return err
				}
				if size > (metadataWorkBytes-(64<<10))/2 {
					return ErrMetadataWorkBudget
				}
				if err := inputs.read.reserve(ctx, size+64<<10); err != nil {
					return err
				}
			}
		} else {
			for _, native := range changed {
				cost, err := metadataAggregateNativeCost(ctx, native, 0)
				if err != nil {
					return err
				}
				if cost > (metadataWorkBytes-4096)/8 {
					return ErrMetadataWorkBudget
				}
				if err := inputs.read.reserve(ctx, cost*8+4096); err != nil {
					return err
				}
			}
		}
		value, err := frames.writeAggregateNode(legacyDiscussionNativeDelta{base: record, changed: changed}, 0)
		if err != nil {
			return err
		}
		if value.blocked || value.unavailable {
			return ErrBoundedMetadataCoverage
		}
		patch.replacements[ordinal] = value
		seen++
		return nil
	}); err != nil {
		return nil, err
	}
	if seen != len(edits) {
		return nil, ErrBoundedMetadataCoverage
	}
	return source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if name == "releases" {
			return patch, true, nil
		}
		return metadataAggregateStoredNode{value: node}, false, nil
	})
}
