package main

import (
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"time"

	"flow/api/internal/domain"
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
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document.attachment_created", documentID, map[string]string{"attachmentId": attachmentID}, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, documentID)
		if err != nil {
			return err
		}
		if !canEditDocument(documentRole(s, *data, *document)) {
			return store.ErrAuthForbidden
		}
		attachment.Creator = data.Viewer
		document.Attachments = append(document.Attachments, attachment)
		return nil
	})
	if err != nil {
		_ = storage.Delete(r.Context(), objectKey)
	}
	respondMutation(w, err, http.StatusCreated, attachment)
}

// documentAttachmentVisible reports whether an upload belongs to a document the viewer can see.
func documentAttachmentVisible(s *server, data domain.Bootstrap, url string) bool {
	return slices.ContainsFunc(data.Documents, func(document domain.Document) bool {
		return slices.ContainsFunc(document.Attachments, func(attachment domain.Attachment) bool { return attachment.URL == url }) && documentVisibleToViewer(s, data, document)
	})
}
