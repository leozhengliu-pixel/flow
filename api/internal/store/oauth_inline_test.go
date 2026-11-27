package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// The OAuth record writers refuse a collection the stored root keeps
// inline (ErrMetadataFieldInline) on the literal path too: both modes run the
// same row callback, so the bounded transaction answers and stores exactly
// what the literal one does. Here settings hold an over-long key, which keeps
// the map inline.
func TestOAuthRecordWriterInlineSettings(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	type outcome struct {
		Inline   bool
		Physical scaleIssuePhysicalField
		Native   any
	}
	var outcomes []outcome
	for _, bounded := range []bool{false, true} {
		f := journalScaleDatabase(t)
		workspace := f.data.Workspace.URLKey
		if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw542_seed", "", nil, func(data *domain.Bootstrap) error {
			if data.Settings == nil {
				data.Settings = map[string]any{}
			}
			data.Settings[strings.Repeat("k", 192)] = "over-long key"
			data.Settings["sw542"] = "kept"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if inline, err := metadataFieldStoredInline(t.Context(), f.db, f.dialect, workspace, "settings"); err != nil || !inline {
			t.Fatal("fixture settings are not inline", inline, err)
		}
		repo := scaleIssuePersistenceView(t, f, bounded, false)
		err := repo.RequestOAuthApplicationApproval(t.Context(), workspace, "sw542-client", func(data *domain.Bootstrap) error {
			data.Settings["applicationPolicies"] = map[string]any{"sw542-client": "requested"}
			return nil
		})
		result := outcome{Inline: errors.Is(err, ErrMetadataFieldInline), Physical: scaleIssuePhysical(t, repo, workspace, "settings"), Native: scaleIssueNative(t, repo, workspace, "settings")}
		if err != nil && !result.Inline {
			t.Fatal(bounded, err)
		}
		outcomes = append(outcomes, result)
	}
	if !outcomes[0].Inline || !reflect.DeepEqual(outcomes[0], outcomes[1]) {
		t.Fatalf("literal/bounded inline OAuth write differs\nliteral=%+v\nbounded=%+v", outcomes[0], outcomes[1])
	}
}
