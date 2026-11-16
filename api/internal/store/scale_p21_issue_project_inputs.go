package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

// Project inputs extend the frozen non-project selector with project
// progress, legacy editor snapshots (DescriptionState: its mention checks
// read the owned issueEditorLookups) and actual team moves (the label and
// SLA closure of loadIssueSelectedInputs plus the attached documents).
// A CRDT snapshot (ContentState) keeps its coverage boundary: decoding it
// has no admitted work bound yet.
func issueProjectInputsSupported(_ domain.Issue, input domain.IssueUpdateInput) bool {
	return input.ContentState == nil
}

func (s *SQLiteStore) loadIssueProjectInputs(ctx context.Context, workspace string, data *domain.Bootstrap, target domain.Issue, input domain.IssueUpdateInput) (_ *issueHotInputs, err error) {
	if !issueProjectInputsSupported(target, input) {
		return nil, ErrBoundedMetadataCoverage
	}
	read, err := s.loadIssueSelectedInputs(ctx, workspace, data, target, input)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			read.close()
		}
	}()
	projects, err := read.keys(ctx)
	if err != nil {
		return nil, err
	}
	for _, issue := range data.Issues {
		if issue.Project != nil {
			if err := read.addKey(ctx, projects, issue.Project.ID); err != nil {
				return nil, err
			}
		}
	}
	if input.ProjectID != nil {
		if err := read.addKey(ctx, projects, *input.ProjectID); err != nil {
			return nil, err
		}
	}
	if err := read.selectSlice(ctx, "projects", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "ID", projects)
	}); err != nil {
		return nil, err
	}
	if read.move != nil {
		// moveIssueDocuments re-derives the scope of the documents attached
		// to the moved issue (with the projects they name).
		if err := read.loadIssueDocuments(ctx, map[string]bool{target.ID: true}); err != nil {
			return nil, err
		}
	}
	return read, ctx.Err()
}

// Attached document scope is selected separately from project editing. The
// native record remains the mutation preimage, including its time values.
func (r *issueHotInputs) loadIssueDocuments(ctx context.Context, issueIDs map[string]bool) error {
	documents, err := r.keys(ctx)
	if err != nil {
		return err
	}
	index, _, _ := metadataLogicalField("documents")
	if err := r.source.fields[index].Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		wanted, err := issueHotMatches(ctx, record, "IssueID", issueIDs)
		if err != nil || !wanted {
			return err
		}
		_, err = r.withHeader(ctx, record, []string{"ID"}, func(value reflect.Value) (bool, error) {
			id := value.Interface().(domain.Document).ID
			if !documents[id] {
				if uint64(len(id)) > (metadataWorkBytes-512)/8 {
					return false, ErrMetadataWorkBudget
				}
				if err := r.reserve(ctx, uint64(len(id))*8+512); err != nil {
					return false, err
				}
				if err := r.addKey(ctx, documents, id); err != nil {
					return false, err
				}
			}
			return true, nil
		})
		return err
	}); err != nil {
		return err
	}
	// An unattached duplicate of an attached document still participates in
	// documentScopes' last-ID precedence and the canonical record delta.
	if err := r.selectSlice(ctx, "documents", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "ID", documents)
	}); err != nil {
		return err
	}
	projects, err := r.keys(ctx)
	if err != nil {
		return err
	}
	for _, document := range r.data.Documents {
		for _, id := range document.ProjectIDs {
			if err := r.addKey(ctx, projects, id); err != nil {
				return err
			}
		}
	}
	for _, project := range r.data.Projects {
		delete(projects, project.ID)
	}
	return r.selectSlice(ctx, "projects", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "ID", projects)
	})
}

func (r *issueHotInputs) installProjects(data, baseline *domain.Bootstrap) {
	r.install(data, baseline)
	for _, name := range []string{"projects", "documents"} {
		index, _, _ := metadataLogicalField(name)
		value := reflect.ValueOf(r.data).Field(index)
		reflect.ValueOf(data).Elem().Field(index).Set(value)
		reflect.ValueOf(baseline).Elem().Field(index).Set(cloneSnapshotValue(value))
	}
}
