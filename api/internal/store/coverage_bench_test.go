package store

import (
	"fmt"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func BenchmarkScaleP15WorkspaceMetadata(b *testing.B) {
	data := domain.Bootstrap{Projects: make([]domain.Project, 128)}
	for i := range data.Projects {
		data.Projects[i] = domain.Project{ID: fmt.Sprint(i), Description: strings.Repeat("body", 16<<10)}
	}
	s := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"bench": data}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, ok := s.WorkspaceMetadata("bench"); !ok {
			b.Fatal("workspace missing")
		}
	}
}
