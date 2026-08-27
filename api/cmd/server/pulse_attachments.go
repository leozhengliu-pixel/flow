package main

import (
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"time"

	"flow/api/internal/domain"
)

func (s *server) createPulseUpdateAttachment(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, (20<<20)+(1<<20))
		if err := r.ParseMultipartForm(20 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "invalid attachment")
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "file is required")
			return
		}
		defer file.Close()
		parentID, updateID := r.PathValue("id"), r.PathValue("updateId")
		attachmentID := "update_attachment_" + newCollaborationID()
		objectKey := attachmentID + "_" + filepath.Base(header.Filename)
		storage, err := s.storage()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage unavailable")
			return
		}
		size, err := storage.Put(r.Context(), objectKey, io.LimitReader(file, (20<<20)+1), header.Header.Get("Content-Type"))
		if err != nil || size > 20<<20 {
			_ = storage.Delete(r.Context(), objectKey)
			if size > 20<<20 {
				writeError(w, http.StatusRequestEntityTooLarge, "attachment exceeds 20 MB")
			} else {
				writeError(w, http.StatusInternalServerError, "upload failed")
			}
			return
		}
		attachment := domain.Attachment{ID: attachmentID, Title: header.Filename, URL: "/uploads/" + objectKey, ContentType: header.Header.Get("Content-Type"), Size: size, CreatedAt: time.Now().UTC()}
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
			updates[index].Attachments = append(updates[index].Attachments, attachment)
			data.InitiativeUpdates[parentID] = updates
			result = updates[index]
			return nil
		})
		if err != nil {
			_ = storage.Delete(r.Context(), objectKey)
		}
		respondMutation(w, err, http.StatusCreated, result)
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
		respondMutation(w, err, http.StatusOK, result)
	}
}
