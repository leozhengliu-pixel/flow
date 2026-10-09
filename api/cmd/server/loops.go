package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"flow/api/internal/domain"
)

type loopInput struct {
	Name                       *string         `json:"name,omitempty"`
	Description                *string         `json:"description,omitempty"`
	Status                     *string         `json:"status,omitempty"`
	Icon                       *string         `json:"icon,omitempty"`
	Color                      *string         `json:"color,omitempty"`
	Level                      *string         `json:"level,omitempty"`
	TeamID                     *string         `json:"teamId,omitempty"`
	TemplateID                 *string         `json:"templateId,omitempty"`
	Prompt                     *string         `json:"prompt,omitempty"`
	TriggerType                *string         `json:"triggerType,omitempty"`
	TriggerConfig              map[string]any  `json:"triggerConfig,omitempty"`
	Instructions               *string         `json:"instructions,omitempty"`
	InstructionsData           json.RawMessage `json:"instructionsData,omitempty"`
	AttachmentIDs              []string        `json:"attachmentIds,omitempty"`
	ConnectorIDs               *[]string       `json:"connectorIds,omitempty"`
	TeamAccess                 *string         `json:"teamAccess,omitempty"`
	AllowChangesOutsideTrigger *bool           `json:"allowChangesOutsideTrigger,omitempty"`
	AllowExternalSync          *bool           `json:"allowExternalSync,omitempty"`
	AllowExternalSyncChanges   *bool           `json:"allowExternalSyncChanges,omitempty"`
	WebSearch                  *bool           `json:"webSearch,omitempty"`
	CodeAccess                 *string         `json:"codeAccess,omitempty"`
	Enabled                    *bool           `json:"enabled,omitempty"`
	OwnerID                    *string         `json:"ownerId,omitempty"`
	EditPolicy                 *string         `json:"editPolicy,omitempty"`
	TrustedSourceKeys          *[]string       `json:"trustedSourceKeys,omitempty"`
	// ExpectedOutputs: [] infers from the instructions, ["none"] expects no
	// output, else statusUpdate, issue, comment, document or change.
	ExpectedOutputs *[]string `json:"expectedOutputs,omitempty"`
}

var loopTriggerTypes = []string{"schedule", "issue", "project", "initiative", "release", "team", "cycle"}

// loopEditPolicies are Linear's "Who can edit this loop" options.
var loopEditPolicies = []string{"all", "teamOwners", "owner"}

// errLoopEditForbidden rejects changes from members the loop's "Who can edit"
// policy leaves out.
var errLoopEditForbidden = errors.New("you don't have permission to edit this loop")

// canEditLoop applies the loop's "Who can edit" policy. Workspace admins and
// the loop owner can always edit; "teamOwners" admits the owners of the loop's
// team (only admins for workspace loops); "owner" admits nobody else.
func canEditLoop(data *domain.Bootstrap, loop domain.Loop, userID string) bool {
	if workspaceAdminRole(data.ViewerRole) {
		return true
	}
	owner := loop.OwnerID
	if owner == "" {
		owner = loop.Creator.ID
	}
	if userID != "" && userID == owner {
		return true
	}
	switch loop.EditPolicy {
	case "owner":
		return false
	case "teamOwners":
		return loop.Level == "team" && loop.TeamID != "" && teamRoleForUser(data, loop.TeamID, userID) == "owner"
	}
	return true
}

// checkLoopEditor is canEditLoop for the request's viewer; local development
// without authentication edits everything.
func (s *server) checkLoopEditor(data *domain.Bootstrap, loop domain.Loop) error {
	if s.authDisabled || canEditLoop(data, loop, data.Viewer.ID) {
		return nil
	}
	return errLoopEditForbidden
}

// respondLoopMutation is respondMutation with the "Who can edit" refusal spelled out.
func respondLoopMutation(w http.ResponseWriter, err error, success int, value any) {
	if errors.Is(err, errLoopEditForbidden) {
		writeError(w, http.StatusForbidden, "Only the people this loop's \"Who can edit\" setting allows can change it")
		return
	}
	respondMutation(w, err, success, value)
}

