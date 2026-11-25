package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// Project only resident fields before decoding. A legal inline record collection
// can exceed a page without making the policy projection exceed a page.
func (s *SQLiteStore) readInlineResidentRoot(ctx context.Context, tx *sqlTx, workspace string, size int, legacyInlineTeams bool) ([]byte, map[string]string, error) {
	if s.maxStateBytes > 0 && size > s.maxStateBytes {
		return nil, nil, fmt.Errorf("workspace state exceeds %d bytes", s.maxStateBytes)
	}
	// Includes the fixed parser buffers, retained projection, map decoding and
	// JSON's worst-case escaping while the existing validator filters it.
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, 8<<20)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	read, err := s.scanMetadataInlineRoot(ctx, tx, workspace, int64(size))
	if err != nil {
		return nil, nil, err
	}
	defer read.Close()
	root := map[string]json.RawMessage{}
	retained := int64(0)
	for name, part := range read.fields {
		mode := metadataResidencyFields()[name]
		if name == metadataCollectionsKey || mode == "core" || mode == "policy" {
			if part.size > MetadataRecordPageMaxBytes-retained {
				return nil, nil, ErrMetadataPageBudget
			}
			retained += part.size
			raw := make([]byte, int(part.size))
			if _, err := read.source.ReadAt(raw, part.offset); err != nil {
				return nil, nil, err
			}
			root[name] = raw
			continue
		}
		// Record fields are omitted from resident policy. Preserve only the
		// shape test for other unsplit fields so coverage refusals stay exact.
		root[name] = json.RawMessage("null")
		if mode != "records" && part.size > 2 {
			first := [1]byte{}
			if _, err := read.source.ReadAt(first[:], part.offset); err != nil {
				return nil, nil, err
			}
			if first[0] == '[' {
				root[name] = json.RawMessage("[0]")
			} else if first[0] == '{' {
				root[name] = json.RawMessage(`{"":0}`)
			}
		}
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return nil, nil, err
	}
	return residentMetadataRootForPolicy(raw, legacyInlineTeams)
}
