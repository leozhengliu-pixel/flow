package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) generalBootstrapBounded(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	s.maintainCycleSchedule(r.Context(), workspaceKey(r))
	s.maintainAdvancedSchedules(r.Context(), workspaceKey(r))
	userID := ""
	if !s.authDisabled {
		userID = authUser(r).ID
	}
	var credential *domain.APIKey
	if key, found := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); found {
		credential = &key
	}
	read, err := s.store.BeginGeneralBootstrapRead(r.Context(), workspaceKey(r), userID, credential, func(snapshot context.Context) {
		s.setChangeCursor(w, r.WithContext(snapshot), workspaceKey(r))
	})
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	defer read.Close()
	r = r.WithContext(read.Context(r.Context()))
	data := read.Data
	filterBootstrapForAPIKey(&data, r)
	if err := s.filterPreferenceIssueTeams(r, &data); err != nil {
		issueRecordsError(w, err)
		return
	}
	if slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool { return issue.AgentSessionID != "" }) {
		// Decorate a copy: the issue slice may be shared with the store snapshot.
		data.Issues = slices.Clone(data.Issues)
		if err := s.decorateAgentSessionStates(r.Context(), data.Workspace.URLKey, data.Issues); err != nil {
			issueRecordsError(w, err)
			return
		}
	}
	if projectListBootstrapRequested(r) {
		r = captureBootstrapCredentialVisibility(r, data)
		store.ProjectListBootstrapProjection(&data)
	} else if err := s.store.PopulateReleaseProgress(r.Context(), &data); err != nil {
		issueRecordsError(w, err)
		return
	}
	// Before development members are synthesized, so both bootstraps count the same team members.
	if err := s.decorateCycleCapacity(r.Context(), &data); err != nil {
		issueRecordsError(w, err)
		return
	}
	if s.authDisabled {
		if err := s.store.ApplyTeamDefaultFavorites(r.Context(), &data, data.Viewer.ID); err != nil {
			issueRecordsError(w, err)
			return
		}
		materializeDevelopmentMembers(&data)
	}
	if err := scaleP21SanitizeBootstrap(r.Context(), &data, read.StreamString); err != nil {
		issueRecordsError(w, err)
		return
	}
	s.applyCalendarAway(r.Context(), &data)
	s.applyLoopRunCounts(r.Context(), data.Workspace.URLKey, data.Loops)
	if leanBootstrapRequested(r) {
		// Everything before the streamed body; the body is a pure encode of data.
		w.Header().Set("Server-Timing", fmt.Sprintf("lean;dur=%.2f", float64(time.Since(started).Microseconds())/1000))
	} else {
		w.Header().Set("Server-Timing", fmt.Sprintf("bootstrap;dur=%.2f", float64(time.Since(started).Microseconds())/1000))
	}
	read.Data = data
	s.writeGeneralBootstrapBounded(w, r, data, read)
}