func validateLoopInput(input loopInput) error {
	if input.TriggerType != nil && !slices.Contains(loopTriggerTypes, *input.TriggerType) {
		return fmt.Errorf("invalid trigger type")
	}
	if input.Level != nil && *input.Level != "workspace" && *input.Level != "team" {
		return fmt.Errorf("invalid loop level")
	}
	if input.Status != nil && *input.Status != "draft" && *input.Status != "published" {
		return fmt.Errorf("status must be draft or published")
	}
	if input.TeamAccess != nil && !slices.Contains([]string{"allPublic", "selected"}, *input.TeamAccess) {
		return fmt.Errorf("invalid team access")
	}
	if input.CodeAccess != nil && !slices.Contains([]string{"disabled", "read", "readWrite"}, *input.CodeAccess) {
		return fmt.Errorf("codeAccess must be disabled, read, or readWrite")
	}
	if input.EditPolicy != nil && !slices.Contains(loopEditPolicies, *input.EditPolicy) {
		return fmt.Errorf("editPolicy must be all, teamOwners, or owner")
	}
	if input.TemplateID != nil && *input.TemplateID != "" && loopTemplateByID(*input.TemplateID) == nil {
		return fmt.Errorf("unknown loop template")
	}
	if len(input.InstructionsData) > 0 && string(input.InstructionsData) != "null" {
		var document map[string]any
		if json.Unmarshal(input.InstructionsData, &document) != nil || document == nil {
			return fmt.Errorf("instructionsData must be a ProseMirror document object")
		}
		if len(input.InstructionsData) > 512<<10 {
			return fmt.Errorf("instructionsData is too large")
		}
	}
	if len(input.AttachmentIDs) > 10 {
		return fmt.Errorf("attach at most 10 files")
	}
	if input.ExpectedOutputs != nil {
		for _, item := range *input.ExpectedOutputs {
			if item != "none" && !slices.Contains(loopOutputKinds, item) {
				return fmt.Errorf("expectedOutputs items must be none, %s", strings.Join(loopOutputKinds, ", "))
			}
		}
		if slices.Contains(*input.ExpectedOutputs, "none") && len(*input.ExpectedOutputs) > 1 {
			return fmt.Errorf("expectedOutputs cannot combine none with other outputs")
		}
	}
	return nil
}

func defaultLoopTriggerConfig(triggerType string, now time.Time) map[string]any {
	if triggerType == "schedule" {
		return map[string]any{"startDate": now.Format("2006-01-02"), "interval": 1, "unit": "day", "time": "10:00"}
	}
	return map[string]any{"event": "created"}
}

func applyLoopInput(loop *domain.Loop, input loopInput) {
	if input.Name != nil {
		loop.Name = strings.TrimSpace(*input.Name)
	}
	if input.Description != nil {
		loop.Description = strings.TrimSpace(*input.Description)
	}
	if input.Icon != nil {
		loop.Icon = strings.TrimSpace(*input.Icon)
	}
	if input.Color != nil {
		loop.Color = strings.TrimSpace(*input.Color)
	}
	if input.Level != nil {
		loop.Level = *input.Level
		if loop.Level == "workspace" {
			loop.TeamID = ""
		}
	}
	if input.TeamID != nil {
		loop.TeamID = strings.TrimSpace(*input.TeamID)
		if loop.TeamID != "" && input.Level == nil {
			loop.Level = "team"
		}
	}
	if input.TriggerType != nil && *input.TriggerType != loop.TriggerType {
		loop.TriggerType = *input.TriggerType
		loop.TriggerConfig = defaultLoopTriggerConfig(loop.TriggerType, time.Now().UTC())
		loop.NextRunAt = nil
	}
	if input.TriggerConfig != nil {
		loop.TriggerConfig = normalizeLoopTriggerConfig(loop.TriggerType, input.TriggerConfig)
		// The schedule is recomputed from the new configuration.
		loop.NextRunAt = nil
	}
	if input.Instructions != nil {
		loop.Instructions = *input.Instructions
	}
	if len(input.InstructionsData) > 0 {
		var document map[string]any
		if string(input.InstructionsData) != "null" {
			_ = json.Unmarshal(input.InstructionsData, &document)
		}
		loop.InstructionsData = document
		// Markdown stays authoritative; derive it when only the document was sent.
		if input.Instructions == nil && document != nil {
			loop.Instructions = proseMirrorPlainText(document)
		}
	}
	if input.ConnectorIDs != nil {
		loop.ConnectorIDs = slices.Clone(*input.ConnectorIDs)
	}
	if input.TeamAccess != nil {
		loop.TeamAccess = *input.TeamAccess
	}
	if input.AllowChangesOutsideTrigger != nil {
		loop.AllowChangesOutsideTrigger = *input.AllowChangesOutsideTrigger
	}
	if input.AllowExternalSyncChanges != nil {
		loop.AllowExternalSync = *input.AllowExternalSyncChanges
	}
	if input.AllowExternalSync != nil {
		loop.AllowExternalSync = *input.AllowExternalSync
	}
	if input.WebSearch != nil {
		loop.WebSearch = *input.WebSearch
	}
	if input.ExpectedOutputs != nil {
		loop.ExpectedOutputs = slices.Compact(slices.Clone(*input.ExpectedOutputs))
		if len(loop.ExpectedOutputs) == 0 {
			loop.ExpectedOutputs = nil
		}
	}
	if input.CodeAccess != nil {
		loop.CodeAccess = *input.CodeAccess
	}
	if input.Enabled != nil {
		loop.Enabled = *input.Enabled
	}
	if input.OwnerID != nil {
		loop.OwnerID = strings.TrimSpace(*input.OwnerID)
	}
	if input.TrustedSourceKeys != nil {
		loop.TrustedSourceKeys = slices.Clone(*input.TrustedSourceKeys)
	}
	if input.EditPolicy != nil {
		loop.EditPolicy = *input.EditPolicy
		if loop.EditPolicy == "all" {
			loop.EditPolicy = ""
		}
	}
}

