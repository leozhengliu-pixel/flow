//go:build loadtest

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// SyntheticMetadata sizes a load-test workspace. Built only with -tags loadtest.
type SyntheticMetadata struct {
	Projects, Trash, Documents, Conversations int
	// Approximate serialized bytes per item.
	ProjectBytes, TrashBytes, DocumentBytes, ConversationBytes int
	TeamIDs                                                    []string
}

func filler(seed string, n int) string {
	if n <= 0 {
		return ""
	}
	var b strings.Builder
	for b.Len() < n {
		fmt.Fprintf(&b, "%s lorem ipsum dolor sit amet %d. ", seed, b.Len())
	}
	return b.String()[:n]
}

// SeedSyntheticMetadata appends synthetic projects, trash entries, documents
// and AI conversations through the normal persistence path, in batches that
// stay under the per-transaction write budget.
func (s *SQLiteStore) SeedSyntheticMetadata(ctx context.Context, workspace string, spec SyntheticMetadata) error {
	const batchBytes = 6 << 20
	kinds := []struct {
		total, size int
		add         func(data *domain.Bootstrap, index int, now time.Time)
	}{
		{spec.Projects, spec.ProjectBytes, func(data *domain.Bootstrap, i int, now time.Time) {
			data.Projects = append(data.Projects, syntheticProject(data.Viewer, spec, i, now))
		}},
		{spec.Trash, spec.TrashBytes, func(data *domain.Bootstrap, i int, now time.Time) {
			data.Trash = append(data.Trash, syntheticTrash(data.Viewer, spec, i, now))
		}},
		{spec.Documents, spec.DocumentBytes, func(data *domain.Bootstrap, i int, now time.Time) {
			data.Documents = append(data.Documents, syntheticDocument(data.Viewer, spec, i, now))
		}},
		{spec.Conversations, spec.ConversationBytes, func(data *domain.Bootstrap, i int, now time.Time) {
			data.AIConversations = append(data.AIConversations, syntheticConversation(data.Viewer, i, spec.ConversationBytes, now))
		}},
	}
	for _, kind := range kinds {
		perBatch := max(1, batchBytes/max(1, kind.size))
		for start := 0; start < kind.total; start += perBatch {
			first, end, add := start, min(kind.total, start+perBatch), kind.add
			if err := s.mutateFull(ctx, workspace, "loadtest.seeded", nil, func(data *domain.Bootstrap) (string, error) {
				now := time.Now().UTC()
				for i := first; i < end; i++ {
					add(data, i, now)
				}
				return "loadtest.seeded", nil
			}); err != nil {
				return fmt.Errorf("seed batch %d-%d: %w", first, end, err)
			}
		}
	}
	return nil
}

func syntheticProject(creator domain.User, spec SyntheticMetadata, i int, now time.Time) domain.Project {
	id := fmt.Sprintf("lt-project-%05d", i)
	history := make([]domain.ProjectProgressHistoryPoint, 0, 30)
	for d := 0; d < 30; d++ {
		history = append(history, domain.ProjectProgressHistoryPoint{Date: now.AddDate(0, 0, -d), Value: float64(d)})
	}
	comments := make([]domain.Comment, 0, 4)
	for c := 0; c < 4; c++ {
		comments = append(comments, domain.Comment{ID: fmt.Sprintf("%s-c%d", id, c), Body: filler("comment", spec.ProjectBytes/16), User: creator, CreatedAt: now, Reactions: map[string][]string{}})
	}
	return domain.Project{
		ID: id, Name: fmt.Sprintf("Load project %d", i), SlugID: fmt.Sprintf("load-project-%d-%012x", i, i),
		Summary: filler("summary", 120), Description: filler("description", spec.ProjectBytes/3),
		Color: "#5E6AD2", Health: "onTrack", TeamIDs: []string{spec.TeamIDs[i%len(spec.TeamIDs)]}, MemberIDs: []string{creator.ID},
		LabelIDs: []string{}, DependencyIDs: []string{}, Initiatives: []string{}, Customers: []string{},
		Resources: []domain.ProjectResource{}, Milestones: []domain.ProjectMilestone{},
		Comments:             comments,
		DescriptionRevisions: []domain.ProjectDescriptionRevision{{ID: id + "-r1", ProjectID: id, Description: filler("revision", spec.ProjectBytes/6), Author: creator, CreatedAt: now}},
		IssueCountHistory:    history, ScopeHistory: history, ProgressHistory: history,
		CreatedAt: now, UpdatedAt: now,
	}
}

func syntheticTrash(creator domain.User, spec SyntheticMetadata, i int, now time.Time) domain.TrashEntry {
	payload, _ := json.Marshal(map[string]any{"id": fmt.Sprintf("lt-trash-res-%d", i), "content": filler("trash", spec.TrashBytes)})
	return domain.TrashEntry{ID: fmt.Sprintf("lt-trash-%04d", i), ResourceType: "document", ResourceID: fmt.Sprintf("lt-trash-res-%d", i), Title: fmt.Sprintf("Deleted %d", i), Payload: payload, TeamIDs: []string{spec.TeamIDs[i%len(spec.TeamIDs)]}, DeletedBy: creator, DeletedAt: now, ExpiresAt: now.AddDate(0, 1, 0)}
}

func syntheticDocument(creator domain.User, spec SyntheticMetadata, i int, now time.Time) domain.Document {
	return domain.Document{ID: fmt.Sprintf("lt-doc-%04d", i), SlugID: fmt.Sprintf("lt-doc-%d-%012x", i, i), Title: fmt.Sprintf("Load document %d", i), Content: filler("document", spec.DocumentBytes), Creator: creator, TeamIDs: []string{spec.TeamIDs[i%len(spec.TeamIDs)]}, CreatedAt: now, UpdatedAt: now}
}

func syntheticConversation(creator domain.User, i, size int, now time.Time) domain.AIConversation {
	return domain.AIConversation{ID: fmt.Sprintf("lt-conv-%04d", i), UserID: creator.ID, Title: fmt.Sprintf("Conversation %d", i), Status: "done", Context: map[string]any{"transcript": filler("chat", size)}, CreatedAt: now, UpdatedAt: now}
}

// WorkspaceStateBytes reports the stored state row size and the serialized
// size of the large metadata collections, for load-test reporting.
func (s *SQLiteStore) WorkspaceStateBytes(ctx context.Context, workspace string) (map[string]int, error) {
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
		return nil, err
	}
	data, ok := s.WorkspaceMetadata(workspace)
	if !ok {
		return nil, ErrAuthForbidden
	}
	size := func(v any) int { b, _ := json.Marshal(v); return len(b) }
	return map[string]int{"stateRow": len(raw), "projects": size(data.Projects), "trash": size(data.Trash), "documents": size(data.Documents), "conversations": size(data.AIConversations)}, nil
}
