package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"flow/api/internal/store"
)

func (s *server) syncRecords(w http.ResponseWriter, r *http.Request) {
	if s.scaleP20Records(w, r) {
		return
	}
	if !syncFlagEnabled("FLOW_INDEXED_READS") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.URL.Query().Has("window") {
		s.scaleP19Window(w, r)
		return
	}
	access, ok := s.syncAccess(w, r)
	if !ok {
		return
	}
	values := r.URL.Query()
	limit, err := syncPageNumber(values.Get("limit"), 100, 1, 100)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	budget, err := syncPageNumber(values.Get("maxBytes"), store.MetadataRecordPageMaxBytes, 8<<10, store.MetadataRecordPageMaxBytes)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	page, err := s.store.ReadRecordPage(r.Context(), access, store.PageRequest{
		Kind: values.Get("kind"), Cursor: values.Get("cursor"), Filter: values.Get("filter"), Order: values.Get("order"), Limit: limit, MaxBytes: budget,
	})
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeSyncJSON(w, page, budget)
}

func syncPageNumber(raw string, fallback, low, high int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < low || value > high {
		return 0, store.ErrMetadataRecordQuery
	}
	return value, nil
}

func (s *server) syncBatch(w http.ResponseWriter, r *http.Request) {
	if !syncFlagEnabled("FLOW_INDEXED_READS") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	access, ok := s.syncAccess(w, r)
	if !ok {
		return
	}
	var input struct {
		Keys       []store.RecordKey `json:"keys"`
		Projection string            `json:"projection"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || len(input.Keys) > store.MetadataRecordBatchMaxKeys {
		writeSyncError(w, store.ErrMetadataRecordQuery)
		return
	}
	if input.Projection == "" {
		input.Projection = "detail"
	}
	if s.scaleP20Batch(w, r, input.Keys, input.Projection) {
		return
	}
	batch, err := s.store.ReadRecords(r.Context(), access, input.Keys, input.Projection)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeSyncJSON(w, batch, store.MetadataRecordPageMaxBytes)
}
