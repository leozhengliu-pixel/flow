package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) createPulseUpdateAttachment(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		parentID, updateID := r.PathValue("id"), r.PathValue("updateId")
		attachmentID := "update_attachment_" + newCollaborationID()
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
		var result any
		err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), kind+".update_attachment_created", parentID, map[string]string{"updateId": updateID, "attachmentId": attachmentID}, func(data *domain.Bootstrap) error {
			attachment.Creator = data.Viewer
			if kind == "project" {
				if _, err := fullProjectByID(data, parentID); err != nil {
					return err
				}
				updates := data.ProjectUpdates[parentID]
				index := slices.IndexFunc(updates, func(item domain.ProjectUpdate) bool { return item.ID == updateID })
				if index < 0 {
					return errNotFound
				}
				if !canEditPulseUpdate(data, updates[index].User) {
					return store.ErrAuthForbidden
				}
				updates[index].Attachments = append(updates[index].Attachments, attachment)
				data.ProjectUpdates[parentID] = updates
				result = updates[index]
				return nil
			}
			if _, err := initiativeByID(data, parentID); err != nil {
				return err
			}
			updates := data.InitiativeUpdates[parentID]
			index := slices.IndexFunc(updates, func(item domain.InitiativeUpdate) bool { return item.ID == updateID })
			if index < 0 {
				return errNotFound
			}
			if !canEditPulseUpdate(data, updates[index].User) {
				return store.ErrAuthForbidden
			}
			updates[index].Attachments = append(updates[index].Attachments, attachment)
			data.InitiativeUpdates[parentID] = updates
			result = updates[index]
			return nil
		})
		if err != nil {
			_ = storage.Delete(r.Context(), objectKey)
		}
		respondMutation(w, err, http.StatusCreated, withoutPulseSnapshot(result))
	}
}

func (s *server) deletePulseUpdateAttachment(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parentID, updateID, attachmentID := r.PathValue("id"), r.PathValue("updateId"), r.PathValue("attachmentId")
		var objectKey string
		var result any
		err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), kind+".update_attachment_deleted", parentID, map[string]string{"updateId": updateID, "attachmentId": attachmentID}, func(data *domain.Bootstrap) error {
			if kind == "project" {
				updates := data.ProjectUpdates[parentID]
				index := slices.IndexFunc(updates, func(item domain.ProjectUpdate) bool { return item.ID == updateID })
				if index < 0 {
					return errNotFound
				}
				if !canEditPulseUpdate(data, updates[index].User) {
					return store.ErrAuthForbidden
				}
				attachmentIndex := slices.IndexFunc(updates[index].Attachments, func(item domain.Attachment) bool { return item.ID == attachmentID })
				if attachmentIndex < 0 {
					return errNotFound
				}
				objectKey = filepath.Base(updates[index].Attachments[attachmentIndex].URL)
				updates[index].Attachments = slices.Delete(updates[index].Attachments, attachmentIndex, attachmentIndex+1)
				data.ProjectUpdates[parentID] = updates
				result = updates[index]
				return nil
			}
			updates := data.InitiativeUpdates[parentID]
			index := slices.IndexFunc(updates, func(item domain.InitiativeUpdate) bool { return item.ID == updateID })
			if index < 0 {
				return errNotFound
			}
			if !canEditPulseUpdate(data, updates[index].User) {
				return store.ErrAuthForbidden
			}
			attachmentIndex := slices.IndexFunc(updates[index].Attachments, func(item domain.Attachment) bool { return item.ID == attachmentID })
			if attachmentIndex < 0 {
				return errNotFound
			}
			objectKey = filepath.Base(updates[index].Attachments[attachmentIndex].URL)
			updates[index].Attachments = slices.Delete(updates[index].Attachments, attachmentIndex, attachmentIndex+1)
			data.InitiativeUpdates[parentID] = updates
			result = updates[index]
			return nil
		})
		if err == nil && objectKey != "" {
			if storage, storageErr := s.storage(); storageErr == nil {
				_ = storage.Delete(r.Context(), objectKey)
			}
		}
		respondMutation(w, err, http.StatusOK, withoutPulseSnapshot(result))
	}
}
