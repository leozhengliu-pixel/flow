package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

type scaleDiscardResponse struct{ header http.Header }

func (w scaleDiscardResponse) Header() http.Header         { return w.header }
func (w scaleDiscardResponse) WriteHeader(int)             {}
func (w scaleDiscardResponse) Write(p []byte) (int, error) { return io.Discard.Write(p) }

func BenchmarkScaleLegacyOutput(b *testing.B) {
	data := domain.Bootstrap{Projects: make([]domain.Project, 128)}
	for i := range data.Projects {
		data.Projects[i] = domain.Project{ID: "project", Name: "Project", Description: strings.Repeat("body & text\n", 4096)}
	}
	b.Run("bootstrap", func(b *testing.B) {
		w := scaleDiscardResponse{make(http.Header)}
		b.ReportAllocs()
		for range b.N {
			writeBootstrapJSON(w, data)
		}
	})
	b.Run("nested-body", func(b *testing.B) {
		value := struct {
			History map[string][]string `json:"history"`
		}{map[string][]string{"document": {strings.Repeat("body & text\n", 1<<18)}}}
		w := scaleDiscardResponse{make(http.Header)}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			writeStreamedJSON(context.Background(), w, http.StatusOK, value)
		}
	})
}
