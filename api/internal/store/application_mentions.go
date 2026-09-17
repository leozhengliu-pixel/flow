package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func syncApplicationMentions(ctx context.Context, tx *sqlTx, workspace, resource string, raw []byte) error {
	var comment domain.Comment
	if err := json.Unmarshal(raw, &comment); err != nil {
		return err
	}
	if comment.User.App || strings.HasPrefix(comment.User.ID, "app_") {
		return nil
	}
	ids := map[string]bool{}
	var visit func(any, int)
	visit = func(value any, depth int) {
		if depth > 64 {
			return
		}
		switch v := value.(type) {
		case map[string]any:
			if v["type"] == "mention" {
				if attrs, ok := v["attrs"].(map[string]any); ok {
					if id, ok := attrs["id"].(string); ok && strings.HasPrefix(id, "app_") {
						ids[id] = true
					}
				}
			}
			if content, ok := v["content"]; ok {
				visit(content, depth+1)
			}
		case []any:
			for _, child := range v {
				visit(child, depth+1)
			}
		}
	}
	visit(comment.BodyData, 0)
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > 20 {
		return ErrIssueQuery
	}
	var issueRaw []byte
	err := tx.QueryRowContext(ctx, `SELECT list_data FROM issue_records WHERE workspace_key=? AND id=?`, workspace, resource).Scan(&issueRaw)
	resourceType := "issue"
	teamIDs := []string{}
	if errors.Is(err, sql.ErrNoRows) {
		var field string
		err = tx.QueryRowContext(ctx, `SELECT field,data FROM workspace_metadata_records WHERE workspace_key=? AND record_key=? AND field IN ('documents','projects')`, workspace, resource).Scan(&field, &issueRaw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var metadata struct {
			TeamIDs    []string `json:"teamIds"`
			ProjectIDs []string `json:"projectIds"`
		}
		if err := json.Unmarshal(issueRaw, &metadata); err != nil {
			return err
		}
		teamIDs = metadata.TeamIDs
		if len(metadata.ProjectIDs) > 0 {
			clause, args := bindList("record_key", metadata.ProjectIDs)
			args = append([]any{workspace}, args...)
			rows, err := tx.QueryContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND `+clause, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var raw []byte
				var project domain.Project
				if err := rows.Scan(&raw); err != nil {
					rows.Close()
					return err
				}
				if err := json.Unmarshal(raw, &project); err != nil {
					rows.Close()
					return err
				}
				teamIDs = append(teamIDs, project.TeamIDs...)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
		}
		if field == "documents" {
			resourceType = "document"
		} else {
			resourceType = "project"
		}
	} else if err != nil {
		return err
	} else {
		var issue domain.Issue
		if err := json.Unmarshal(issueRaw, &issue); err != nil {
			return err
		}
		teamIDs = []string{issue.Team.ID}
	}
	var role string
	if err := tx.QueryRowContext(ctx, `SELECT m.role FROM workspace_memberships m JOIN workspace_states w ON w.workspace_id=m.workspace_id WHERE w.workspace_key=? AND m.user_id=? AND m.status='active'`, workspace, comment.User.ID).Scan(&role); err != nil {
		return err
	}
	if role == "guest" {
		var policy []byte
		if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='workspaceSettings' AND record_key='preventGuestAgents'`, workspace).Scan(&policy); err == nil && string(policy) == "true" {
			return nil
		}
	}
	for id := range ids {
		var app domain.ApplicationInstallation
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT data FROM application_installations WHERE workspace_key=? AND user_id=?`, workspace, id).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
			continue
		} else if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &app); err != nil {
			return err
		}
		teamID := ""
		for _, id := range teamIDs {
			if slices.Contains(app.TeamIDs, id) {
				teamID = id
				break
			}
		}
		if !app.Active || teamID == "" || !slices.Contains(app.Scopes, "app:mentionable") {
			continue
		}
		taskID := fmt.Sprintf("mention_%x", sha256.Sum256([]byte(workspace+":"+comment.ID+":"+id)))
		task := domain.AgentTask{ID: taskID, WorkspaceKey: workspace, ResourceType: resourceType, ResourceID: resource, TeamID: teamID, AppUserID: id, CreatorID: comment.User.ID, Status: "pending", Prompt: comment.Body, Trigger: "mention", Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if resourceType == "issue" {
			task.IssueID = resource
		}
		raw, err := json.Marshal(task)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO application_agent_tasks(id,workspace_key,issue_id,app_user_id,status,version,data,created_at) VALUES(?,?,?,?,?,1,?,?) ON CONFLICT(id) DO NOTHING`, task.ID, workspace, resource, id, "pending", raw, task.CreatedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return nil
}
