package main

import (
	"io"
	"net/http"
	"path/filepath"
	"time"

	"flow/api/internal/domain"
)

// createProjectCommentAttachment stores media inserted inline into a project
// comment (or update) composer. The upload is recorded on the project so the
// served /uploads URL passes the attachment visibility check for every member
// who can see the project, and the returned attachment URL is embedded
// directly into the comment body.
func (s *server) createProjectCommentAttachment(w http.ResponseWriter, r *http.Request) {
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
	projectID := r.PathValue("id")
	attachmentID := "project_comment_attachment_" + newCollaborationID()
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
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.comment_attachment_created", projectID, map[string]string{"attachmentId": attachmentID}, func(data *domain.Bootstrap) error {
		project, err := fullProjectByID(data, projectID)
		if err != nil {
			return err
		}
		attachment.Creator = data.Viewer
		project.CommentAttachments = append(project.CommentAttachments, attachment)
		return nil
	})
	if err != nil {
		_ = storage.Delete(r.Context(), objectKey)
	}
	respondMutation(w, err, http.StatusCreated, attachment)
}