// repairLoopLevel fixes team loops saved without their team (loops created
// before the level picker existed): the trigger's single team becomes the loop's
// team, otherwise the loop is workspace-level. Without this every edit of such
// a loop, even disabling it, fails validation.
func repairLoopLevel(loop *domain.Loop) {
	if loop.Level != "team" || loop.TeamID != "" {
		return
	}
	if ids, _ := loop.TriggerConfig["teamIds"].([]any); len(ids) == 1 {
		if id, _ := ids[0].(string); id != "" {
			loop.TeamID = id
			return
		}
	}
	loop.Level = "workspace"
}

// checkLoop validates a loop after an input was applied. Publishing also
// needs a name and instructions.
func checkLoop(data *domain.Bootstrap, loop domain.Loop, publishing bool) error {
	if loop.Level == "team" {
		if loop.TeamID == "" {
			return fmt.Errorf("%w: choose the team this loop belongs to", errInvalid)
		}
		if !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == loop.TeamID }) {
			return fmt.Errorf("%w: team not found", errInvalid)
		}
	}
	if template := loopTemplateByID(loop.TemplateID); template != nil && template.RequiresTeam && loop.Level != "team" {
		return fmt.Errorf("%w: %s", errInvalid, template.LevelHint)
	}
	if err := validateLoopTriggerConfig(loop.TriggerType, normalizeLoopTriggerConfig(loop.TriggerType, loop.TriggerConfig)); err != nil {
		return fmt.Errorf("%w: %s", errInvalid, err.Error())
	}
	if loop.Status == "draft" && loop.Enabled {
		return fmt.Errorf("%w: create the loop before enabling it", errInvalid)
	}
	if loop.Status != "draft" && strings.TrimSpace(loop.Name) == "" {
		return fmt.Errorf("%w: name is required", errInvalid)
	}
	if publishing && strings.TrimSpace(loop.Instructions) == "" {
		return fmt.Errorf("%w: instructions are required", errInvalid)
	}
	if loop.OwnerID == "" || userByID(data, loop.OwnerID) == nil {
		return fmt.Errorf("%w: owner not found", errInvalid)
	}
	return nil
}

// presentLoop returns a loop in the current API shape: legacy trigger
// configurations mapped, defaults filled and the 30-day run count computed.
// presentLoop fills defaults for loops stored before a setting existed and the
// 30-day run count (counts: runs per loop id; nil when not loaded).
func presentLoop(counts map[string]int, loop domain.Loop) domain.Loop {
	if loop.Status == "" {
		loop.Status = "published"
	}
	loop.CodeAccess = loopCodeAccess(loop)
	if loop.Status == "published" && loop.Version == 0 {
		loop.Version, loop.VersionID = 1, loopVersionID(loop.ID, 1)
	}
	loop.TriggerConfig = normalizeLoopTriggerConfig(loop.TriggerType, loop.TriggerConfig)
	if loop.ConnectorIDs == nil {
		loop.ConnectorIDs = []string{}
	}
	if loop.Description == "" && loop.Status == "published" {
		loop.Description = fallbackLoopDescription(loop.Instructions)
	}
	loop.RunCount30d = counts[loop.ID]
	return loop
}

