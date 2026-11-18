package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"time"

	"flow/api/internal/domain"
)

type legacyMetadataWriteRow struct {
	id, parent          string
	before, next        metadataLogicalNode
	oldOrdinal, ordinal int
	changed, deleted    bool
	forced              bool
}

type legacyMetadataWriteField struct {
	field                    metadataField
	before, next             metadataLogicalNode
	rows                     []legacyMetadataWriteRow
	oldOrder, order          []int
	parents                  []string
	parentNil                map[string]bool
	parentBefore, parentNext map[string]metadataLogicalNode
	full, changed, persist   bool
	inlineReady              bool
	scheduleParents          *legacyScheduleParentMap
	// reorder takes writeArrayReorder (legacyMetadataStreamedOrder).
	reorder bool
}

type legacyMetadataWritePlan struct {
	store             *SQLiteStore
	tx                *sqlTx
	workspace         string
	workspaceID       string
	work              *ResourceRead
	frames            *metadataLogicalBuilder
	requested, actual *metadataLogicalSource
	fields            []*legacyMetadataWriteField
	progress          legacyMetadataProgress
	inlineRoot        *metadataInlineRoot
}

type legacyMetadataProgress interface {
	HasProject(context.Context, string) (bool, error)
	ForEachProject(context.Context, func(string) error) error
	Rebuild(context.Context, *sqlTx, string, *metadataLogicalSource, time.Time) (*metadataLogicalSource, error)
}

type legacyMetadataImportBoundary interface {
	legacyImportCompaction() bool
}

// persistLegacyMetadataImage owns the metadata phase of one literal full
// discussion mutation. Its caller owns the transaction and publication.
func (s *SQLiteStore) persistLegacyMetadataImage(ctx context.Context, tx *sqlTx, workspace string, before, target *metadataLogicalSource) (_ *metadataLogicalSource, err error) {
	return s.persistLegacyMetadataImageWithProgress(ctx, tx, workspace, before, target, nil)
}

