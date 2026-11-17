package store

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW234IssueCanonicalSupportedContentDataDepth(t *testing.T) {
	// IssueUpdateInput accepts this HTTP descriptionData without a schema or
	// nesting restriction. applyUpdate assigns that map to ContentData.
	const blocks = 256
	document := `{"type":"doc","content":[` +
		strings.Repeat(`{"type":"blockquote","content":[`, blocks) +
		`{"type":"paragraph","content":[{"type":"text","text":"supported HTTP content"}]}` +
		strings.Repeat(`]}`, blocks) + `]}`
	request := []byte(`{"descriptionData":` + document + `}`)
	if len(request) >= 16<<20 {
		t.Fatal("depth witness exceeds the HTTP producer's byte limit")
	}
	var input domain.IssueUpdateInput
	decoder := json.NewDecoder(bytes.NewReader(request))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal("supported HTTP descriptionData must decode", err)
	}
	produced := domain.Issue{
		ID: "sw234-supported-content-depth",
		DocumentContent: &domain.DocumentContent{
			ID:          "document_content_sw234-supported-content-depth",
			Version:     1,
			ContentData: input.DescriptionData,
		},
	}
	raw, err := json.Marshal(produced)
	if err != nil {
		t.Fatal("supported producer's stored Issue must marshal", err)
	}
	oracle, expected := scaleP21SW234ExpectWire(t, raw)
	repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
	disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
	frames, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		frames.Close()
		frames.Close()
		if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 ||
			metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors ||
			recordSpoolBytes.Load() != spools {
			t.Error("deep supported ContentData retained work/disk/descriptors or decoded whole metadata")
		}
	}()
	node, err := legacyIssueCanonicalRecord(t.Context(), repo, frames, legacySessionJSONPart{bytes.NewReader(raw), 0, int64(len(raw))})
	if err != nil {
		t.Fatal("bounded canonical Issue must accept supported ContentData depth", err)
	}
	if repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("completed deep ContentData frame retained a temporary native owner")
	}
	scaleP21SW234CompareFrame(t, repo, node, oracle, expected, true)
}
