package main

import (
	"encoding/base64"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// writeArrayPage preserves the legacy array response while bounding job-list
// payloads. Clients can continue with X-Next-Cursor without downloading the
// complete workspace job history on every poll.
func writeArrayPage[T any](w http.ResponseWriter, r *http.Request, values []T) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := 0
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		if decoded, err := base64.RawURLEncoding.DecodeString(raw); err == nil {
			offset, _ = strconv.Atoi(string(decoded))
		}
	}
	if offset < 0 || offset > len(values) {
		offset = 0
	}
	end := min(len(values), offset+limit)
	w.Header().Set("X-Total-Count", strconv.Itoa(len(values)))
	if end < len(values) {
		w.Header().Set("X-Next-Cursor", base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end))))
	}
	writeJSON(w, http.StatusOK, values[offset:end])
}

// listImports and listExports provide a lightweight job monitor instead of
// forcing clients to repeatedly download the complete bootstrap payload.
func (s *server) listImports(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	jobs := make([]domain.ImportJob, 0)
	for _, job := range data.ImportJobs {
		if s.authDisabled || job.UserID == authUser(r).ID {
			jobs = append(jobs, job)
		}
	}
	writeArrayPage(w, r, jobs)
}

func (s *server) getImport(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	id := r.PathValue("id")
	for _, job := range data.ImportJobs {
		if job.ID == id && (s.authDisabled || job.UserID == authUser(r).ID) {
			writeJSON(w, http.StatusOK, job)
			return
		}
	}
	writeError(w, http.StatusNotFound, "import not found")
}

func (s *server) cancelImport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var updated domain.ImportJob
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "import.cancelled", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.ImportJobs, func(item domain.ImportJob) bool {
			return item.ID == id && (s.authDisabled || item.UserID == data.Viewer.ID)
		})
		if index < 0 {
			return errNotFound
		}
		if data.ImportJobs[index].Status != "mapping" && data.ImportJobs[index].Status != "running" {
			return errInvalid
		}
		data.ImportJobs[index].Status = "cancelled"
		data.ImportJobs[index].UpdatedAt = time.Now().UTC()
		updated = data.ImportJobs[index]
		appendAudit(data, "cancelled", "import", id, nil)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

// retryImport resets a failed or cancelled mapping job without dropping the
// original uploaded rows. The caller must commit it again with an explicit
// mapping; this mirrors Flow's recoverable background import workflow and
// avoids silently replaying a partially successful import.
func (s *server) retryImport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var updated domain.ImportJob
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "import.retried", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.ImportJobs, func(item domain.ImportJob) bool {
			return item.ID == id && (s.authDisabled || item.UserID == data.Viewer.ID)
		})
		if index < 0 {
			return errNotFound
		}
		job := &data.ImportJobs[index]
		if job.Status != "failed" && job.Status != "cancelled" {
			return errInvalid
		}
		job.Status = "mapping"
		job.Progress = 0
		job.Imported = 0
		job.Errors = []string{}
		job.RowErrors = []domain.ImportRowError{}
		job.Checkpoint = 0
		job.Error = ""
		job.RetryCount++
		job.UpdatedAt = time.Now().UTC()
		updated = *job
		appendAudit(data, "retried", "import", id, map[string]any{"retryCount": job.RetryCount})
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) resumeImport(w http.ResponseWriter, r *http.Request) {
	// Resuming uses the same idempotent retry path while preserving the uploaded
	// mapping and row-level diagnostics for the next commit request.
	id := r.PathValue("id")
	var updated domain.ImportJob
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "import.resumed", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.ImportJobs, func(item domain.ImportJob) bool {
			return item.ID == id && (s.authDisabled || item.UserID == data.Viewer.ID)
		})
		if index < 0 {
			return errNotFound
		}
		job := &data.ImportJobs[index]
		if job.Status != "failed" && job.Status != "cancelled" {
			return errInvalid
		}
		job.Status = "mapping"
		job.Error = ""
		job.UpdatedAt = time.Now().UTC()
		updated = *job
		appendAudit(data, "resumed", "import", id, map[string]any{"checkpoint": job.Checkpoint})
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) listExports(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	jobs := make([]domain.ExportJob, 0)
	for _, job := range data.ExportJobs {
		if s.authDisabled || job.UserID == authUser(r).ID {
			jobs = append(jobs, job)
		}
	}
	writeArrayPage(w, r, jobs)
}

func (s *server) getExport(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	id := r.PathValue("id")
	for _, job := range data.ExportJobs {
		if job.ID == id && (s.authDisabled || job.UserID == authUser(r).ID) {
			writeJSON(w, http.StatusOK, job)
			return
		}
	}
	writeError(w, http.StatusNotFound, "export not found")
}

func (s *server) retryExport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var updated domain.ExportJob
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "export.retried", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.ExportJobs, func(item domain.ExportJob) bool {
			return item.ID == id && (s.authDisabled || item.UserID == data.Viewer.ID)
		})
		if index < 0 {
			return errNotFound
		}
		if data.ExportJobs[index].Status != "failed" && data.ExportJobs[index].Status != "cancelled" {
			return errInvalid
		}
		data.ExportJobs[index].Status = "queued"
		data.ExportJobs[index].Error = ""
		data.ExportJobs[index].CompletedAt = nil
		updated = data.ExportJobs[index]
		appendAudit(data, "retried", "export", id, nil)
		return nil
	})
	if err == nil {
		go s.completeExport(workspaceKey(r), id)
	}
	respondMutation(w, err, http.StatusAccepted, updated)
}

// workspaceAnalytics is intentionally computed from the same projections used
// by the UI, so dashboards remain correct after imports or webhook updates.
func (s *server) workspaceAnalytics(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -30)
	if raw := strings.TrimSpace(r.URL.Query().Get("since")); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			since = parsed
		}
	}
	status := map[string]int{}
	team := map[string]int{}
	daily := map[string]int{}
	var cycleTotal float64
	var cycleCount int
	for _, issue := range data.Issues {
		status[issue.State.Name]++
		team[issue.Team.Key]++
		if issue.CreatedAt.Before(since) {
			continue
		}
		day := issue.CreatedAt.UTC().Format("2006-01-02")
		daily[day]++
		if issue.UpdatedAt.After(issue.CreatedAt) {
			cycleTotal += issue.UpdatedAt.Sub(issue.CreatedAt).Hours()
			cycleCount++
		}
	}
	type point struct {
		Date  string `json:"date"`
		Count int    `json:"count"`
	}
	points := make([]point, 0, len(daily))
	for date, count := range daily {
		points = append(points, point{date, count})
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Date < points[j].Date })
	writeJSON(w, http.StatusOK, map[string]any{
		"since":       since,
		"generatedAt": now,
		"issues": map[string]any{"total": len(data.Issues), "active": len(slices.DeleteFunc(slices.Clone(data.Issues), func(issue domain.Issue) bool {
			return issue.State.Type == "completed" || issue.State.Type == "canceled"
		}))},
		"status":     status,
		"team":       team,
		"throughput": points,
		"averageCycleTimeHours": func() float64 {
			if cycleCount == 0 {
				return 0
			}
			return cycleTotal / float64(cycleCount)
		}(),
		"projects": len(data.Projects),
		"cycles":   len(data.Cycles),
	})
}