func (s *SQLiteStore) persistLegacyMetadataImageWithProgress(ctx context.Context, tx *sqlTx, workspace string, before, target *metadataLogicalSource, progress legacyMetadataProgress) (_ *metadataLogicalSource, err error) {
	if s == nil || !s.BoundedMetadataEnabled() || tx == nil || tx.database != s.db || tx.dialect != s.dialect || before == nil || target == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	for _, source := range []*metadataLogicalSource{before, target} {
		if err := source.Retain(); err != nil {
			return nil, err
		}
		defer source.Release()
	}
	ctx = metadataLogicalSourceScratchContext(metadataLogicalUnpinnedContext(ctx), target)
	work := &ResourceRead{store: s}
	defer work.Close()
	if uint64(len(workspace)) > (metadataWorkBytes-(256<<10))/16 {
		return nil, ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, uint64(len(workspace))*16+256<<10); err != nil {
		return nil, err
	}
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return nil, err
	}
	for _, source := range []*metadataLogicalSource{before, target} {
		if stamp.workspaceID == "" || source.Stamp().workspaceID != stamp.workspaceID {
			return nil, ErrMetadataRecordQuery
		}
		index, _, _ := metadataLogicalField("workspace")
		id, err := issueHotField(ctx, source.fields[index], "ID")
		if err != nil {
			return nil, err
		}
		same, err := id.StringEqual(ctx, stamp.workspaceID)
		if err != nil {
			return nil, err
		}
		if !same {
			return nil, ErrMetadataRecordQuery
		}
	}
	frames, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer frames.Close()
	for _, source := range []*metadataLogicalSource{before, target} {
		for _, storage := range source.stores {
			if err := frames.inheritLocations(storage); err != nil {
				return nil, err
			}
		}
	}
	plan := &legacyMetadataWritePlan{store: s, tx: tx, workspace: workspace, workspaceID: stamp.workspaceID, work: work, frames: frames, requested: target, progress: progress}
	defer func() {
		if plan.inlineRoot != nil {
			plan.inlineRoot.Close()
		}
		for _, field := range plan.fields {
			if field.scheduleParents != nil {
				field.scheduleParents.Close()
			}
		}
	}()
	allowed := map[string]bool{"viewer": true, "users": true, "members": true, "projects": true, "initiatives": true, "labels": true, "projectUpdates": true, "initiativeUpdates": true, "threadSubscriptions": true}
	if boundary, ok := progress.(legacyMetadataImportBoundary); ok && boundary.legacyImportCompaction() {
		allowed["importJobs"], allowed["migrationJobs"] = true, true
	}
	// Writers that own additional selected collections declare them; the
	// existing callers declare none.
	for _, name := range legacyMetadataExtraFields(ctx) {
		allowed[name] = true
	}
	for _, field := range persistedMetadataFields() {
		a, b := before.fields[field.index], target.fields[field.index]
		if a == b && (progress == nil || field.name != "projects") {
			continue
		}
		f := &legacyMetadataWriteField{field: field, before: a, next: b}
		if !allowed[field.name] {
			same, err := plan.equal(ctx, a, b)
			if err != nil {
				return nil, err
			}
			if !same {
				return nil, fmt.Errorf("%w: legacy discussion changed %s", ErrBoundedMetadataCoverage, field.name)
			}
			continue
		}
		if err := plan.headers(ctx, f); err != nil {
			return nil, err
		}
		if f.changed || progress != nil && field.name == "projects" {
			plan.fields = append(plan.fields, f)
		}
	}
	// Rebase the union in the original field/key order before any metadata
	// row or root write. The callback-final directory is immutable here.
	sorted := append([]*legacyMetadataWriteField(nil), plan.fields...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].field.name < sorted[j].field.name })
	var conflicts []metadataRecordKey
	for _, field := range sorted {
		err := plan.rebase(ctx, field)
		if keys, ok := conflictedMetadataKeys(err); ok {
			conflicts = append(conflicts, keys...)
		} else if err != nil {
			return nil, err
		}
	}
	if len(conflicts) > 0 {
		return nil, &metadataConflictError{keys: conflicts}
	}
	if progress != nil {
		rebased, err := plan.postimage(ctx, target)
		if err != nil {
			return nil, err
		}
		defer rebased.Release()
		rebuilt, err := progress.Rebuild(ctx, tx, workspace, rebased, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		defer rebuilt.Release()
		for index, field := range plan.fields {
			if field.field.name != "projects" {
				continue
			}
			canonicalBefore := field.before
			if !field.full {
				canonicalBefore, err = frames.writeAggregateNode(legacyMetadataWriteCollection{field: field}, 0)
				if err != nil {
					return nil, err
				}
			}
			replacement := &legacyMetadataWriteField{
				field: field.field, before: canonicalBefore, next: rebuilt.fields[field.field.index],
			}
			if err := plan.headers(ctx, replacement); err != nil {
				return nil, err
			}
			plan.fields[index] = replacement
			break
		}
		target = rebuilt
		plan.requested = rebuilt
	}
	// Canonical deletions/merges can make a previously changed field equal
	// on both sides. The native postimage still needs those changes, while
	// the original delta omits that field from SQL persistence.
	for _, field := range plan.fields {
		field.persist, err = plan.changedAfterRebase(ctx, field)
		if err != nil {
			return nil, err
		}
	}
	actual, err := plan.postimage(ctx, target)
	if err != nil {
		return nil, err
	}
	defer actual.Release()
	plan.actual = actual
	shapes, err := plan.rootShapes(ctx)
	if err != nil {
		return nil, err
	}
	if plan.inlineRoot != nil {
		// The remainder writes canonical rows, then one slow root patch.
		// Borrow the locked shape snapshot only until that root write.
		ctx = withMetadataInlineRootCapture(ctx, plan.inlineRoot)
	}
	// Decide and admit every fallback before the first fast-path write.
	var slowNames []string
	var sessionComplete []*legacyMetadataWriteField
	var inlineComplete []*legacyMetadataWriteField
	var parentMaps []*legacyMetadataWriteField
	var slowBefore, slowTarget domain.Bootstrap
	slowBefore.Workspace.ID, slowTarget.Workspace.ID = stamp.workspaceID, stamp.workspaceID
	for _, field := range plan.fields {
		if !field.persist {
			continue
		}
		if field.scheduleParents != nil {
			if shapes[field.field.name] != "map" {
				return nil, ErrConcurrentWrite
			}
			parentMaps = append(parentMaps, field)
			continue
		}
		fast := false
		if !field.full {
			if updateMetadataField(field.field.name) && shapes[field.field.name] == "updates" {
				fast, err = plan.updateFastReady(ctx, field)
			} else if shapes[field.field.name] == "array" {
				fast, err = plan.arrayFastReady(ctx, field)
			}
		}
		if err != nil {
			return nil, err
		}
		if fast {
			continue
		}
		if !field.full && shapes[field.field.name] == "array" && legacyMetadataStreamedOrder(ctx, field.field.name) && plan.arrayReorderReady(field) {
			field.reorder = true
			continue
		}
		if handled, err := plan.agentSessionLifecycleInlineReady(ctx, field, shapes); err != nil {
			return nil, err
		} else if handled {
			sessionComplete = append(sessionComplete, field)
			continue
		}
		ready := field.inlineReady
		if !ready {
			ready, err = plan.inlineFieldReady(ctx, field, shapes)
			if err != nil {
				return nil, err
			}
		}
		if ready {
			field.full = true
			inlineComplete = append(inlineComplete, field)
			continue
		}
		left, right, err := plan.materializeField(ctx, field, work)
		if err != nil {
			return nil, err
		}
		field.full = true
		reflect.ValueOf(&slowBefore).Elem().Field(field.field.index).Set(left)
		reflect.ValueOf(&slowTarget).Elem().Field(field.field.index).Set(right)
		slowNames = append(slowNames, field.field.name)
	}
	for _, field := range plan.fields {
		if field.full || !field.persist {
			continue
		}
		if field.reorder {
			err = plan.writeArrayReorder(ctx, field)
		} else if updateMetadataField(field.field.name) {
			err = plan.writeUpdates(ctx, field)
		} else {
			err = plan.writeArray(ctx, field)
		}
		if err != nil {
			return nil, err
		}
	}
	var sessionPatch *legacySessionRootPatch
	var sessionTail *legacySessionRootPatch
	for _, field := range parentMaps {
		if err := plan.writeProjectUpdateScheduleParentMap(ctx, field); err != nil {
			return nil, err
		}
	}
	for _, field := range sessionComplete {
		part, err := plan.prepareAgentSessionLifecycleRoot(ctx, field)
		if err != nil {
			return nil, err
		}
		if sessionPatch == nil {
			sessionPatch = part
			defer sessionPatch.Close()
		} else {
			sessionTail.next = part
		}
		sessionTail = part
	}
	var inlinePatch *legacyInlineFieldsPatch
	if len(inlineComplete) != 0 {
		inlinePatch, err = plan.prepareInlineFields(ctx, inlineComplete)
		if err != nil {
			return nil, err
		}
		defer inlinePatch.Close()
	}
	if len(slowNames) > 0 || sessionPatch != nil || inlinePatch != nil {
		refs, err := plan.references(ctx, actual, work, slowBefore, slowTarget, nil)
		if err != nil {
			return nil, err
		}
		writeCtx := withLegacyMetadataDeltaReadBudget(ctx, work)
		if sessionPatch != nil {
			writeCtx = context.WithValue(writeCtx, legacySessionRootPatchKey{}, sessionPatch)
		}
		if inlinePatch != nil {
			writeCtx = context.WithValue(writeCtx, legacyInlineFieldsPatchKey{}, inlinePatch)
		}
		names := slowNames
		if names == nil {
			names = []string{}
		}
		for _, field := range inlineComplete {
			names = append(names, field.field.name)
		}
		if _, err := s.writeLegacyMetadataSlowFields(writeCtx, tx, workspace, slowBefore, slowTarget, names, refs); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := actual.Retain(); err != nil {
		return nil, err
	}
	return actual, nil
}

func (p *legacyMetadataWritePlan) changedAfterRebase(ctx context.Context, field *legacyMetadataWriteField) (bool, error) {
	if field.full {
		equal, err := p.equal(ctx, field.before, field.next)
		return !equal, err
	}
	oldOrder := make([]int, 0, len(field.oldOrder))
	for _, index := range field.oldOrder {
		if !field.rows[index].deleted {
			oldOrder = append(oldOrder, index)
		}
	}
	order := make([]int, 0, len(field.order))
	for _, index := range field.order {
		if !field.rows[index].deleted {
			order = append(order, index)
		}
	}
	if len(oldOrder) != len(order) {
		return true, nil
	}
	for i, index := range oldOrder {
		if index != order[i] {
			return true, nil
		}
		row := field.rows[index]
		if row.before.typ == nil || row.next.typ == nil {
			return true, nil
		}
		equal, err := p.equal(ctx, row.before, row.next)
		if err != nil || !equal {
			return !equal, err
		}
	}
	return false, nil
}

func (p *legacyMetadataWritePlan) equal(ctx context.Context, a, b metadataLogicalNode) (bool, error) {
	if a == b {
		return true, nil
	}
	work := &ResourceRead{store: p.store}
	defer work.Close()
	if err := work.reserve(ctx, 128<<10); err != nil {
		return false, err
	}
	return legacyMetadataWriteEqual(ctx, a, b, 0)
}

// Compare the existing typed frames without decoding unchanged bodies. This
// uses the logical codec's cursor and mirrors metadataValueEqual's omission
// of transient struct fields.
func legacyMetadataWriteEqual(ctx context.Context, a, b metadataLogicalNode, depth int) (bool, error) {
	return legacyMetadataWriteEqualMode(ctx, a, b, depth, false)
}

func legacyMetadataWriteEqualMode(ctx context.Context, a, b metadataLogicalNode, depth int, strict bool) (bool, error) {
	if a == b {
		return true, nil
	}
	if a.typ != b.typ {
		return false, nil
	}
	if depth >= metadataLogicalDepth {
		return false, ErrMetadataWorkBudget
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if a.blocked || b.blocked || a.unavailable || b.unavailable {
		return false, ErrBoundedMetadataCoverage
	}
	if a.generic || b.generic {
		return metadataGenericJSONEqual(ctx, a, b)
	}
	kind := a.typ.Kind()
	if a.typ == reflect.TypeFor[time.Time]() {
		left, err := a.Value(ctx, 4096)
		if err != nil {
			return false, err
		}
		right, err := b.Value(ctx, 4096)
		if err != nil {
			return false, err
		}
		return reflect.DeepEqual(left.Interface(), right.Interface()), nil
	}
	if kind == reflect.String || kind == reflect.Slice && a.typ.Elem().Kind() == reflect.Uint8 {
		if kind == reflect.Slice {
			x, err := a.isNil(ctx)
			if err != nil {
				return false, err
			}
			y, err := b.isNil(ctx)
			if err != nil {
				return false, err
			}
			if x != y {
				return false, nil
			}
		}
		equal := true
		err := a.StreamBytes(ctx, func(left io.Reader) error {
			return b.StreamBytes(ctx, func(right io.Reader) error {
				var x, y [4096]byte
				for {
					n, errA := io.ReadFull(left, x[:])
					m, errB := io.ReadFull(right, y[:])
					if errA != nil && !errors.Is(errA, io.EOF) && !errors.Is(errA, io.ErrUnexpectedEOF) {
						return errA
					}
					if errB != nil && !errors.Is(errB, io.EOF) && !errors.Is(errB, io.ErrUnexpectedEOF) {
						return errB
					}
					if n != m || !bytes.Equal(x[:n], y[:m]) {
						equal = false
						return nil
					}
					if errA != nil || errB != nil {
						equal = (errA != nil) == (errB != nil)
						return nil
					}
				}
			})
		})
		return equal, err
	}
	left := metadataLogicalCursor{node: a, offset: a.offset + 16, ctx: ctx}
	right := metadataLogicalCursor{node: b, offset: b.offset + 16, ctx: ctx}
	count := 0
	switch kind {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map:
		x, err := left.byte()
		if err != nil {
			return false, err
		}
		y, err := right.byte()
		if err != nil {
			return false, err
		}
		if x != y {
			return false, nil
		}
		if x == 0 {
			return true, nil
		}
		if kind == reflect.Pointer || kind == reflect.Interface {
			typ := a.typ.Elem
			if kind == reflect.Interface {
				x, err := left.uint()
				if err != nil {
					return false, err
				}
				y, err := right.uint()
				if err != nil {
					return false, err
				}
				if x >= uint64(len(a.source.types)) || y >= uint64(len(b.source.types)) {
					return false, ErrMetadataRecordQuery
				}
				if a.source.types[x] != b.source.types[y] {
					return false, nil
				}
				typ = func() reflect.Type { return a.source.types[x] }
			}
			x, err := left.child(typ())
			if err != nil {
				return false, err
			}
			y, err := right.child(typ())
			if err != nil {
				return false, err
			}
			return legacyMetadataWriteEqualMode(ctx, x, y, depth+1, strict || kind == reflect.Interface)
		}
		leftCount, err := left.uint()
		if err != nil {
			return false, err
		}
		rightCount, err := right.uint()
		if err != nil {
			return false, err
		}
		if leftCount != rightCount {
			return false, nil
		}
		if leftCount > uint64(a.cost) || rightCount > uint64(b.cost) {
			return false, ErrMetadataRecordQuery
		}
		count = int(leftCount)
	case reflect.Struct:
		count = a.typ.NumField()
	case reflect.Array:
		count = a.typ.Len()
	default:
		x, err := a.Value(ctx, 4096)
		if err != nil {
			return false, err
		}
		y, err := b.Value(ctx, 4096)
		if err != nil {
			return false, err
		}
		return metadataValueEqual(x, y), nil
	}
	if kind == reflect.Map {
		// Captures and transforms preserve Go map iteration order, which
		// carries no semantic meaning. Match borrowed keys without retaining
		// either map's bodies. This deliberately trades quadratic key scans
		// for fixed memory; every scan remains context-cancellable.
		equal := true
		err := a.MapEntries(ctx, func(key, value metadataLogicalNode) error {
			found := false
			err := b.MapEntries(ctx, func(otherKey, otherValue metadataLogicalNode) error {
				same, err := legacyMetadataWriteEqualMode(ctx, key, otherKey, depth+1, true)
				if err != nil {
					return err
				}
				if !same {
					return nil
				}
				found = true
				equal, err = legacyMetadataWriteEqualMode(ctx, value, otherValue, depth+1, strict || a.typ.Key().Kind() != reflect.String)
				if err != nil {
					return err
				}
				return errMetadataAggregateStop
			})
			if err != nil && !errors.Is(err, errMetadataAggregateStop) {
				return err
			}
			if !found {
				equal = false
			}
			if !equal {
				return errMetadataAggregateStop
			}
			return nil
		})
		if errors.Is(err, errMetadataAggregateStop) {
			err = nil
		}
		return equal, err
	}
	for i := 0; i < count; i++ {
		typ := a.typ.Elem
		skip := false
		if kind == reflect.Struct {
			field := a.typ.Field(i)
			typ = func() reflect.Type { return field.Type }
			skip = !strict && field.Tag.Get("json") == "-"
		}
		x, err := left.child(typ())
		if err != nil {
			return false, err
		}
		y, err := right.child(typ())
		if err != nil {
			return false, err
		}
		if skip {
			continue
		}
		same, err := legacyMetadataWriteEqualMode(ctx, x, y, depth+1, strict || kind == reflect.Array)
		if err != nil || !same {
			return same, err
		}
	}
	return true, nil
}

func (p *legacyMetadataWritePlan) headers(ctx context.Context, f *legacyMetadataWriteField) error {
	kind := f.before.typ.Kind()
	if kind != reflect.Slice && !updateMetadataField(f.field.name) {
		same, err := p.equal(ctx, f.before, f.next)
		f.changed = !same
		f.full = true
		return err
	}
	aNil, err := f.before.isNil(ctx)
	if err != nil {
		return err
	}
	bNil, err := f.next.isNil(ctx)
	if err != nil {
		return err
	}
	if aNil != bNil || aNil {
		same, err := p.equal(ctx, f.before, f.next)
		f.changed = !same
		f.full = true
		return err
	}
	if kind == reflect.Slice {
		if _, ok := f.before.typ.Elem().FieldByName("ID"); !ok {
			same, err := p.equal(ctx, f.before, f.next)
			f.changed = !same
			f.full = true
			return err
		}
	}
	f.parentNil = map[string]bool{}
	f.parentBefore, f.parentNext = map[string]metadataLogicalNode{}, map[string]metadataLogicalNode{}
	positions := map[string]int{}
	read := func(next bool) error {
		node := f.before
		if next {
			node = f.next
		}
		appendRow := func(parent string, ordinal int, item metadataLogicalNode) error {
			idNode, err := issueHotField(ctx, item, "ID")
			if err != nil {
				return err
			}
			idWork := &ResourceRead{store: p.store}
			defer idWork.Close()
			id, key, full, err := p.headerID(ctx, idWork, idNode, parent, updateMetadataField(f.field.name))
			if err != nil {
				return err
			}
			if full {
				f.full = true
			}
			if err := p.retainHeaderOrder(ctx); err != nil {
				return err
			}
			if !next {
				_, exists := positions[key]
				if exists {
					f.full = true
				}
				if err := p.retainHeaderRow(ctx, id, key, !exists); err != nil {
					return err
				}
				positions[key] = len(f.rows)
				f.rows = append(f.rows, legacyMetadataWriteRow{id: id, parent: parent, before: item, oldOrdinal: ordinal, ordinal: -1})
				f.oldOrder = append(f.oldOrder, len(f.rows)-1)
			} else {
				at, exists := positions[key]
				if !exists {
					if err := p.retainHeaderRow(ctx, id, key, true); err != nil {
						return err
					}
					at = len(f.rows)
					positions[key] = at
					f.rows = append(f.rows, legacyMetadataWriteRow{id: id, parent: parent, oldOrdinal: -1})
				}
				if f.rows[at].next.typ != nil {
					f.full = true
				}
				f.rows[at].next, f.rows[at].ordinal = item, ordinal
				f.order = append(f.order, at)
			}
			return nil
		}
		if !updateMetadataField(f.field.name) {
			return node.Children(ctx, func(key reflect.Value, item metadataLogicalNode) error { return appendRow("", int(key.Int()), item) })
		}
		return node.MapEntries(ctx, func(parent, list metadataLogicalNode) error {
			name, err := p.headerParent(ctx, parent, next)
			if err != nil {
				return err
			}
			nilList, err := list.isNil(ctx)
			if err != nil {
				return err
			}
			if !next {
				f.parents = append(f.parents, name)
				f.parentNil[name] = nilList
				f.parentBefore[name] = list
			} else {
				f.parentNext[name] = list
				found := false
				for _, old := range f.parents {
					if old == name {
						found = true
						break
					}
				}
				if !found || f.parentNil[name] != nilList {
					f.full = true
				}
			}
			return list.Children(ctx, func(key reflect.Value, item metadataLogicalNode) error { return appendRow(name, int(key.Int()), item) })
		})
	}
	if err := read(false); err != nil {
		return err
	}
	if err := read(true); err != nil {
		return err
	}
	if updateMetadataField(f.field.name) {
		if len(f.parentBefore) != len(f.parentNext) {
			f.full = true
		}
		for _, row := range f.rows {
			if row.before.typ == nil || row.next.typ == nil {
				f.full = true
			}
		}
	}
	if f.full {
		same, err := p.equal(ctx, f.before, f.next)
		f.changed = !same
		return err
	}
	for i := range f.rows {
		row := &f.rows[i]
		if p.progress != nil && f.field.name == "projects" {
			row.forced, err = p.progress.HasProject(ctx, row.id)
			if err != nil {
				return err
			}
		}
		same := false
		if row.before.typ != nil && row.next.typ != nil {
			var err error
			same, err = p.equal(ctx, row.before, row.next)
			if err != nil {
				return err
			}
		}
		row.changed = !same
		f.changed = f.changed || row.changed
	}
	if len(f.oldOrder) != len(f.order) {
		f.changed = true
	}
	if !f.changed {
		for i := range f.order {
			if f.order[i] != f.oldOrder[i] {
				f.changed = true
				break
			}
		}
	}
	return nil
}

func (p *legacyMetadataWritePlan) rebase(ctx context.Context, f *legacyMetadataWriteField) error {
	if handled, err := p.rebaseProjectUpdateScheduleParentMap(ctx, f); handled || err != nil {
		return err
	}
	if f.full {
		return p.rebaseFull(ctx, f)
	}
	order := make([]int, 0, len(f.rows))
	for i, row := range f.rows {
		if (row.changed || row.forced) && row.before.typ != nil && row.next.typ != nil {
			order = append(order, i)
		}
	}
	keyOf := func(row legacyMetadataWriteRow) string {
		if updateMetadataField(f.field.name) {
			return updateRecordKey(row.parent, row.id)
		}
		return row.id
	}
	sort.SliceStable(order, func(i, j int) bool { return keyOf(f.rows[order[i]]) < keyOf(f.rows[order[j]]) })
	var conflicts []metadataRecordKey
	for _, i := range order {
		err := p.rebaseRow(ctx, f, i)
		if keys, ok := conflictedMetadataKeys(err); ok {
			conflicts = append(conflicts, keys...)
		} else if err != nil {
			return err
		}
	}
	if len(conflicts) > 0 {
		return &metadataConflictError{keys: conflicts}
	}
	return nil
}

func (p *legacyMetadataWritePlan) rebaseRow(ctx context.Context, f *legacyMetadataWriteField, index int) error {
	if handled, err := p.rebaseProjectProgressRow(ctx, f, index); handled || err != nil {
		return err
	}
	if handled, err := p.rebaseIntegrationDeliveryRow(ctx, f, index); handled || err != nil {
		return err
	}
	if handled, err := p.rebaseAgentSessionLifecycleRow(ctx, f, index); handled || err != nil {
		return err
	}
	if handled, err := p.rebaseWorkflowRunStreamRow(ctx, f, index); handled || err != nil {
		return err
	}
	row := &f.rows[index]
	work := &ResourceRead{store: p.store}
	defer work.Close()
	for _, node := range []metadataLogicalNode{row.before, row.next} {
		if err := legacyActorWriteNodeWork(ctx, work, node, 64); err != nil {
			return err
		}
	}
	left, err := row.before.Value(ctx, metadataWorkBytes)
	if err != nil {
		return err
	}
	right, err := row.next.Value(ctx, metadataWorkBytes)
	if err != nil {
		return err
	}
	name, key := f.field.name, row.id
	if updateMetadataField(name) {
		key = updateRecordKey(row.parent, row.id)
	}
	if p.tx.rebasedMetadata[metadataRecordKey{name, key}] {
		return nil
	}
	guard := withLegacyMetadataDeltaReadBudget(ctx, work)
	stored, err := lockMetadataRecords(guard, p.tx, p.workspace, name, []string{key})
	if err != nil {
		return err
	}
	raw, exists := stored[key]
	if !exists {
		backed, err := metadataFieldRecordBacked(guard, p.tx, p.workspace, name, reflect.TypeFor[domain.Bootstrap]().Field(f.field.index).Type)
		if err != nil {
			return err
		}
		if !backed {
			// An inline field has no rows; rebaseMetadata leaves it as is.
			return nil
		}
	}
	refs, err := p.references(ctx, p.requested, work, left.Interface(), right.Interface(), raw)
	if err != nil {
		return err
	}
	var before, next domain.Bootstrap
	before.Users, next.Users = legacyMetadataRefsUsers(refs), legacyMetadataRefsUsers(refs)
	fieldLeft, fieldRight := reflect.ValueOf(&before).Elem().Field(f.field.index), reflect.ValueOf(&next).Elem().Field(f.field.index)
	if updateMetadataField(name) {
		for _, pair := range []struct{ field, value reflect.Value }{{fieldLeft, left}, {fieldRight, right}} {
			pair.field.Set(reflect.MakeMap(pair.field.Type()))
			list := reflect.Append(reflect.MakeSlice(pair.field.Type().Elem(), 0, 1), pair.value)
			pair.field.SetMapIndex(reflect.ValueOf(row.parent), list)
		}
	} else {
		fieldLeft.Set(reflect.Append(reflect.MakeSlice(fieldLeft.Type(), 0, 1), left))
		fieldRight.Set(reflect.Append(reflect.MakeSlice(fieldRight.Type(), 0, 1), right))
	}
	// The original rebaser owns normalization, duplicate/update precedence,
	// deletion and conflict rules. Its second guarded read sees the same lock.
	var extra map[string][]string
	if row.forced {
		extra = map[string][]string{name: {key}}
	}
	_, _, err = p.store.rebaseMetadata(guard, p.tx, p.workspace, &before, &next, []metadataField{f.field}, extra)
	if err != nil {
		return err
	}
	if updateMetadataField(name) {
		fieldLeft = reflect.ValueOf(before).Field(f.field.index).MapIndex(reflect.ValueOf(row.parent))
		fieldRight = reflect.ValueOf(next).Field(f.field.index).MapIndex(reflect.ValueOf(row.parent))
	} else {
		fieldLeft = reflect.ValueOf(before).Field(f.field.index)
		fieldRight = reflect.ValueOf(next).Field(f.field.index)
	}
	if !exists || fieldLeft.Len() == 0 && fieldRight.Len() == 0 {
		row.deleted = true
		return nil
	}
	if fieldLeft.Len() != 1 || fieldRight.Len() != 1 {
		return ErrBoundedMetadataCoverage
	}
	actualLeft, actualRight := fieldLeft.Index(0), fieldRight.Index(0)
	row.before, err = p.stage(ctx, row.before, left, actualLeft)
	if err != nil {
		return err
	}
	row.next, err = p.stage(ctx, row.next, right, actualRight)
	if err != nil {
		return err
	}
	id := actualRight.FieldByName("ID").String()
	if id != row.id {
		if uint64(len(id)) > (metadataWorkBytes-8192)/64 {
			return ErrMetadataWorkBudget
		}
		if err := p.work.reserve(ctx, uint64(len(id))*64+8192); err != nil {
			return err
		}
		row.id = id
	}
	return nil
}

func legacyMetadataRefsUsers(refs issueReferences) []domain.User {
	users := make([]domain.User, 0, len(refs.users))
	for _, user := range refs.users {
		users = append(users, user)
	}
	return users
}

func (p *legacyMetadataWritePlan) rebaseFull(ctx context.Context, f *legacyMetadataWriteField) error {
	if handled, err := p.rebaseAgentSessionLifecycleCollection(ctx, f); handled || err != nil {
		return err
	}
	ready, err := p.inlineFieldReady(ctx, f, nil)
	if err != nil {
		return err
	}
	if ready {
		// The original rebaser retains inline values when there are no
		// canonical rows. Keep their frames rather than decoding the field.
		f.inlineReady = true
		return nil
	}
	work := &ResourceRead{store: p.store}
	defer work.Close()
	left, right, err := p.materializeField(ctx, f, work)
	if err != nil {
		return err
	}
	originalLeft, originalRight := left, right
	if left.Kind() == reflect.Map || left.Kind() == reflect.Slice {
		originalLeft, originalRight = cloneMetadataContainer(left), cloneMetadataContainer(right)
	}
	var before, next domain.Bootstrap
	reflect.ValueOf(&before).Elem().Field(f.field.index).Set(left)
	reflect.ValueOf(&next).Elem().Field(f.field.index).Set(right)
	guard := withLegacyMetadataDeltaReadBudget(ctx, work)
	var extra map[string][]string
	if p.progress != nil && f.field.name == "projects" {
		ids := []string{}
		if err := p.progress.ForEachProject(ctx, func(id string) error {
			if err := work.reserve(ctx, uint64(len(id))*16+256); err != nil {
				return err
			}
			ids = append(ids, id)
			return nil
		}); err != nil {
			return err
		}
		extra = map[string][]string{"projects": ids}
	}
	var canonical [][]byte
	if updateMetadataField(f.field.name) || rebaseableMetadataField(f.field, left.Type()) {
		keys := map[string]bool{}
		if updateMetadataField(f.field.name) {
			for _, row := range f.rows {
				if row.before.typ != nil && row.next.typ != nil {
					keys[updateRecordKey(row.parent, row.id)] = true
				}
			}
		} else {
			a, b := newMetadataCollection(f.field, left), newMetadataCollection(f.field, right)
			for key := range b.ids {
				old, ok := a.record(key)
				value, _ := b.record(key)
				if ok && !metadataValueEqual(old, value) {
					keys[key] = true
				}
			}
		}
		if len(extra[f.field.name]) > 0 {
			a, b := newMetadataCollection(f.field, left), newMetadataCollection(f.field, right)
			for _, id := range extra[f.field.name] {
				if _, inBefore := a.record(id); inBefore {
					if _, inAfter := b.record(id); inAfter {
						keys[id] = true
					}
				}
			}
		}
		ids := make([]string, 0, len(keys))
		for key := range keys {
			ids = append(ids, key)
		}
		sort.Strings(ids)
		if len(ids) > 0 {
			rows, err := lockMetadataRecords(guard, p.tx, p.workspace, f.field.name, ids)
			if err != nil {
				return err
			}
			for _, id := range ids {
				canonical = append(canonical, rows[id])
			}
		}
	}
	refs, err := p.references(ctx, p.requested, work, left.Interface(), right.Interface(), canonical...)
	if err != nil {
		return err
	}
	if f.field.name != "users" {
		before.Users, next.Users = legacyMetadataRefsUsers(refs), legacyMetadataRefsUsers(refs)
	}
	_, _, err = p.store.rebaseMetadata(guard, p.tx, p.workspace, &before, &next, []metadataField{f.field}, extra)
	if err != nil {
		return err
	}
	left, right = reflect.ValueOf(before).Field(f.field.index), reflect.ValueOf(next).Field(f.field.index)
	f.before, err = p.stage(ctx, f.before, originalLeft, left)
	if err != nil {
		return err
	}
	f.next, err = p.stage(ctx, f.next, originalRight, right)
	return err
}

func (p *legacyMetadataWritePlan) stage(ctx context.Context, original metadataLogicalNode, before, actual reflect.Value) (metadataLogicalNode, error) {
	if reflect.DeepEqual(before.Interface(), actual.Interface()) {
		return original, nil
	}
	if err := p.frames.auditJSONLocations(actual); err != nil {
		return metadataLogicalNode{}, err
	}
	if actual.Type() == reflect.TypeFor[domain.User]() {
		return p.frames.writeAggregateNode(legacyActorUserNode{value: actual}, 0)
	}
	if actual.Kind() == reflect.Map || actual.Kind() == reflect.Slice {
		return p.frames.writeAggregateNode(legacyMetadataWriteNative{base: original, value: actual}, 0)
	}
	return p.frames.writeAggregateNode(legacyMetadataWriteNativeRecord{value: actual}, 0)
}

func (p *legacyMetadataWritePlan) references(ctx context.Context, directory *metadataLogicalSource, work *ResourceRead, left, right any, stored ...[]byte) (issueReferences, error) {
	ids := map[string]bool{}
	var visitJSON func([]byte) error
	visitJSON = func(raw []byte) error {
		if len(raw) == 0 {
			return nil
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil {
			return nil
		}
		if item := object["item"]; len(item) > 0 {
			if err := visitJSON(item); err != nil {
				return err
			}
		}
		for _, name := range []string{"user", "actor", "lead", "creator", "owner"} {
			var user struct {
				ID string `json:"id"`
			}
			if value := object[name]; len(value) > 0 && json.Unmarshal(value, &user) == nil {
				if err := work.reserve(ctx, uint64(len(user.ID))*8+256); err != nil {
					return err
				}
				ids[user.ID] = true
			}
		}
		return nil
	}
	var visitValue func(reflect.Value) error
	visitValue = func(value reflect.Value) error {
		if !value.IsValid() {
			return nil
		}
		if value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return nil
			}
			return visitValue(value.Elem())
		}
		if value.Kind() == reflect.Map {
			iter := value.MapRange()
			for iter.Next() {
				if err := visitValue(iter.Value()); err != nil {
					return err
				}
			}
			return nil
		}
		if value.Kind() == reflect.Slice {
			for i := 0; i < value.Len(); i++ {
				if err := visitValue(value.Index(i)); err != nil {
					return err
				}
			}
			return nil
		}
		if value.Kind() == reflect.Struct {
			if value.Type() == reflect.TypeFor[domain.Bootstrap]() {
				for _, field := range persistedMetadataFields() {
					if field.name != "users" {
						if err := visitValue(value.Field(field.index)); err != nil {
							return err
						}
					}
				}
				return nil
			}
			raw, err := json.Marshal(value.Interface())
			if err != nil {
				return err
			}
			return visitJSON(raw)
		}
		return nil
	}
	for _, value := range []any{left, right} {
		if err := visitValue(reflect.ValueOf(value)); err != nil {
			return issueReferences{}, err
		}
	}
	for _, raw := range stored {
		if err := visitJSON(raw); err != nil {
			return issueReferences{}, err
		}
	}
	users := []domain.User{}
	for id := range ids {
		found, err := metadataAggregateLookupUser(ctx, directory, metadataAggregateNativeNode{value: reflect.ValueOf(id)})
		if err != nil {
			return issueReferences{}, err
		}
		if found == nil {
			continue
		}
		node := found.(metadataAggregateStoredNode).value
		if err := legacyActorWriteNodeWork(ctx, work, node, 64); err != nil {
			return issueReferences{}, err
		}
		value, err := node.Value(ctx, metadataWorkBytes)
		if err != nil {
			return issueReferences{}, err
		}
		users = append(users, value.Interface().(domain.User))
	}
	return newIssueReferences(domain.Bootstrap{Users: users}), nil
}

func (p *legacyMetadataWritePlan) postimage(ctx context.Context, source *metadataLogicalSource) (*metadataLogicalSource, error) {
	return source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		for _, field := range p.fields {
			if field.field.name == name {
				if field.full {
					return metadataAggregateStoredNode{value: field.next}, true, nil
				}
				return legacyMetadataWriteCollection{field: field, next: true}, true, nil
			}
		}
		return metadataAggregateStoredNode{value: original}, false, nil
	})
}

