package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"reflect"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) scaleP21SW16ListProjectTemplates(w http.ResponseWriter, r *http.Request) {
	// The original BootstrapFor is context-insensitive. Snapshot that same
	// installed source without cancelling it on an already-ended request.
	ctx := context.WithoutCancel(r.Context())
	read, found, err := s.store.BeginProjectTemplatesStream(ctx, workspaceKey(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	defer read.Close()
	if !s.store.BoundedMetadataEnabled() {
		writeJSON(w, http.StatusOK, read.Templates())
		return
	}
	release, err := legacyStreamLease(ctx, 256<<10)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer release()
	file, err := store.NewRecordSpool()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer file.Close()
	buffer := bufio.NewWriterSize(file, 64<<10)
	encodingCtx := hydratedSigningContext(ctx, w)
	first := true
	nilCollection, err := read.Visit(func(_ context.Context, template domain.ProjectTemplate) error {
		if first {
			first = false
			if _, err := buffer.WriteString("["); err != nil {
				return err
			}
		} else if _, err := buffer.WriteString(","); err != nil {
			return err
		}
		return encodeHydratedJSON(encodingCtx, buffer, reflect.ValueOf(template), 0)
	})
	if err == nil {
		switch {
		case nilCollection:
			_, err = io.WriteString(buffer, "null\n")
		case first:
			_, err = io.WriteString(buffer, "[]\n")
		default:
			_, err = io.WriteString(buffer, "]\n")
		}
	}
	if err == nil {
		err = buffer.Flush()
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := sendLegacySpool(ctx, w, http.StatusOK, file); err != nil {
		panic(http.ErrAbortHandler)
	}
}