func (s *server) listLoops(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceMetadataFields(workspaceKey(r), "loops")
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	counts := s.loopRunCounts(r.Context(), data.Workspace.URLKey)
	loops := make([]domain.Loop, 0, len(data.Loops))
	for _, loop := range data.Loops {
		loops = append(loops, presentLoop(counts, loop))
	}
	writeJSON(w, http.StatusOK, loops)
}

func (s *server) getLoop(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceMetadataFields(workspaceKey(r), "loops")
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if loop := loopByID(&data, r.PathValue("id")); loop != nil {
		writeJSON(w, http.StatusOK, presentLoop(s.loopRunCounts(r.Context(), data.Workspace.URLKey), *loop))
		return
	}
	writeError(w, http.StatusNotFound, "loop not found")
}

func (s *server) listLoopTemplates(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	templates := make([]loopTemplate, 0, len(loopTemplates))
	for _, template := range loopTemplates {
		template.TriggerConfig = template.config(now)
		templates = append(templates, template)
	}
	writeJSON(w, http.StatusOK, templates)
}

// createLoop creates a draft ("Create a new loop"), optionally from a
// template or a prompt. Passing status "published" creates a live loop.
func (s *server) createLoop(w http.ResponseWriter, r *http.Request) {
	var input loopInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := validateLoopInput(input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var created domain.Loop
	var referenced []domain.Issue
	if len(input.InstructionsData) == 0 {
		markdowns := []string{}
		if input.Instructions != nil {
			markdowns = append(markdowns, *input.Instructions)
		}
		if input.TemplateID != nil && *input.TemplateID != "" {
			markdowns = append(markdowns, loopTemplateByID(*input.TemplateID).Instructions)
		}
		referenced = s.loopReferencedIssues(r, markdowns...)
	}
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "loop.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		created = domain.Loop{ID: fmt.Sprintf("loop_%d", now.UnixNano()), Status: "draft", Icon: "Automation", Color: "#d9b84b", Level: "workspace", TriggerType: "schedule", TriggerConfig: defaultLoopTriggerConfig("schedule", now), ConnectorIDs: []string{}, TeamAccess: "allPublic", AllowChangesOutsideTrigger: true, CodeAccess: "disabled", OwnerID: data.Viewer.ID, Creator: data.Viewer, CreatedAt: now, UpdatedAt: now}
		if input.TemplateID != nil && *input.TemplateID != "" {
			template := loopTemplateByID(*input.TemplateID)
			created.TemplateID, created.Name, created.Description, created.Icon, created.Color = template.ID, template.Name, template.Description, template.Icon, template.Color
			created.TriggerType, created.TriggerConfig, created.Instructions = template.TriggerType, template.config(now), template.Instructions
			if template.CodeAccess != "" {
				created.CodeAccess = template.CodeAccess
			}
		}
		if input.Prompt != nil {
			created.SourcePrompt = strings.TrimSpace(*input.Prompt)
		}
		publish := input.Status != nil && *input.Status == "published"
		applyLoopInput(&created, input)
		if len(input.InstructionsData) == 0 && strings.TrimSpace(created.Instructions) != "" {
			created.InstructionsData = loopMarkdownDocumentWithIssues(data, created.Instructions, referenced)
		}
		if publish && input.Enabled == nil {
			created.Enabled = true
		}
		if publish {
			created.Status, created.PublishedAt = "published", &now
			if input.Description == nil && created.Description == "" {
				created.Description = fallbackLoopDescription(created.Instructions)
			}
		}
		if created.TriggerConfig == nil {
			created.TriggerConfig = defaultLoopTriggerConfig(created.TriggerType, now)
		}
		if created.OwnerID == "" {
			created.OwnerID = data.Viewer.ID
		}
		repairLoopLevel(&created)
		if err := checkLoop(data, created, publish); err != nil {
			return "", err
		}
		if len(input.AttachmentIDs) > 0 {
			attachments, err := claimLoopAttachments(data, input.AttachmentIDs, created.ID)
			if err != nil {
				return "", err
			}
			created.Attachments = attachments
		}
		if publish {
			recordLoopVersion(data, nil, &created, now, 0)
		}
		data.Loops = append([]domain.Loop{created}, data.Loops...)
		appendAudit(data, "created", "loop", created.ID, map[string]any{"triggerType": created.TriggerType, "status": created.Status})
		return created.ID, nil
	})
	if err == nil && created.Status == "published" && input.Description == nil {
		s.describeLoopAsync(workspaceKey(r), created)
	}
	respondMutation(w, err, http.StatusCreated, presentLoop(nil, created))
}