func (p *legacyMetadataWritePlan) materializeField(ctx context.Context, f *legacyMetadataWriteField, work *ResourceRead) (reflect.Value, reflect.Value, error) {
	left, right := f.before, f.next
	if !f.full {
		var err error
		left, err = p.frames.writeAggregateNode(legacyMetadataWriteCollection{field: f}, 0)
		if err != nil {
			return reflect.Value{}, reflect.Value{}, err
		}
		right, err = p.frames.writeAggregateNode(legacyMetadataWriteCollection{field: f, next: true}, 0)
		if err != nil {
			return reflect.Value{}, reflect.Value{}, err
		}
	}
	for _, node := range []metadataLogicalNode{left, right} {
		// Native decoding and the rebaser's shallow container copies stay
		// live. JSON encoding work is measured from the decoded values below.
		if node.cost > (metadataWorkBytes-(64<<10))/2 {
			return reflect.Value{}, reflect.Value{}, ErrMetadataWorkBudget
		}
		if err := work.reserve(ctx, node.cost*2+64<<10); err != nil {
			return reflect.Value{}, reflect.Value{}, err
		}
	}
	a, err := left.Value(ctx, metadataWorkBytes)
	if err != nil {
		return reflect.Value{}, reflect.Value{}, err
	}
	b, err := right.Value(ctx, metadataWorkBytes)
	if err != nil {
		return reflect.Value{}, reflect.Value{}, err
	}
	for _, value := range []reflect.Value{a, b} {
		encoding, err := metadataAggregatePayloadEncodingWork(ctx, value, 0)
		if err != nil {
			return reflect.Value{}, reflect.Value{}, err
		}
		// The slow writer keeps both marshal results and split raw JSON,
		// while rebasing may encode a before/after record pair.
		if encoding > (metadataWorkBytes-(64<<10))/2 {
			return reflect.Value{}, reflect.Value{}, ErrMetadataWorkBudget
		}
		if err := work.reserve(ctx, uint64(encoding)*2+64<<10); err != nil {
			return reflect.Value{}, reflect.Value{}, err
		}
	}
	return a, b, nil
}

