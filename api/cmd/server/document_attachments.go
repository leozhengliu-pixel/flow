package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// createDocumentAttachment stores media and files pasted or dropped into a
// document body. The upload is recorded on the document, so the served
// /uploads URL passes the attachment visibility check for everyone who can see
// the document, and the returned URL is embedded directly into the content.
func (s *server) createDocumentAttachment(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, (20<<20)+(1<<20))
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid attachment")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	documentID := r.PathValue("id")
	attachmentID := "document_attachment_" + newCollaborationID()
	objectKey := attachmentID + "_" + filepath.Base(header.Filename)
	storage, err := s.storage()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage unavailable")
		return
	}
	upload, policyErr := s.checkUploadPolicy(workspaceKey(r), header.Filename, file)
	if policyErr != nil {
		writeError(w, http.StatusForbidden, policyErr.Error())
		return
	}
	size, err := storage.Put(r.Context(), objectKey, io.LimitReader(upload, (20<<20)+1), header.Header.Get("Content-Type"))
	if err != nil || size > 20<<20 {
		_ = storage.Delete(r.Context(), objectKey)
		if err == nil {
			writeError(w, http.StatusRequestEntityTooLarge, "attachment exceeds 20 MB")
		} else {
			writeError(w, http.StatusInternalServerError, "upload failed")
		}
		return
	}
	attachment := domain.Attachment{ID: attachmentID, Title: header.Filename, URL: "/uploads/" + objectKey, ContentType: header.Header.Get("Content-Type"), Size: size, CreatedAt: time.Now().UTC()}
	attachment, err = s.recordDocumentAttachment(r.Context(), workspaceKey(r), documentID, attachment)
	if err != nil {
		_ = storage.Delete(r.Context(), objectKey)
	}
	respondMutation(w, err, http.StatusCreated, attachment)
}

// recordDocumentAttachment records a stored upload on a document the actor
// can edit (store.ErrAuthForbidden otherwise). The REST upload above and the
// MCP attachment tools share it, so both apply the same document roles,
// including the scope a document inherits from a private team. A creator left
// empty is the actor.
func (s *server) recordDocumentAttachment(ctx context.Context, workspace, documentID string, attachment domain.Attachment) (domain.Attachment, error) {
	err := s.store.MutateWorkspace(ctx, workspace, "document.attachment_created", documentID, map[string]string{"attachmentId": attachment.ID}, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, documentID)
		if err != nil {
			return err
		}
		if !canEditDocument(documentRole(s, *data, *document)) {
			return store.ErrAuthForbidden
		}
		if attachment.Creator.ID == "" {
			attachment.Creator = data.Viewer
		}
		document.Attachments = append(document.Attachments, attachment)
		return nil
	})
	return attachment, err
}

// saveDocumentLinkAttachment creates or updates (same URL on the same
// document) a link attachment on a document the actor can edit: the MCP
// save_attachment tool with a document target.
func (s *server) saveDocumentLinkAttachment(ctx context.Context, workspace, documentID string, input linkAttachmentInput, creator domain.User) (domain.Attachment, error) {
	var saved domain.Attachment
	err := s.store.MutateWorkspace(ctx, workspace, "document.attachment_saved", documentID, input, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, documentID)
		if err != nil {
			return err
		}
		if !canEditDocument(documentRole(s, *data, *document)) {
			return store.ErrAuthForbidden
		}
		now := time.Now().UTC()
		if index := slices.IndexFunc(document.Attachments, func(item domain.Attachment) bool { return item.URL == input.URL }); index >= 0 {
			applyLinkAttachmentInput(&document.Attachments[index], input)
			document.Attachments[index].UpdatedAt = &now
			saved = document.Attachments[index]
			return nil
		}
		parsed, _ := url.Parse(input.URL)
		attachment := domain.Attachment{ID: idgen.New("attachment_"), Title: parsed.Host, URL: input.URL, ContentType: "text/uri-list", CreatedAt: now, UpdatedAt: &now, Creator: creator, BotActor: creator.Via}
		applyLinkAttachmentInput(&attachment, input)
		if attachment.Title == "" {
			attachment.Title = parsed.Host
		}
		document.Attachments = append(document.Attachments, attachment)
		saved = attachment
		return nil
	})
	return saved, err
}

// deleteDocumentAttachment removes an attachment from a document the actor
// can edit and returns it (the caller deletes an uploaded file's object).
func (s *server) deleteDocumentAttachment(ctx context.Context, workspace, documentID, attachmentID string) (domain.Attachment, error) {
	var removed domain.Attachment
	err := s.store.MutateWorkspace(ctx, workspace, "document.attachment_deleted", documentID, map[string]string{"attachmentId": attachmentID}, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, documentID)
		if err != nil {
			return err
		}
		if !canEditDocument(documentRole(s, *data, *document)) {
			return store.ErrAuthForbidden
		}
		index := slices.IndexFunc(document.Attachments, func(item domain.Attachment) bool { return item.ID == attachmentID })
		if index < 0 {
			return errNotFound
		}
		removed = document.Attachments[index]
		document.Attachments = slices.Delete(document.Attachments, index, index+1)
		return nil
	})
	return removed, err
}

// documentAttachmentVisible reports whether an upload belongs to a document the viewer can see.
func documentAttachmentVisible(s *server, data domain.Bootstrap, url string) bool {
	return slices.ContainsFunc(data.Documents, func(document domain.Document) bool {
		return slices.ContainsFunc(document.Attachments, func(attachment domain.Attachment) bool { return attachment.URL == url }) && documentVisibleToViewer(s, data, document)
	})
}
