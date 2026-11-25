package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

type scaleP15DiscardResponse struct{ header http.Header }

func (w scaleP15DiscardResponse) Header() http.Header         { return w.header }
func (w scaleP15DiscardResponse) WriteHeader(int)             {}
func (w scaleP15DiscardResponse) Write(p []byte) (int, error) { return io.Discard.Write(p) }

func BenchmarkScaleP15LegacyOutput(b *testing.B) {
	data := domain.Bootstrap{Projects: make([]domain.Project, 128)}
	for i := range data.Projects {
		data.Projects[i] = domain.Project{ID: "project", Name: "Project", Description: strings.Repeat("body & text\n", 4096)}
	}
	b.Run("bootstrap", func(b *testing.B) {
		w := scaleP15DiscardResponse{make(http.Header)}
		b.ReportAllocs()
		for range b.N {
			writeBootstrapJSON(w, data)
		}
	})
	b.Run("nested-body", func(b *testing.B) {
		value := struct {
			History map[string][]string `json:"history"`
		}{map[string][]string{"document": {strings.Repeat("body & text\n", 1<<18)}}}
		w := scaleP15DiscardResponse{make(http.Header)}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			writeStreamedJSON(context.Background(), w, http.StatusOK, value)
		}
	})
}