func (s *server) writeGeneralBootstrapBounded(w http.ResponseWriter, r *http.Request, data domain.Bootstrap, read *store.GeneralBootstrapRead) {
	ctx := hydratedSigningContext(r.Context(), w)
	const mapLimit uint64 = 256 << 20
	var mapWork uint64
	reserve := func(bytes uint64) error {
		if bytes > mapLimit-mapWork {
			return store.ErrMetadataWorkBudget
		}
		if err := read.ReserveOutputWork(ctx, bytes); err != nil {
			return err
		}
		mapWork += bytes
		return nil
	}
	entry := func(id string, base uint64) error {
		if base > mapLimit || uint64(len(id)) > (mapLimit-base)/8 {
			return store.ErrMetadataWorkBudget
		}
		return reserve(base + uint64(len(id))*8)
	}
	// Bases and entries cover the map buckets and copied values. Input
	// strings still belong to Data; retained walker keys are cloned below.
	if err := reserve(4 * 4096); err != nil {
		issueRecordsError(w, err)
		return
	}
	visible := map[string]bool{}
	visibleIssues := map[string]bool{}
	for _, issue := range data.Issues {
		if err := entry(issue.ID, 1024); err != nil {
			issueRecordsError(w, err)
			return
		}
		visible[issue.ID] = true
		visibleIssues[issue.ID] = true
	}
	for _, document := range data.Documents {
		if err := entry(document.ID, 512); err != nil {
			issueRecordsError(w, err)
			return
		}
		visible[document.ID] = true
	}
	for _, project := range data.Projects {
		if err := entry(project.ID, 512); err != nil {
			issueRecordsError(w, err)
			return
		}
		visible[project.ID] = true
		for _, milestone := range project.Milestones {
			if err := entry(milestone.ID, 512); err != nil {
				issueRecordsError(w, err)
				return
			}
			visible[milestone.ID] = true
		}
	}
	users := map[string]domain.User{}
	for _, user := range data.Users {
		if err := entry(user.ID, 4096); err != nil {
			issueRecordsError(w, err)
			return
		}
		users[user.ID] = user
	}
	key, credential := r.Context().Value(apiKeyContextKey{}).(domain.APIKey)
	scoped := credential && apiKeyTeamRestrictionSelected(key)
	captured, hasCaptured := r.Context().Value(bootstrapCredentialVisibilityKey{}).(bootstrapCredentialVisibility)
	resourceVisible := func(kind, id string) bool {
		if hasCaptured {
			return captured[kind][id]
		}
		if kind == "issue" {
			return visibleIssues[id]
		}
		return credentialResourceExists(&data, kind, id)
	}
	visibleNotification := func(item domain.Notification) bool {
		if item.IssueID != "" {
			return resourceVisible("issue", item.IssueID)
		}
		if item.ProjectID != "" {
			return resourceVisible("project", item.ProjectID)
		}
		if item.ReviewID != "" {
			return resourceVisible("review", item.ReviewID)
		}
		switch item.SourceType {
		case "issue", "project", "initiative", "document", "cycle", "team":
			return resourceVisible(item.SourceType, item.SourceID)
		}
		return true
	}
	visibleNotifications := map[string]bool{}
	if scoped {
		if err := read.WalkContentRecords(ctx, data.Workspace.URLKey, "notification", func(_ string, raw json.RawMessage) error {
			var item domain.Notification
			if err := json.Unmarshal(raw, &item); err != nil {
				return err
			}
			if item.RecipientID == data.Viewer.ID && item.DeletedAt == nil && item.ArchivedAt == nil && visibleNotification(item) {
				if err := entry(item.ID, 512); err != nil {
					return err
				}
				visibleNotifications[strings.Clone(item.ID)] = true
			}
			return nil
		}); err != nil {
			issueRecordsError(w, err)
			return
		}
	}
	lean := leanBootstrapRequested(r)
	field := func(field string, buffer *bufio.Writer, encoder *json.Encoder) (bool, error) {
		if lean {
			switch field {
			case "issues":
				// The outline already is the list projection: no per-issue read.
				buffer.WriteByte('[')
				for i, issue := range data.Issues {
					if i > 0 {
						buffer.WriteByte(',')
					}
					if err := encodeBootstrapValue(ctx, buffer, encoder, leanIssueRecord(issue)); err != nil {
						return true, err
					}
				}
				buffer.WriteByte(']')
				return true, nil
			case "comments", "activities":
				buffer.WriteString("{}")
				return true, nil
			}
		}
		if field == "issues" {
			buffer.WriteByte('[')
			count := 0
			if err := reserve(4096); err != nil {
				return true, err
			}
			outlines := map[string]int{}
			for index, issue := range data.Issues {
				if err := entry(issue.ID, 512); err != nil {
					return true, err
				}
				outlines[issue.ID] = index
			}
			err := read.WalkIssueRecords(ctx, store.IssueRecordQuery{Workspace: data.Workspace.URLKey, Archived: "all"}, func(issue domain.Issue) error {
				index, ok := outlines[issue.ID]
				if !ok {
					return nil
				}
				outline := data.Issues[index]
				issue.Team = outline.Team
				issue.State = outline.State
				issue.Creator = outline.Creator
				issue.Assignee = outline.Assignee
				issue.Delegate = outline.Delegate
				issue.Project = outline.Project
				issue.ParentID = outline.ParentID
				issue.SubIssueIDs = outline.SubIssueIDs
				issue.Relations = outline.Relations
				issue.Labels = outline.Labels
				if scoped {
					issue.CycleID = outline.CycleID
					issue.ProjectMilestoneID = outline.ProjectMilestoneID
					issue.SuggestedLabelIDs = outline.SuggestedLabelIDs
					issue.SuggestedProjectIDs = outline.SuggestedProjectIDs
					issue.SuggestedTeamIDs = outline.SuggestedTeamIDs
					issue.SuggestedDuplicateIDs = outline.SuggestedDuplicateIDs
					issue.SuggestedRelatedIDs = outline.SuggestedRelatedIDs
				}
				// The outline carries the decorated agent task state (issue records store it empty).
				issue.AgentSessionState = outline.AgentSessionState
				issue.IsSummary = false
				if count > 0 {
					buffer.WriteByte(',')
				}
				if err := encodeBootstrapValue(ctx, buffer, encoder, issue); err != nil {
					return err
				}
				count++
				return nil
			})
			if err != nil {
				return true, err
			}
			buffer.WriteByte(']')
			return true, nil
		}
		kind := ""
		object := false
		switch field {
		case "comments":
			kind = "comment"
			object = true
		case "activities":
			kind = "activity"
			object = true
		case "notifications":
			kind = "notification"
		case "notificationDeliveries":
			kind = "delivery"
		default:
			return false, nil
		}
		if object {
			buffer.WriteByte('{')
		} else {
			buffer.WriteByte('[')
		}
		resourceKey := ""
		haveResource := false
		count := 0
		err := read.WalkContentRecords(ctx, data.Workspace.URLKey, kind, func(resource string, raw json.RawMessage) error {
			var value any
			switch kind {
			case "comment":
				if !visible[resource] {
					return nil
				}
				var item domain.Comment
				if err := json.Unmarshal(raw, &item); err != nil {
					return err
				}
				if user, ok := users[item.User.ID]; ok {
					item.User = user
				}
				value = item
			case "activity":
				if !visible[resource] {
					return nil
				}
				var item domain.ActivityEvent
				if err := json.Unmarshal(raw, &item); err != nil {
					return err
				}
				if user, ok := users[item.Actor.ID]; ok {
					item.Actor = user
				}
				value = item
			case "notification":
				var item domain.Notification
				if err := json.Unmarshal(raw, &item); err != nil {
					return err
				}
				// The inbox only shows live rows; deleted and archived ones are
				// history. Sending them here let a cached snapshot keep them.
				if item.DeletedAt != nil || item.ArchivedAt != nil {
					return nil
				}
				if !s.authDisabled && item.RecipientID != data.Viewer.ID {
					return nil
				}
				if scoped && !visibleNotifications[item.ID] {
					return nil
				}
				if user, ok := users[item.Actor.ID]; ok {
					item.Actor = user
				}
				value = item
			case "delivery":
				var item domain.NotificationDelivery
				if err := json.Unmarshal(raw, &item); err != nil {
					return err
				}
				if !s.authDisabled && item.RecipientID != data.Viewer.ID {
					return nil
				}
				if scoped && !visibleNotifications[item.NotificationID] {
					return nil
				}
				value = item
			}
			if object && (!haveResource || resource != resourceKey) {
				if err := entry(resource, 256); err != nil {
					return err
				}
				if haveResource {
					buffer.WriteString("],")
				}
				if err := encoder.Encode(resource); err != nil {
					return err
				}
				buffer.WriteString(":[")
				resourceKey = strings.Clone(resource)
				haveResource = true
				count = 0
			}
			if count > 0 {
				buffer.WriteByte(',')
			}
			count++
			return encodeBootstrapValue(ctx, buffer, encoder, value)
		})
		if err != nil {
			return true, err
		}
		if object {
			if haveResource {
				buffer.WriteByte(']')
			}
			buffer.WriteByte('}')
		} else {
			buffer.WriteByte(']')
		}
		return true, nil
	}
	signing, _ := ctx.Value(hydratedURLSigningKey{}).(*hydratedURLSigning)
	var rewrite store.MetadataJSONRewriter
	if signing != nil && signing.key != nil {
		rewrite = func(ctx context.Context, source store.MetadataJSONSource, dst io.Writer) error {
			return rewriteHydratedURLSource(ctx, source, source.Size(), dst, signing)
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	prepareHydratedBootstrapSpool(w, data)
	if err := read.WriteJSONWithFields(ctx, w, field, rewrite); err != nil {
		log.Printf("encode bounded general bootstrap: %v", err)
		panic(http.ErrAbortHandler)
	}
}
