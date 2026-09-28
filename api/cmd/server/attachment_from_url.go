package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

const maxAttachmentFromURLBytes = 20 << 20

func (s *server) createAttachmentFromURL(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL   string `json:"url"`
		Title string `json:"title,omitempty"`
		Embed *bool  `json:"embed,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	raw := strings.TrimSpace(input.URL)
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		writeError(w, http.StatusBadRequest, "a valid http or https URL is required")
		return
	}
	issueID := r.PathValue("id")
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, parsed.String(), nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid url")
		return
	}
	req.Header.Set("User-Agent", "FlowAttachmentFetcher/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not fetch url")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("url returned status %d", resp.StatusCode))
		return
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	filename := strings.TrimSpace(input.Title)
	if filename == "" {
		filename = path.Base(parsed.Path)
	}
	if filename == "" || filename == "/" || filename == "." {
		filename = "upload"
	}
	filename = filepath.Base(filename)
	upload, policyErr := s.checkUploadPolicy(workspaceKey(r), filename, io.LimitReader(resp.Body, maxAttachmentFromURLBytes+1))
	if policyErr != nil {
		writeError(w, http.StatusForbidden, policyErr.Error())
		return
	}
	attachmentID := fmt.Sprintf("attachment_%d", time.Now().UnixNano())
	safeName := attachmentID + "_" + filename
	storage, err := s.storage()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage unavailable")
		return
	}
	size, copyErr := storage.Put(r.Context(), safeName, upload, contentType)
	if copyErr != nil {
		_ = storage.Delete(r.Context(), safeName)
		writeError(w, http.StatusInternalServerError, "upload failed")
		return
	}
	if size > maxAttachmentFromURLBytes {
		_ = storage.Delete(r.Context(), safeName)
		writeError(w, http.StatusRequestEntityTooLarge, "attachment exceeds 20 MB")
		return
	}
	var attachment domain.Attachment
	embed := input.Embed == nil || *input.Embed
	err = s.store.MutateWorkspace(store.WithIssueRecordMutations(r.Context(), issueID), workspaceKey(r), "attachment.created", issueID, map[string]string{"name": filename, "sourceUrl": raw}, func(data *domain.Bootstrap) error {
		issue, err := issueByID(data, issueID)
		if err != nil {
			return err
		}
		attachment = domain.Attachment{ID: attachmentID, IssueID: issueID, Title: filename, URL: "/uploads/" + safeName, ContentType: contentType, Size: size, CreatedAt: time.Now().UTC(), Creator: data.Viewer, ProviderURL: raw}
		issue.Attachments = append(issue.Attachments, attachment)
		appendActivity(data, issueID, "attachment.created", data.Viewer, map[string]string{"attachmentId": attachment.ID, "title": attachment.Title})
		if embed {
			return applyIssueAttachmentEmbed(data, issue, attachment)
		}
		return nil
	})
	if err != nil {
		_ = storage.Delete(r.Context(), safeName)
	}
	respondMutation(w, err, http.StatusCreated, attachment)
}