func (s *server) updateLoop(w http.ResponseWriter, r *http.Request) {
	var input loopInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := validateLoopInput(input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	var updated domain.Loop
	describe := false
	var referenced []domain.Issue
	if input.Instructions != nil && len(input.InstructionsData) == 0 {
		referenced = s.loopReferencedIssues(r, *input.Instructions)
	}
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "loop.updated", id, input, func(data *domain.Bootstrap) error {
		loop := loopByID(data, id)
		if loop == nil {
			return errNotFound
		}
		if err := s.checkLoopEditor(data, *loop); err != nil {
			return err
		}
		wasDraft := loop.Status == "draft"
		if !wasDraft && input.Status != nil && *input.Status == "draft" {
			return fmt.Errorf("%w: a created loop cannot go back to draft", errInvalid)
		}
		previousInstructions := loop.Instructions
		next := *loop
		applyLoopInput(&next, input)
		// Markdown written without the editor (e.g. by the loop builder agent)
		// replaces the rich document, which would otherwise go stale.
		if input.Instructions != nil && len(input.InstructionsData) == 0 {
			next.InstructionsData = nil
			if strings.TrimSpace(next.Instructions) != "" {
				next.InstructionsData = loopMarkdownDocumentWithIssues(data, next.Instructions, referenced)
			}
		}
		if input.Status != nil {
			next.Status = *input.Status
		}
		now := time.Now().UTC()
		if wasDraft && next.Status == "published" {
			next.PublishedAt = &now
			if input.Enabled == nil {
				next.Enabled = true
			}
		}
		if next.OwnerID == "" {
			next.OwnerID = next.Creator.ID
		}
		repairLoopLevel(&next)
		if err := checkLoop(data, next, wasDraft && next.Status == "published"); err != nil {
			return err
		}
		if next.Status == "published" && input.Description == nil && (wasDraft || next.Instructions != previousInstructions) {
			describe = true
			if next.Description == "" || next.Instructions != previousInstructions {
				next.Description = fallbackLoopDescription(next.Instructions)
			}
		}
		next.UpdatedAt = now
		if wasDraft {
			recordLoopVersion(data, nil, &next, now, 0)
		} else {
			recordLoopVersion(data, loop, &next, now, 0)
		}
		*loop = next
		updated = next
		appendAudit(data, "updated", "loop", id, nil)
		return nil
	})
	if err == nil && describe {
		s.describeLoopAsync(workspaceKey(r), updated)
	}
	respondLoopMutation(w, err, http.StatusOK, presentLoop(s.loopRunCounts(r.Context(), s.workspaceURLKey(r)), updated))
}

// duplicateLoop copies a loop into a new draft ("⋯ › Duplicate").
func (s *server) duplicateLoop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var created domain.Loop
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "loop.duplicated", map[string]string{"sourceId": id}, func(data *domain.Bootstrap) (string, error) {
		source := loopByID(data, id)
		if source == nil {
			return "", errNotFound
		}
		now := time.Now().UTC()
		created = *source
		created.ID = fmt.Sprintf("loop_%d", now.UnixNano())
		created.Name = strings.TrimSpace(source.Name + " (copy)")
		created.Status, created.Enabled = "draft", false
		created.TriggerConfig = normalizeLoopTriggerConfig(source.TriggerType, source.TriggerConfig)
		created.ConnectorIDs = slices.Clone(source.ConnectorIDs)
		created.TrustedSourceKeys = slices.Clone(source.TrustedSourceKeys)
		created.CodeAccess = loopCodeAccess(*source)
		created.OwnerID, created.Creator = data.Viewer.ID, data.Viewer
		created.LastRunAt, created.NextRunAt, created.PublishedAt = nil, nil, nil
		created.Version, created.VersionID, created.Attachments = 0, "", nil
		created.InstructionsData = cloneJSONMap(source.InstructionsData)
		created.CreatedAt, created.UpdatedAt = now, now
		data.Loops = append([]domain.Loop{created}, data.Loops...)
		appendAudit(data, "created", "loop", created.ID, map[string]any{"duplicateOf": id})
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, presentLoop(nil, created))
}

