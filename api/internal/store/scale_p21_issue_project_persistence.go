package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"time"

	"flow/api/internal/domain"
)

type issueMetadataFieldPlan struct {
	name   string
	inline bool
}

func (s *SQLiteStore) admitIssueMetadataRows(ctx context.Context, tx *sqlTx, workspace, field string, ids []string, inputs *issueHotInputs) error {
	lock := ""
	if s.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	length := "OCTET_LENGTH(data)"
	if s.dialect == "sqlite" {
		length = "LENGTH(CAST(data AS BLOB))"
	}
	clause, args := bindList("record_key", ids)
	rows, err := tx.QueryContext(ctx, `SELECT `+length+` FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+clause+` ORDER BY record_key`+lock, append([]any{workspace, field}, args...)...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var size int64
		if err := rows.Scan(&size); err != nil {
			rows.Close()
			return err
		}
		if size < 0 || uint64(size) > (metadataWorkBytes-2048)/8 {
			rows.Close()
			return ErrMetadataWorkBudget
		}
		if err := inputs.reserve(ctx, uint64(size)*8+2048); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	stored, err := lockMetadataRecords(ctx, tx, workspace, field, ids)
	if err != nil {
		return err
	}
	for key, raw := range stored {
		var record struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &record); err != nil {
			return err
		}
		if record.ID != key {
			return fmt.Errorf("%w: %s requires native ordinal/physical-key rebinding", ErrBoundedMetadataCoverage, field)
		}
	}
	return nil
}

func (s *SQLiteStore) issueMetadataWebhookNeeded(ctx context.Context, workspace string) (bool, error) {
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, 64<<10)
	if err != nil {
		return false, err
	}
	defer release()
	source, err := s.retainMetadataLogicalSource(metadataLogicalUnpinnedContext(ctx), workspace)
	if err != nil {
		return false, err
	}
	defer source.Release()
	return s.logicalWebhookNeeded(ctx, source, workspace)
}

func (s *SQLiteStore) planIssueMetadataFields(ctx context.Context, tx *sqlTx, workspace string, keys []RecordKey) ([]issueMetadataFieldPlan, error) {
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, 2<<20)
	if err != nil {
		return nil, err
	}
	defer release()
	var names []string
	for _, key := range keys {
		if !slices.Contains(names, key.Kind) {
			names = append(names, key.Kind)
		}
	}
	slices.Sort(names)
	read, err := s.metadataCollectionsInSnapshot(ctx, tx, workspace, true, names...)
	if err != nil {
		return nil, err
	}
	defer read.Close()
	if err := read.scanRoot(nil); err != nil {
		return nil, err
	}
	var plans []issueMetadataFieldPlan
	for _, name := range names {
		field := read.collections[name]
		switch field.manifest {
		case "":
			plans = append(plans, issueMetadataFieldPlan{name: name, inline: true})
		case "array":
			// A nonempty inline prefix combined with canonical append rows
			// needs the complete mixed-shape field writer.
			hasInline := false
			err := read.scanRoot(func(found string, part metadataCollectionRange, null bool) error {
				if found != name || null {
					return nil
				}
				return read.scanner(part).array(func(metadataCollectionRange) error {
					hasInline = true
					return nil
				}, 0)
			})
			if err != nil {
				return nil, err
			}
			if hasInline {
				return nil, fmt.Errorf("%w: mixed inline/canonical %s", ErrBoundedMetadataCoverage, name)
			}
			plans = append(plans, issueMetadataFieldPlan{name: name})
		default:
			return nil, fmt.Errorf("%w: unsupported %s shape", ErrBoundedMetadataCoverage, name)
		}
	}
	return plans, nil
}

// Native selected inputs remain owned and writeMu stays held through commit.
// Callback metadata edits are discarded except the two derived document scope
// fields. The single legacy rebase runs before progress and either persistence
// branch receives that modified base.
func (s *SQLiteStore) persistSelectedIssueMetadata(ctx context.Context, tx *sqlTx, workspace string, selected domain.Bootstrap, before, after []domain.Issue, documents []domain.Document, inputs *issueHotInputs) (*metadataLogicalPublication, error) {
	nativeBefore, base, next := selected, selected, selected
	scoped := false
	if documents != nil {
		next.Documents, scoped = documentScopes(base.Documents, documents)
	}
	projects := issueProgressProjects(next, before, after)
	if !scoped && len(projects) == 0 {
		return nil, nil
	}
	keys := make([]RecordKey, 0, len(projects)+len(next.Documents))
	for _, id := range projects {
		keys = append(keys, RecordKey{Kind: "projects", ID: id})
	}
	if scoped {
		for index, document := range next.Documents {
			key := RecordKey{Kind: "documents", ID: document.ID}
			if !reflect.DeepEqual(document, base.Documents[index]) && !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		}
	}
	plans, err := s.planIssueMetadataFields(ctx, tx, workspace, keys)
	if err != nil {
		return nil, err
	}
	baseStamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return nil, err
	}
	work := &issueProgressWork{inputs: inputs}
	ctx = context.WithValue(ctx, issueProgressWorkKey{}, work)
	var splitKeys []RecordKey
	inline := false
	for _, plan := range plans {
		var ids []string
		for _, key := range keys {
			if key.Kind == plan.name {
				ids = append(ids, key.ID)
				if !plan.inline {
					splitKeys = append(splitKeys, key)
				}
			}
		}
		inline = inline || plan.inline
		slices.Sort(ids)
		// The legacy rebase reads canonical rows even for an inline field.
		// Reserve a peer-enlarged preimage before the driver copies it.
		for start := 0; start < len(ids); start += 500 {
			if err := s.admitIssueMetadataRows(ctx, tx, workspace, plan.name, ids[start:min(start+500, len(ids))], inputs); err != nil {
				return nil, err
			}
		}
	}
	next.Projects = slices.Clone(base.Projects)
	if err := s.rebaseIssueMetadata(ctx, tx, workspace, &base, &next, before, after); err != nil {
		return nil, err
	}
	rebased := !reflect.DeepEqual(nativeBefore.Projects, base.Projects) || !reflect.DeepEqual(nativeBefore.Documents, base.Documents)
	if _, err := refreshIssueProjectProgress(ctx, tx, workspace, &next, before, after, time.Now().UTC()); err != nil {
		return nil, err
	}
	changes, err := selectedIssueMetadataChanges(base, next, splitKeys)
	if err != nil {
		return nil, err
	}
	// rebaseIssueMetadata already locked and merged these rows. Repeating
	// rebase here would apply first-occurrence removal twice to inline data.
	if err := writeMetadataRecordUpserts(ctx, tx, workspace, changes); err != nil {
		return nil, err
	}
	exact := inputs.source.Stamp().revision >= 0 && inputs.source.Stamp() == baseStamp
	if !inline && !rebased {
		return s.prepareMetadataLogicalDeltaPublication(ctx, tx, workspace, inputs.source,
			nativeBefore, next, MetadataRecordScope{Write: keys}, changes, nil, baseStamp, exact, false)
	}
	candidate, err := selectedIssueMetadataPostimage(ctx, inputs.source, nativeBefore, next, keys)
	if err != nil {
		return nil, err
	}
	defer func() {
		if candidate != nil {
			candidate.Release()
		}
	}()
	if inline {
		rebasedBefore, err := selectedIssueMetadataPostimage(ctx, inputs.source, nativeBefore, base, keys)
		if err != nil {
			return nil, err
		}
		defer rebasedBefore.Release()
		for _, plan := range plans {
			if !plan.inline {
				continue
			}
			index, _, _ := metadataLogicalField(plan.name)
			adjusted, err := s.persistMetadataLogicalInlinePostimage(ctx, tx, workspace, plan.name,
				candidate, rebasedBefore.fields[index], candidate.fields[index])
			if err != nil {
				if adjusted != nil {
					adjusted.Release()
				}
				return nil, err
			}
			candidate.Release()
			candidate = adjusted
		}
	}
	publication, err := s.prepareMetadataLogicalPublication(ctx, tx, workspace, candidate, baseStamp, exact, false)
	if err == nil {
		candidate = nil
	}
	return publication, err
}
