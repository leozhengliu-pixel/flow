package main

import (
	"encoding/csv"
	"math"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Settings › AI & agents: the credit balance behind the Usage row and the usage feedback switch.

// usageFeedbackFlag is the workspace feature flag behind "Enable usage feedback".
const usageFeedbackFlag = "usage-feedback"

// usageFeedbackEnabled reports whether agent answers may be rated and annotated. On until an admin turns it off.
func usageFeedbackEnabled(settings domain.WorkspaceSettings) bool {
	return workspaceFeatureEnabled(settings, usageFeedbackFlag)
}

// aiCredits is the workspace's AI credit balance (Settings › AI & agents › Usage, Usage & limits › Workspace credits):
// the credits the deployment includes (FLOW_AI_INCLUDED_CREDITS_USD), the metered coding-session and loop spend since
// the workspace started, and what is left, never below zero. With nothing included the balance is $0.00, as in a
// workspace that has no credits.
type aiCredits struct {
	IncludedMicros  int64 `json:"includedMicros"`
	SpentMicros     int64 `json:"spentMicros"`
	RemainingMicros int64 `json:"remainingMicros"`
}

func aiCreditsView(included, spent int64) aiCredits {
	return aiCredits{IncludedMicros: included, SpentMicros: spent, RemainingMicros: max(0, included-spent)}
}

// includedAICreditsMicros reads FLOW_AI_INCLUDED_CREDITS_USD, a non-negative dollar amount. Unset, empty or invalid
// means no included credits.
func includedAICreditsMicros() int64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("FLOW_AI_INCLUDED_CREDITS_USD")), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > 1e9 {
		return 0
	}
	return int64(math.Round(value * 1e6))
}

// getAIUsageCredits answers GET /api/workspace/ai-credits for any workspace member.
func (s *server) getAIUsageCredits(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceSettingsMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if !s.authDisabled {
		_, status, err := s.store.WorkspaceRole(r.Context(), data.Workspace.ID, authUser(r).ID)
		if err != nil || status != "active" {
			writeError(w, http.StatusForbidden, "Workspace access required")
			return
		}
	}
	spent, err := s.store.CodingSpendMicros(r.Context(), data.Workspace.URLKey, "", "", time.Time{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read usage")
		return
	}
	writeJSON(w, http.StatusOK, aiCreditsView(includedAICreditsMicros(), spent))
}

// agentFeedbackEntry is one rating or note an agent user left, without the conversation's text.
type agentFeedbackEntry struct {
	SessionID string    `json:"sessionId"`
	MessageID string    `json:"messageId,omitempty"`
	UserID    string    `json:"userId"`
	UserName  string    `json:"userName,omitempty"`
	Scope     string    `json:"scope"` // reply | conversation
	Rating    string    `json:"rating,omitempty"`
	Comment   string    `json:"comment,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// listAgentFeedback answers GET /api/workspace/agent-feedback[?format=csv] for workspace admins: every usage feedback
// rating and note recorded in the workspace, newest first. It carries no conversation text.
func (s *server) listAgentFeedback(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkspaceAdmin(w, r) {
		return
	}
	data := s.workspaceData(r)
	entries := []agentFeedbackEntry{}
	for _, session := range data.AgentSessions {
		if session.DeletedAt != nil {
			continue
		}
		name := ""
		if user := userByID(&data, session.UserID); user != nil {
			name = user.Name
		}
		base := agentFeedbackEntry{SessionID: session.ID, UserID: session.UserID, UserName: name}
		for _, message := range session.Messages {
			if message.Feedback == nil {
				continue
			}
			entry := base
			entry.Scope, entry.MessageID = "reply", message.ID
			entry.Rating, entry.Comment, entry.UpdatedAt = message.Feedback.Rating, message.Feedback.Comment, message.Feedback.UpdatedAt
			entries = append(entries, entry)
		}
		for _, note := range session.Feedback {
			entry := base
			entry.Scope, entry.Comment, entry.UpdatedAt = "conversation", note.Comment, note.UpdatedAt
			entries = append(entries, entry)
		}
	}
	slices.SortStableFunc(entries, func(a, b agentFeedbackEntry) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	if r.URL.Query().Get("format") != "csv" {
		writeJSON(w, http.StatusOK, entries)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="agent-feedback.csv"`)
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"updated_at", "scope", "rating", "comment", "user_id", "user_name", "session_id", "message_id"})
	for _, entry := range entries {
		_ = writer.Write([]string{entry.UpdatedAt.UTC().Format(time.RFC3339), entry.Scope, entry.Rating, csvCell(entry.Comment), entry.UserID, csvCell(entry.UserName), entry.SessionID, entry.MessageID})
	}
	writer.Flush()
}

// csvCell keeps a spreadsheet from running user text as a formula.
func csvCell(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}