func (s *server) deleteLoop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "loop.deleted", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Loops, func(item domain.Loop) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		item := data.Loops[index]
		if err := s.checkLoopEditor(data, item); err != nil {
			return err
		}
		if err := appendTrash(data, "loop", item.ID, item.Name, item); err != nil {
			return err
		}
		data.Loops = slices.Delete(data.Loops, index, index+1)
		data.LoopVersions = slices.DeleteFunc(data.LoopVersions, func(version domain.LoopVersion) bool { return version.LoopID == id })
		return nil
	})
	if err == nil {
		if err := s.store.DeleteLoopRuns(r.Context(), s.workspaceURLKey(r), id); err != nil {
			log.Printf("Loop run cleanup workspace=%s loop=%s: %v", s.workspaceURLKey(r), id, err)
		}
	}
	respondLoopMutation(w, err, http.StatusNoContent, nil)
}

// fallbackLoopDescription is the first sentence of the instructions, used
// until (or instead of) a generated description.
func fallbackLoopDescription(instructions string) string {
	text := strings.TrimSpace(instructions)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "#-*> \t0123456789."))
		if line == "" || strings.HasSuffix(line, ":") && len(line) < 40 {
			continue
		}
		text = line
		break
	}
	for index, char := range text {
		if (char == '.' || char == '!' || char == '?') && (index+1 == len(text) || unicode.IsSpace(rune(text[index+1]))) {
			text = text[:index+1]
			break
		}
	}
	text = strings.ReplaceAll(text, "**", "")
	if runes := []rune(text); len(runes) > 140 {
		text = strings.TrimSpace(string(runes[:137])) + "…"
	}
	return text
}

const loopDescriptionPrompt = `Write a one-line description for an automation loop from its name and instructions, like "Reviews incoming issues, adds context, and routes each one to the right owner."
Rules: one sentence, at most 16 words, present tense starting with a verb, no quotes or markdown, same language as the instructions. Reply with the description only.`

// describeLoopAsync generates the loop's one-line description with the model.
func (s *server) describeLoopAsync(workspace string, loop domain.Loop) {
	if !s.agent.Enabled || !s.agentAutoTitle || strings.TrimSpace(loop.Instructions) == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		turn, err := s.requestAgentTurnWithoutTools(ctx, []agentProviderMessage{{Role: "system", Content: loopDescriptionPrompt}, {Role: "user", Content: "Name: " + loop.Name + "\n\nInstructions:\n" + truncateSettingsText(loop.Instructions, 6000)}})
		if err != nil {
			log.Printf("Loop description workspace=%s loop=%s: %v", workspace, loop.ID, err)
			return
		}
		description := strings.Trim(strings.TrimSpace(strings.Split(strings.TrimSpace(turn.Text), "\n")[0]), "\"'")
		if description == "" || len([]rune(description)) > 200 {
			return
		}
		err = s.store.MutateWorkspace(context.Background(), workspace, "loop.described", loop.ID, nil, func(data *domain.Bootstrap) error {
			current := loopByID(data, loop.ID)
			// Skip when the instructions changed again meanwhile; that change describes itself.
			if current == nil || current.Instructions != loop.Instructions {
				return nil
			}
			current.Description = description
			describeLatestLoopVersion(data, loop.ID, loop.Instructions, description)
			return nil
		})
		if err != nil {
			log.Printf("Loop description workspace=%s loop=%s: %v", workspace, loop.ID, err)
		}
	}()
}

// describeLatestLoopVersion gives the newest published version the generated
// description: it was snapshotted before the description existed and
// describes the same definition (same instructions).
func describeLatestLoopVersion(data *domain.Bootstrap, loopID, instructions, description string) {
	latest := -1
	for index, version := range data.LoopVersions {
		if version.LoopID == loopID && (latest < 0 || version.Version > data.LoopVersions[latest].Version) {
			latest = index
		}
	}
	if latest >= 0 && data.LoopVersions[latest].Definition.Instructions == instructions {
		data.LoopVersions[latest].Definition.Description = description
	}
}
