package main

import (
	"cmp"
	"encoding/json"
	"net/http"
	"slices"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (v *realtimeViewer) boundedProjectVisible(id string) bool {
	if v.err != nil {
		return false
	}
	read, found, err := v.s.store.BeginProjectVisibilityRead(v.r.Context(), workspaceKey(v.r), id)
	if err != nil {
		v.err = err
		return false
	}
	if !found {
		return false
	}
	defer read.Close()
	return read.Allowed && (len(read.Data.Projects[0].TeamIDs) == 0 || slices.ContainsFunc(read.Data.Projects[0].TeamIDs, v.content))
}

func (s *server) boundedPagedPolicyEvent(r *http.Request, data domain.Bootstrap, query store.IssueRecordQuery, event domain.RealtimeEvent) (domain.RealtimeEvent, bool, error) {
	if event.Type == "connected" || event.Type == "workspace.resync_required" {
		return event, true, nil
	}
	rule, _ := realtimeRuleFor(event.Type)
	viewer := &realtimeViewer{s: s, r: r, data: data, query: query, event: event}
	if !viewer.inAudience(rule) {
		return event, false, nil
	}
	if rule.anonymous {
		return anonymousRealtimeEvent(event), true, nil
	}
	if event.Type == "coding.approval_requested" && !s.codingSessionRealtimeVisible(r, event.AggregateID) {
		return event, false, nil
	}
	if rule.visible == nil {
		return realtimeSignal(rule, event, realtimeRecord{}), true, nil
	}
	record, found, err := s.boundedRealtimeRecordFor(r, data, rule, event)
	if err != nil {
		return event, false, err
	}
	projected, visible := projectPolicyRecord(viewer, rule, record, found)
	if viewer.err != nil {
		return event, false, viewer.err
	}
	return projected, visible, nil
}

func (s *server) boundedRealtimeRecordFor(r *http.Request, data domain.Bootstrap, rule realtimeRule, event domain.RealtimeEvent) (realtimeRecord, bool, error) {
	type cached struct {
		Found  bool           `json:"found"`
		Record realtimeRecord `json:"record"`
	}
	build := func() (json.RawMessage, error) {
		result := cached{}
		if rule.payloadTeam {
			var payload struct {
				TeamID string `json:"teamId"`
				State  struct {
					TeamID string `json:"teamId"`
				} `json:"state"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			if teamID := cmp.Or(payload.TeamID, payload.State.TeamID); teamID != "" {
				result = cached{Found: true, Record: realtimeRecord{TeamID: teamID}}
			}
		}
		if !result.Found && rule.teamAggregate && slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == event.AggregateID }) {
			result = cached{Found: true, Record: realtimeRecord{TeamID: event.AggregateID}}
		}
		for _, field := range rule.fields {
			if result.Found {
				break
			}
			read, found, err := s.store.BeginRealtimeRecordRead(r.Context(), workspaceKey(r), field, event.AggregateID)
			if err != nil {
				return nil, err
			}
			if !found {
				continue
			}
			defer read.Close()
			if read.Found {
				result = cached{Found: true, Record: realtimeRecord(read.Data)}
			}
		}
		return json.Marshal(result)
	}
	var raw json.RawMessage
	var err error
	if s.realtime == nil {
		raw, err = build()
	} else {
		raw, err = s.realtime.payloads.loadResult(event.ID+"|record", build)
	}
	if err != nil {
		return realtimeRecord{}, false, err
	}
	var result cached
	_ = json.Unmarshal(raw, &result)
	return result.Record, result.Found, nil
}

func (s *server) boundedAgentTaskRealtimeVisible(r *http.Request, taskID string) (bool, error) {
	workspace := workspaceKey(r)
	if workspace == "" {
		if metadata, ok := s.store.WorkspaceSettingsMetadata(""); ok {
			workspace = metadata.Workspace.URLKey
		}
	}
	task, err := s.store.AgentTask(r.Context(), workspace, taskID)
	if err != nil {
		return false, nil
	}
	switch task.ResourceType {
	case "document":
		if viewer := authUser(r); viewer.App && task.AppUserID != viewer.ID {
			return false, nil
		}
		return s.boundedLegacyDocumentPresenceAllowed(r, task.ResourceID)
	case "project":
		if viewer := authUser(r); viewer.App && task.AppUserID != viewer.ID {
			return false, nil
		}
		read, found, err := s.store.BeginProjectVisibilityRead(r.Context(), workspace, task.ResourceID)
		if err != nil {
			return false, err
		}
		if !found {
			return false, nil
		}
		defer read.Close()
		if !read.Allowed {
			return false, nil
		}
		_, query, err := s.pagedRealtimeMetadata(r)
		if err != nil {
			return false, err
		}
		project := read.Data.Projects[0]
		access := query.Access
		return access == nil || access.Admin || len(project.TeamIDs) == 0 || slices.ContainsFunc(project.TeamIDs, func(id string) bool { return slices.Contains(access.VisibleTeamIDs, id) }), nil
	}
	_, _, err = s.authorizedAgentTask(r, taskID, false)
	return err == nil, nil
}