func (p *legacyMetadataWritePlan) rootShapes(ctx context.Context) (map[string]string, error) {
	length := p.work.byteLength("data")
	var size int64
	if err := p.tx.QueryRowContext(ctx, "SELECT "+length+" FROM workspace_states WHERE workspace_key=?", p.workspace).Scan(&size); err != nil {
		return nil, err
	}
	if size > MetadataRecordPageMaxBytes && size <= int64(p.store.maxStateBytes) {
		if p.store.dialect != "sqlite" {
			// Streaming uses several reads; pin the root after the union's
			// metadata row locks and recapture its current byte length.
			if err := p.tx.QueryRowContext(ctx, "SELECT "+length+" FROM workspace_states WHERE workspace_key=? FOR UPDATE", p.workspace).Scan(&size); err != nil {
				return nil, err
			}
		}
		if size < 0 || size > int64(p.store.maxStateBytes) {
			return nil, ErrMetadataWorkBudget
		}
		if err := p.work.reserve(ctx, 2<<20); err != nil {
			return nil, err
		}
		root, err := p.store.scanMetadataInlineRoot(ctx, p.tx, p.workspace, size)
		if err != nil {
			return nil, err
		}
		captured, _ := ctx.Value(metadataInlineRootCaptureKey{}).(*metadataInlineRoot)
		if integrationDeliveryStreamEvent(ctx) != "" && p.progress == nil && captured == nil {
			p.inlineRoot = root
		} else {
			defer root.Close()
		}
		return root.shapes()
	}
	if size < 0 || size > MetadataRecordPageMaxBytes || uint64(size) > (metadataWorkBytes-(64<<10))/128 {
		return nil, ErrMetadataWorkBudget
	}
	if err := p.work.reserve(ctx, uint64(size)*128+64<<10); err != nil {
		return nil, err
	}
	var raw []byte
	if err := p.tx.QueryRowContext(ctx, "SELECT CASE WHEN "+length+"<=? THEN data ELSE NULL END FROM workspace_states WHERE workspace_key=?", size, p.workspace).Scan(&raw); err != nil {
		return nil, err
	}
	if raw == nil || int64(len(raw)) != size {
		return nil, ErrConcurrentWrite
	}
	var root struct {
		Shapes map[string]string `json:"_flowCollections"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	return root.Shapes, nil
}

func (f *legacyMetadataWriteField) skeleton(next bool, parent string) reflect.Value {
	typ := f.before.typ
	if updateMetadataField(f.field.name) {
		typ = typ.Elem()
	}
	value := reflect.MakeSlice(typ, 0, len(f.rows))
	order := f.oldOrder
	if next {
		order = f.order
	}
	for _, index := range order {
		row := f.rows[index]
		if row.deleted || row.parent != parent {
			continue
		}
		item := reflect.New(typ.Elem()).Elem()
		item.FieldByName("ID").SetString(row.id)
		value = reflect.Append(value, item)
	}
	return value
}

func (p *legacyMetadataWritePlan) arrayFastReady(ctx context.Context, f *legacyMetadataWriteField) (bool, error) {
	before, after := f.skeleton(false, ""), f.skeleton(true, "")
	positions := map[string]int{}
	for i := 0; i < before.Len(); i++ {
		id := before.Index(i).FieldByName("ID").String()
		if _, exists := positions[id]; exists {
			return false, nil
		}
		positions[id] = i
	}
	seen := map[string]bool{}
	first, last, position, tail := -1, -1, -1, false
	for i := 0; i < after.Len(); i++ {
		id := after.Index(i).FieldByName("ID").String()
		if id == "" || len(id) > 191 || seen[id] {
			return false, nil
		}
		seen[id] = true
		at, found := positions[id]
		if !found {
			tail = tail || first >= 0
			continue
		}
		if tail || at <= position {
			return false, nil
		}
		if first < 0 {
			first = i
		}
		last, position = i, at
	}
	if first < 0 {
		return false, nil
	}
	lookup := []string{after.Index(first).FieldByName("ID").String(), after.Index(last).FieldByName("ID").String()}
	for _, row := range f.rows {
		if row.changed && !row.deleted && row.before.typ != nil && row.next.typ != nil {
			lookup = append(lookup, row.id)
		}
	}
	orders, err := p.orders(ctx, f.field.name, lookup)
	if err != nil {
		return false, err
	}
	for _, id := range lookup {
		if _, found := orders[id]; !found {
			return false, nil
		}
	}
	return true, nil
}

func (p *legacyMetadataWritePlan) updateFastReady(ctx context.Context, f *legacyMetadataWriteField) (bool, error) {
	var keys []string
	for _, parent := range f.parents {
		marker := fmt.Sprintf("%x", sha256SumParent(parent))
		keys = append(keys, marker)
	}
	for _, row := range f.rows {
		if !row.deleted && row.before.typ != nil {
			keys = append(keys, updateRecordKey(row.parent, row.id))
		}
	}
	orders, err := p.orders(ctx, f.field.name, keys)
	if err != nil {
		return false, err
	}
	for _, key := range keys {
		if _, ok := orders[key]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func sha256SumParent(parent string) [32]byte { return sha256.Sum256([]byte("parent:" + parent)) }

func (p *legacyMetadataWritePlan) orders(ctx context.Context, field string, keys []string) (map[string]int, error) {
	result := map[string]int{}
	guard := legacyMetadataDeltaBudget(withLegacyMetadataDeltaReadBudget(ctx, p.work))
	for start := 0; start < len(keys); start += MetadataRecordBatchMaxKeys {
		part, err := guard.arrayOrders(ctx, p.tx, p.workspace, field, keys[start:min(start+MetadataRecordBatchMaxKeys, len(keys))])
		if err != nil {
			return nil, err
		}
		for key, order := range part {
			result[key] = order
		}
	}
	return result, nil
}

func (p *legacyMetadataWritePlan) writeArray(ctx context.Context, f *legacyMetadataWriteField) error {
	// Topology is ready before writing. Persist additions, changed surviving
	// records and removals in the original fast-path sequence.
	before, after := f.skeleton(false, ""), f.skeleton(true, "")
	positions := map[string]int{}
	for i := 0; i < before.Len(); i++ {
		positions[before.Index(i).FieldByName("ID").String()] = i
	}
	first, last := -1, -1
	var lookup []string
	for i := 0; i < after.Len(); i++ {
		id := after.Index(i).FieldByName("ID").String()
		if _, ok := positions[id]; ok {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return ErrBoundedMetadataCoverage
	}
	lookup = append(lookup, after.Index(first).FieldByName("ID").String(), after.Index(last).FieldByName("ID").String())
	for _, row := range f.rows {
		if row.changed && !row.deleted && row.before.typ != nil && row.next.typ != nil {
			lookup = append(lookup, row.id)
		}
	}
	orders, err := p.orders(ctx, f.field.name, lookup)
	if err != nil {
		return err
	}
	write := func(index, order int) error {
		row := &f.rows[f.order[index]]
		if handled, err := p.writeProjectProgressRow(ctx, f, row, order); handled || err != nil {
			return err
		}
		if handled, err := p.writeIntegrationDeliveryRow(ctx, f, row, order); handled || err != nil {
			return err
		}
		if handled, err := p.writeAgentSessionLifecycleRow(ctx, f, row, order); handled || err != nil {
			return err
		}
		if handled, err := p.writeWorkflowRunStreamRow(ctx, f, row, order); handled || err != nil {
			return err
		}
		work := &ResourceRead{store: p.store}
		defer work.Close()
		if err := legacyActorWriteNodeWork(ctx, work, row.next, 64); err != nil {
			return err
		}
		next, err := row.next.Value(ctx, metadataWorkBytes)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(next.Interface())
		if err != nil {
			return err
		}
		var previous []byte
		if row.before.typ != nil {
			if err := legacyActorWriteNodeWork(ctx, work, row.before, 64); err != nil {
				return err
			}
			old, err := row.before.Value(ctx, metadataWorkBytes)
			if err != nil {
				return err
			}
			previous, err = json.Marshal(old.Interface())
			if err != nil {
				return err
			}
		}
		refs, err := p.references(ctx, p.actual, work, next.Interface(), nil, previous, raw)
		if err != nil {
			return err
		}
		if previous != nil && refs.equalDisplayData(previous, raw) {
			return nil
		}
		return writeMetadataRecordRow(ctx, p.tx, p.workspace, metadataRecordKey{f.field.name, row.id}, metadataRecord{data: raw, order: order}, previous)
	}
	firstID, lastID := after.Index(first).FieldByName("ID").String(), after.Index(last).FieldByName("ID").String()
	filtered := make([]int, 0, len(f.order))
	for _, index := range f.order {
		if !f.rows[index].deleted {
			filtered = append(filtered, index)
		}
	}
	originalOrder := f.order
	f.order = filtered
	defer func() { f.order = originalOrder }()
	for i := 0; i < first; i++ {
		if err := write(i, orders[firstID]-first+i); err != nil {
			return err
		}
	}
	for i := last + 1; i < after.Len(); i++ {
		if err := write(i, orders[lastID]+i-last); err != nil {
			return err
		}
	}
	for i := first; i <= last; i++ {
		row := f.rows[f.order[i]]
		if row.changed {
			if err := write(i, orders[row.id]); err != nil {
				return err
			}
		}
	}
	seen := map[string]bool{}
	for _, index := range f.order {
		seen[f.rows[index].id] = true
	}
	for _, index := range f.oldOrder {
		row := f.rows[index]
		if !row.deleted && !seen[row.id] {
			if err := deleteMetadataRecordRow(ctx, p.tx, p.workspace, metadataRecordKey{f.field.name, row.id}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *legacyMetadataWritePlan) writeUpdates(ctx context.Context, f *legacyMetadataWriteField) error {
	for _, parent := range f.parents {
		changed := false
		for _, row := range f.rows {
			if row.parent == parent && row.changed {
				changed = true
				break
			}
		}
		if !changed {
			continue
		}
		// Each parent retains complete ID/order topology. Admit one changed
		// body pair at a time and let the original update writer own markers.
		for _, selected := range f.order {
			row := &f.rows[selected]
			if row.parent != parent || row.deleted || !row.changed {
				continue
			}
			if handled, err := p.writeProjectUpdateScheduleRow(ctx, f, row); handled || err != nil {
				if err != nil {
					return err
				}
				continue
			}
			work := &ResourceRead{store: p.store}
			err := func() error {
				defer work.Close()
				left, right := f.skeleton(false, parent), f.skeleton(true, parent)
				for _, node := range []metadataLogicalNode{row.before, row.next} {
					if node.typ != nil {
						if err := legacyActorWriteNodeWork(ctx, work, node, 64); err != nil {
							return err
						}
					}
				}
				old, err := row.before.Value(ctx, metadataWorkBytes)
				if err != nil {
					return err
				}
				next, err := row.next.Value(ctx, metadataWorkBytes)
				if err != nil {
					return err
				}
				for i := 0; i < left.Len(); i++ {
					if left.Index(i).FieldByName("ID").String() == row.id {
						left.Index(i).Set(old)
					}
				}
				for i := 0; i < right.Len(); i++ {
					if right.Index(i).FieldByName("ID").String() == row.id {
						right.Index(i).Set(next)
					}
				}
				before, after := reflect.MakeMap(f.before.typ), reflect.MakeMap(f.next.typ)
				before.SetMapIndex(reflect.ValueOf(parent), left)
				after.SetMapIndex(reflect.ValueOf(parent), right)
				refs, err := p.references(ctx, p.actual, work, old.Interface(), next.Interface())
				if err != nil {
					return err
				}
				ok, err := writeUpdatesFieldDelta(withLegacyMetadataDeltaReadBudget(ctx, work), p.tx, p.workspace, refs, f.field, before, after)
				if err != nil {
					return err
				}
				if !ok {
					return ErrConcurrentWrite
				}
				return nil
			}()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

type legacyMetadataWriteCollection struct {
	field  *legacyMetadataWriteField
	next   bool
	parent *string
}

func (n legacyMetadataWriteCollection) nodeType() reflect.Type {
	if n.parent != nil {
		return n.field.before.typ.Elem()
	}
	return n.field.before.typ
}
func (n legacyMetadataWriteCollection) logicalBase() metadataLogicalNode {
	if n.parent != nil {
		if n.next {
			return n.field.parentNext[*n.parent]
		}
		return n.field.parentBefore[*n.parent]
	}
	if n.next {
		return n.field.next
	}
	return n.field.before
}
func (n legacyMetadataWriteCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyMetadataWriteCollection) logicalMapEntries(ctx context.Context, visit func(metadataAggregateNode, metadataAggregateNode) error) error {
	for _, parent := range n.field.parents {
		key := metadataAggregateNativeNode{value: reflect.ValueOf(parent)}
		value := legacyMetadataWriteCollection{field: n.field, next: n.next, parent: &parent}
		if err := visit(key, value); err != nil {
			return err
		}
	}
	return nil
}
func (n legacyMetadataWriteCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	if n.parent == nil && updateMetadataField(n.field.field.name) {
		for _, parent := range n.field.parents {
			if err := visit(reflect.ValueOf(parent), legacyMetadataWriteCollection{field: n.field, next: n.next, parent: &parent}); err != nil {
				return err
			}
		}
		return nil
	}
	parent := ""
	if n.parent != nil {
		parent = *n.parent
	}
	order := n.field.oldOrder
	if n.next {
		order = n.field.order
	}
	ordinal := 0
	for _, index := range order {
		row := n.field.rows[index]
		if row.deleted || row.parent != parent {
			continue
		}
		node := row.before
		if n.next {
			node = row.next
		}
		if err := visit(reflect.ValueOf(ordinal), metadataAggregateStoredNode{value: node}); err != nil {
			return err
		}
		ordinal++
	}
	return nil
}

// Enumerating native containers preserves User.Via in freshly merged records;
// standard writeValue intentionally omits json:"-" members.
type legacyMetadataWriteNative struct {
	base  metadataLogicalNode
	value reflect.Value
}

func (n legacyMetadataWriteNative) nodeType() reflect.Type           { return n.value.Type() }
func (n legacyMetadataWriteNative) logicalBase() metadataLogicalNode { return n.base }
func (n legacyMetadataWriteNative) nodeValue(context.Context, int) (reflect.Value, error) {
	return n.value, nil
}
func (n legacyMetadataWriteNative) logicalMapEntries(ctx context.Context, visit func(metadataAggregateNode, metadataAggregateNode) error) error {
	// MapRange order is intentionally unconstrained. Frame comparison and
	// canonical JSON encoding must match keys rather than frame positions.
	iter := n.value.MapRange()
	for iter.Next() {
		key, value := iter.Key(), iter.Value()
		var base metadataLogicalNode
		err := n.base.MapEntries(ctx, func(k, v metadataLogicalNode) error {
			matched, err := k.StringEqual(ctx, key.String())
			if err != nil {
				return err
			}
			if matched {
				base = v
				return errMetadataAggregateStop
			}
			return nil
		})
		if err != nil && !errors.Is(err, errMetadataAggregateStop) {
			return err
		}
		if base.typ == nil {
			return ErrBoundedMetadataCoverage
		}
		if err := visit(metadataAggregateNativeNode{value: key}, legacyMetadataWriteNative{base: base, value: value}); err != nil {
			return err
		}
	}
	return nil
}
func (n legacyMetadataWriteNative) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	if n.value.Kind() != reflect.Slice {
		return ErrMetadataRecordQuery
	}
	for i := 0; i < n.value.Len(); i++ {
		var child metadataAggregateNode = legacyMetadataWriteNativeRecord{value: n.value.Index(i)}
		if n.value.Type().Elem() == reflect.TypeFor[domain.User]() {
			child = legacyActorUserNode{value: n.value.Index(i)}
		}
		if err := visit(reflect.ValueOf(i), child); err != nil {
			return err
		}
	}
	return ctx.Err()
}

type legacyMetadataWriteNativeRecord struct{ value reflect.Value }

func (n legacyMetadataWriteNativeRecord) nodeType() reflect.Type { return n.value.Type() }
func (n legacyMetadataWriteNativeRecord) nodeValue(context.Context, int) (reflect.Value, error) {
	return n.value, nil
}
func (n legacyMetadataWriteNativeRecord) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	for i := 0; i < n.value.NumField(); i++ {
		value := n.value.Field(i)
		var child metadataAggregateNode = metadataAggregateNativeNode{value: value}
		if value.Type() == reflect.TypeFor[domain.User]() {
			child = legacyActorUserNode{value: value}
		}
		if value.Type() == reflect.TypeFor[*domain.User]() {
			child = legacyCountWriteUserPointer{value: value}
		}
		if err := visit(reflect.ValueOf(n.value.Type().Field(i).Name), child); err != nil {
			return err
		}
	}
	return ctx.Err()
}
