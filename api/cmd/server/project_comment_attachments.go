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
	attachmentID := kind + "_comment_attachment_" + newCollaborationID()
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
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), kind+".comment_attachment_created", projectID, map[string]string{"attachmentId": attachmentID}, func(data *domain.Bootstrap) error {
		attachment.Creator = data.Viewer
		return record(data, projectID, attachment)
	})
	if err != nil {
		_ = storage.Delete(r.Context(), objectKey)
	}
	respondMutation(w, err, http.StatusCreated, attachment)
}
