package main

import (
	"bufio"
	"errors"
	"net/http"
	"reflect"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) writeBoundedTeamResources(w http.ResponseWriter, r *http.Request, teamID string) {
	read, workspaceFound, teamFound, err := s.store.BeginTeamResourceRead(r.Context(), workspaceKey(r), teamID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if !workspaceFound {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if !teamFound {
		writeError(w, http.StatusNotFound, "team not found")
		return
	}
	defer read.Close()
	release, err := legacyStreamLease(r.Context(), 256<<10)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Workspace snapshot is busy; retry later")
		return
	}
	defer release()
	file, _ := store.NewRecordSpool()
	defer removeLegacySpool(file)
	spool := &legacyResponseSpool{ResponseWriter: w, ctx: r.Context(), file: file}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	buffer := bufio.NewWriterSize(spool, 64<<10)
	ctx := hydratedSigningContext(r.Context(), spool)
	write := func(text string) error {
		_, err := buffer.WriteString(text)
		return err
	}
	first := true
	err = write(`{"sections":[`)
	if err == nil {
		err = read.VisitSections(func(section domain.TeamResourceSection) error {
			if !first {
				if err := write(","); err != nil {
					return err
				}
			}
			first = false
			return encodeHydratedJSON(ctx, buffer, reflect.ValueOf(section), 0)
		})
	}
	if err == nil {
		err = write(`],"resources":[`)
	}
	first = true
	if err == nil {
		err = read.VisitResources(func(resource domain.TeamPinnedResource) error {
			if !first {
				if err := write(","); err != nil {
					return err
				}
			}
			first = false
			return encodeHydratedJSON(ctx, buffer, reflect.ValueOf(resource), 0)
		})
	}
	if err == nil {
		err = write("]}\n")
	}
	if err == nil {
		err = buffer.Flush()
	}
	err = errors.Join(err, read.Close(), spool.err, r.Context().Err())
	if err != nil {
		if spool.passthrough {
			panic(http.ErrAbortHandler)
		}
		removeLegacySpool(file)
		writeError(w, http.StatusServiceUnavailable, "team resources unavailable")
		return
	}
	if !spool.passthrough && file != nil {
		if err := sendLegacySpool(r.Context(), w, http.StatusOK, file); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
}
