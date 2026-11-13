package main

import (
	"io"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21SW0UploadSettingsBudgetDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		settings := scaleP21SW3PopulatedSettings(t, f.data.WorkspaceSettings)
		settings.AllowedFileExtensions = []string{".txt"}
		for _, restricted := range []bool{false, true} {
			settings.RestrictFileUploads = restricted
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw0.upload", "", nil, func(data *domain.Bootstrap) error {
				data.WorkspaceSettings = settings
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			probe, err := repo.BoundedMetadataViewForTesting(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, spec := range []struct {
				name, body string
				allowed    bool
			}{
				{"allowed.txt", strings.Repeat("text ", 200), true},
				{"image.bin", "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 600), true},
				{"denied.exe", strings.Repeat("text ", 200), !restricted},
			} {
				for _, source := range []*store.SQLiteStore{repo, probe} {
					input := strings.NewReader(spec.body)
					output, err := (&server{store: source}).checkUploadPolicy(workspace, spec.name, input)
					if spec.allowed {
						if err != nil {
							t.Fatal(err)
						}
						raw, err := io.ReadAll(output)
						if err != nil || string(raw) != spec.body {
							t.Fatal("sniffing lost or changed input bytes", err)
						}
						if !restricted && output != input {
							t.Fatal("unrestricted upload wrapped the original reader")
						}
					} else if err == nil || err.Error() != "file type is not permitted by workspace upload policy" {
						t.Fatalf("restricted extension error changed: %v", err)
					}
				}
			}
			if probe.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("upload policy decoded full metadata")
			}
		}
	}
}
