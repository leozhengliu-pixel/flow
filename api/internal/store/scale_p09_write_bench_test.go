package store

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/pprof"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func BenchmarkScaleP09IssueUpdate(b *testing.B) {
	b.Run("10Teams", benchmarkScaleP09IssueUpdate)
}

func benchmarkScaleP09IssueUpdate(b *testing.B) {
	scaleP09QuietBenchmark(b)
	repo, workspace := benchmarkWorkspace(b, "p09-issue", 10)
	issue := repo.Bootstrap().Issues[0]
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		title := fmt.Sprintf("Issue title %d", i%2)
		_, err := repo.UpdateIssueRecord(ctx, workspace, issue.ID, nil, IssueMutationScope{Payload: domain.IssueUpdateInput{Title: &title}}, func(_ *domain.Bootstrap, target *domain.Issue) error {
			target.Title = title
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScaleP09CommentCreate(b *testing.B) {
	b.Run("10Teams", benchmarkScaleP09CommentCreate)
}

func benchmarkScaleP09CommentCreate(b *testing.B) {
	scaleP09QuietBenchmark(b)
	repo, workspace := benchmarkWorkspace(b, "p09-comment", 10)
	issue := repo.Bootstrap().Issues[0]
	run := benchmarkRunSeq.Add(1)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("p09-bench-%d-%d", run, i)
		if err := repo.MutateWorkspace(WithIssueDiscussionMutation(ctx, issue.ID, id), workspace, "comment.created", issue.ID, nil, func(data *domain.Bootstrap) error {
			data.Comments[issue.ID] = append(data.Comments[issue.ID], domain.Comment{ID: id, Body: "A small comment", CreatedAt: time.Now().UTC()})
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func scaleP09QuietBenchmark(b *testing.B) {
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
	b.Cleanup(func() { slog.SetDefault(previous) })
}

func BenchmarkScaleP09TeamHotProfile(b *testing.B) {
	profilePath := os.Getenv("FLOW_SCALE_P09_CPU_PROFILE")
	if profilePath == "" {
		b.Skip("set FLOW_SCALE_P09_CPU_PROFILE for the setup-free CPU profile")
	}
	repo, workspace := benchmarkWorkspace(b, "p09-hot-profile", 1000)
	var profile *os.File
	if b.N > 1 {
		var err error
		profile, err = os.Create(profilePath)
		if err != nil {
			b.Fatal(err)
		}
		if err := pprof.StartCPUProfile(profile); err != nil {
			b.Fatal(err)
		}
	}
	ctx := context.Background()
	zones := []string{"Asia/Shanghai", "UTC"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		zone := zones[i%2]
		if err := repo.MutateWorkspace(ctx, workspace, "team.settings_updated", "bulk-team-00000", map[string]string{"timezone": zone}, func(data *domain.Bootstrap) error {
			settings := data.TeamSettings["bulk-team-00000"]
			settings.Timezone = zone
			data.TeamSettings["bulk-team-00000"] = settings
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if profile != nil {
		pprof.StopCPUProfile()
		if err := profile.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
