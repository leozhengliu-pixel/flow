package main

import (
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
	s.storeCommentAttachment(w, r, "project", func(data *domain.Bootstrap, id string, attachment domain.Attachment) error {
		project, err := fullProjectByID(data, id)
		if err != nil {
			return err
		}
		project.CommentAttachments = append(project.CommentAttachments, attachment)
		return nil
	})
}

// createInitiativeCommentAttachment is the initiative twin used by Pulse
// update comments: the upload is recorded on the initiative so its served URL
// stays visible to everyone who can see the initiative.
func (s *server) createInitiativeCommentAttachment(w http.ResponseWriter, r *http.Request) {
	s.storeCommentAttachment(w, r, "initiative", func(data *domain.Bootstrap, id string, attachment domain.Attachment) error {
		initiative, err := initiativeByID(data, id)
		if err != nil {
			return err
		}
		initiative.CommentAttachments = append(initiative.CommentAttachments, attachment)
		return nil
	})
}

func (s *server) storeCommentAttachment(w http.ResponseWriter, r *http.Request, kind string, record func(*domain.Bootstrap, string, domain.Attachment) error) {
	limit := uploadLimit()
	form, err := openUploadForm(w, r, limit)
	if err != nil {
		writeAttachmentUploadError(w, err, limit)
		return
	}
	file, err := form.File()
	if err != nil {
		writeAttachmentUploadError(w, err, limit)
		return
	}
	defer file.Close()
	filename, contentType := file.FileName(), file.Header.Get("Content-Type")
	projectID := r.PathValue("id")
	attachmentID := kind + "_comment_attachment_" + newCollaborationID()
	objectKey := attachmentID + "_" + filepath.Base(filename)
	storage, err := s.storage()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage unavailable")
		return
	}
	upload, policyErr := s.checkUploadPolicy(workspaceKey(r), filename, file)
	if policyErr != nil {
		writeError(w, http.StatusForbidden, policyErr.Error())
		return
	}
	size, err := form.Store(r.Context(), storage, objectKey, upload, contentType, limit)
	if err != nil {
		writeAttachmentUploadError(w, err, limit)
		return
	}
	attachment := domain.Attachment{ID: attachmentID, Title: filename, URL: "/uploads/" + objectKey, ContentType: contentType, Size: size, CreatedAt: time.Now().UTC()}
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), kind+".comment_attachment_created", projectID, map[string]string{"attachmentId": attachmentID}, func(data *domain.Bootstrap) error {
		attachment.Creator = data.Viewer
		return record(data, projectID, attachment)
	})
	if err != nil {
		_ = storage.Delete(r.Context(), objectKey)
	}
	respondMutation(w, err, http.StatusCreated, attachment)
}
