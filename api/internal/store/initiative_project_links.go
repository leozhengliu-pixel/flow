package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// initiativeProjectLinks is a receipt for a transaction that changed only
// the reciprocal link array of each named project.
type initiativeProjectLinks map[string]initiativeProjectLink

type initiativeProjectLink struct {
	ids      []string
	labelUse domain.Project
}

type initiativeProjectLinkEdit struct {
	before, after string
	search        string
}

const (
	initiativeProjectLinkBatch    = 100
	initiativeProjectLinkRowBytes = 256 << 10
)

func (s *SQLiteStore) writeInitiativeProjectLinks(ctx context.Context, tx *sqlTx, workspace string, before, after []domain.Project) (bool, error) {
	base := s.coldWrite.publishBase
	if base == nil || base.workspace != workspace || base.source != s.coldWrite.source ||
		!coldPublishPatchShape("projects", base.source, base.state) || len(before) != len(after) {
		return false, nil
	}
	links := make(initiativeProjectLinks)
	edits := make(map[string]initiativeProjectLinkEdit)
	seen := make(map[string]bool, len(before))
	// Keep the link parameters for all batches bounded. Large or noncanonical
	// arrays still go through the existing merge path.
	remaining := 1 << 20
	searchRemaining := 1 << 20
	for i := range after {
		a, b := before[i], after[i]
		if a.ID != b.ID || seen[a.ID] {
			return false, nil
		}
		seen[a.ID] = true
		if !reflect.DeepEqual(a.Initiatives, b.Initiatives) {
			old, ok := base.state.records["projects"][a.ID]
			if !ok || old.version < 0 || old.physical {
				return false, nil
			}
			oldLink, ok := initiativeProjectLinkJSON(a.Initiatives, &remaining)
			if !ok {
				return false, nil
			}
			newLink, ok := initiativeProjectLinkJSON(b.Initiatives, &remaining)
			if !ok {
				return false, nil
			}
			search, ok, err := initiativeProjectLinkSearch(b, tx.database.searchMaxChars, &searchRemaining)
			if err != nil || !ok {
				return false, err
			}
			edits[a.ID] = initiativeProjectLinkEdit{oldLink, newLink, search}
			links[a.ID] = initiativeProjectLink{
				ids: b.Initiatives, labelUse: domain.Project{ID: a.ID, LabelIDs: a.LabelIDs, ArchivedAt: a.ArchivedAt},
			}
		}
		a.Initiatives, b.Initiatives = nil, nil
		if !reflect.DeepEqual(a, b) {
			return false, nil
		}
	}
	if len(links) == 0 {
		return false, nil
	}
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil || stamp != base.state.stamp {
		return false, err
	}
	var generation string
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM record_journal_mode WHERE id=1 AND enabled=1 AND capturing=1`).Scan(&generation); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if generation != base.state.generation {
		return false, nil
	}
	keys := slices.Sorted(maps.Keys(links))
	lock := ""
	if tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	text, length, find, replace, eligible := initiativeProjectLinkSQL(tx.dialect)
	// Lock only the changed rows. Check the version after taking the row
	// locks, so a peer that committed while they were being taken cannot
	// invalidate the preimage. Keep the ordinary merge for ambiguous rows.
	charge := 0
	shape := "CASE WHEN " + length + "<=? THEN CASE WHEN " + eligible + " AND " + find + " THEN 1 ELSE 0 END ELSE 0 END"
	for start := 0; start < len(keys); start += initiativeProjectLinkBatch {
		batch := keys[start:min(start+initiativeProjectLinkBatch, len(keys))]
		where, keys := bindList("record_key", batch)
		args := append([]any{workspace}, keys...)
		choice, needles := initiativeProjectLinkCase(batch, "record_key", func(key string) string { return edits[key].before })
		query := `SELECT record_key,` + length + `,` + strings.Replace(shape, "$link", choice, 1) +
			` FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND ` + where + ` ORDER BY record_key` + lock
		params := append([]any{initiativeProjectLinkRowBytes}, needles...)
		rows, err := tx.QueryContext(ctx, query, append(params, args...)...)
		if err != nil {
			return false, err
		}
		// Close the bounded row window before looking up its locked versions.
		count, exact := 0, true
		for rows.Next() {
			var id string
			var size, shape int
			if err := rows.Scan(&id, &size, &shape); err != nil {
				rows.Close()
				return false, err
			}
			edit, ok := edits[id]
			if shape != 1 || !ok || size < len(edit.before) ||
				size-len(edit.before) > int(^uint(0)>>1)-len(edit.after)-charge {
				exact = false
				break
			}
			charge += size - len(edit.before) + len(edit.after)
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil || !exact || count != len(batch) {
			return false, err
		}
		rows, err = tx.QueryContext(ctx, `SELECT record_key,version FROM record_versions WHERE workspace_key=? AND field='projects' AND `+where+` ORDER BY record_key`+lock, args...)
		if err != nil {
			return false, err
		}
		count = 0
		for rows.Next() {
			var id string
			var version int64
			if err := rows.Scan(&id, &version); err != nil {
				rows.Close()
				return false, err
			}
			old, ok := base.state.records["projects"][id]
			exact = exact && ok && version == old.version
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil || !exact || count != len(batch) {
			return false, err
		}
	}
	// Account for the canonical bytes the database rewrites even though the
	// only parameters crossing the driver boundary are the changed links.
	if err := tx.admitStatementWriteBytes(charge); err != nil {
		return false, err
	}
	for start := 0; start < len(keys); start += initiativeProjectLinkBatch {
		batch := keys[start:min(start+initiativeProjectLinkBatch, len(keys))]
		where, ids := bindList("record_key", batch)
		oldChoice, oldArgs := initiativeProjectLinkCase(batch, "record_key", func(key string) string { return edits[key].before })
		newChoice, newArgs := initiativeProjectLinkCase(batch, "record_key", func(key string) string { return edits[key].after })
		args := append(append(append(oldArgs, newArgs...), workspace), ids...)
		expression := replace("REPLACE(" + text + "," + oldChoice + "," + newChoice + ")")
		result, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=`+expression+` WHERE workspace_key=? AND field='projects' AND `+where, args...)
		if err != nil {
			return false, err
		}
		if n, err := result.RowsAffected(); err != nil || n != int64(len(batch)) {
			return false, ErrConcurrentWrite
		}
		if err := writeInitiativeProjectLinkSearch(ctx, tx, workspace, batch, edits); err != nil {
			return false, err
		}
	}
	tx.metadataWritten = true
	tx.initiativeProjectLinks = links
	return true, nil
}

func initiativeProjectLinkJSON(ids []string, remaining *int) (string, bool) {
	for _, id := range ids {
		if len(id) > (*remaining-4)/6 || *remaining < 4 || !utf8.ValidString(id) || strings.ContainsRune(id, 0) {
			return "", false
		}
		*remaining -= len(id)*6 + 4
	}
	raw, err := json.Marshal(ids)
	needle := `,"initiatives":` + string(raw) + `,`
	*remaining -= len(needle)
	return needle, err == nil && *remaining >= 0
}

func initiativeProjectLinkCase(keys []string, column string, valueFor func(string) string) (string, []any) {
	first := valueFor(keys[0])
	if !slices.ContainsFunc(keys[1:], func(key string) bool { return valueFor(key) != first }) {
		return "?", []any{first}
	}
	args := make([]any, 0, len(keys)*2)
	for _, key := range keys {
		args = append(args, key, valueFor(key))
	}
	return "CASE " + column + strings.Repeat(" WHEN ? THEN ?", len(keys)) + " END", args
}

// The search trigger's JSON text differs from the application projection on
// some dialects. Project just the searchable fields as the ordinary write
// does, including on MySQL installations without triggers.
func initiativeProjectLinkSearch(project domain.Project, limit int, remaining *int) (string, bool, error) {
	for _, text := range []string{project.Name, project.Summary, project.Description, project.Status.ID, project.Status.Name,
		project.Status.Description, project.Status.Type, project.Status.Color} {
		if len(text) > *remaining {
			return "", false, nil
		}
		*remaining -= len(text)
	}
	raw, err := json.Marshal(struct {
		Name        string               `json:"name"`
		Summary     string               `json:"summary"`
		Description string               `json:"description"`
		Status      domain.ProjectStatus `json:"status"`
	}{project.Name, project.Summary, project.Description, project.Status})
	if err != nil {
		return "", false, err
	}
	content := capSearchText(metadataSearchText(StripJSONNUL(raw)), limit)
	*remaining -= len(content) + 256
	return content, *remaining >= 0, nil
}

func writeInitiativeProjectLinkSearch(ctx context.Context, tx *sqlTx, workspace string, batch []string, edits map[string]initiativeProjectLinkEdit) error {
	if tx.dialect != "mysql" {
		args := make([]any, 0, len(batch)*3)
		for _, key := range batch {
			args = append(args, workspace, key, edits[key].search)
		}
		values := strings.Repeat("(?, 'projects', ?, ?),", len(batch))
		_, err := tx.ExecContext(ctx, `INSERT INTO metadata_search_documents(workspace_key,field,record_key,content) VALUES `+strings.TrimSuffix(values, ",")+
			` ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET content=excluded.content WHERE metadata_search_documents.content<>excluded.content`, args...)
		return err
	}
	// Updating existing MySQL documents in place preserves their FULLTEXT
	// entries. A multi-row duplicate-key update can drop them from the index.
	choice, content := initiativeProjectLinkCase(batch, "record_key", func(key string) string { return edits[key].search })
	where, keys := bindList("record_key", batch)
	args := append(append(append([]any{}, content...), workspace), keys...)
	if _, err := tx.ExecContext(ctx, `UPDATE metadata_search_documents SET content=`+choice+
		` WHERE workspace_key=? AND field='projects' AND `+where+` AND content<>`+choice, append(args, content...)...); err != nil {
		return err
	}
	choice, content = initiativeProjectLinkCase(batch, "m.record_key", func(key string) string { return edits[key].search })
	where, keys = bindList("m.record_key", batch)
	_, err := tx.ExecContext(ctx, `INSERT INTO metadata_search_documents(workspace_key,field,record_key,content)
		SELECT m.workspace_key,m.field,m.record_key,`+choice+` FROM workspace_metadata_records m
		WHERE m.workspace_key=? AND m.field='projects' AND `+where+` AND NOT EXISTS (SELECT 1 FROM metadata_search_documents d
		WHERE d.workspace_key=m.workspace_key AND d.field=m.field AND CAST(d.record_key AS BINARY)=m.record_key)
		ON DUPLICATE KEY UPDATE content=IF(content<>VALUES(content),VALUES(content),content)`, append(append(content, workspace), keys...)...)
	return err
}

// Replace a single proven top-level member byte for byte. Re-serializing via
// a database JSON type can collapse repeated keys elsewhere in a legacy row.
// Escaped member names and ambiguous occurrences use the ordinary rebaser.
func initiativeProjectLinkSQL(dialect string) (text, length, find string, binaryResult func(string) string, eligible string) {
	var escape, folded, rootType, valid string
	switch dialect {
	case "mysql":
		text = "(CONVERT(data USING utf8mb4) COLLATE utf8mb4_bin)"
		length = "OCTET_LENGTH(data)"
		find = "LOCATE(CAST(($link) AS BINARY),data)>0"
		binaryResult = func(value string) string { return "CAST(" + value + " AS BINARY)" }
		escape = "LOCATE(CONCAT(CHAR(92),'u'),data)=0"
		rootType = "LOWER(JSON_TYPE(JSON_EXTRACT(" + text + ",'$.initiatives')))"
		valid = "CAST(" + text + " AS BINARY)=data AND JSON_VALID(" + text + ")"
	case "postgres":
		text = `(flow_utf8(data) COLLATE "C")`
		length = "OCTET_LENGTH(data)"
		find = "STRPOS(" + text + ",($link))>0"
		binaryResult = func(value string) string { return "convert_to(" + value + ",'UTF8')" }
		escape = "STRPOS(" + text + ",CHR(92)||'u')=0"
		rootType = "json_typeof(" + text + "::json->'initiatives')"
		valid = "convert_to(" + text + ",'UTF8')=data"
	default:
		text = "CAST(data AS TEXT)"
		length = "LENGTH(CAST(data AS BLOB))"
		find = "INSTR(" + text + ",($link))>0"
		binaryResult = func(value string) string { return "CAST(" + value + " AS BLOB)" }
		escape = "INSTR(" + text + ",CHAR(92)||'u')=0"
		rootType = "json_type(" + text + ",'$.initiatives')"
		valid = "json_valid(" + text + ")=1"
	}
	// Match Go's JSON key folding independently of the database locale.
	folded = "REPLACE(LOWER(" + text + "),'\u017f','s')"
	eligible = "CASE WHEN " + escape + " AND " + valid + " THEN " +
		`LENGTH(` + folded + `)-LENGTH(REPLACE(` + folded + `,'"initiatives"',''))=13 AND ` +
		rootType + " IN ('array','null') ELSE FALSE END"
	return
}

func (links initiativeProjectLinks) matches(change *coldPublishChange, before, after *coldPublishState) bool {
	if len(links) == 0 || change == nil || change.root || change.manifest || len(change.keys) != len(links) {
		return false
	}
	for _, key := range change.keys {
		if _, ok := links[key]; !ok {
			return false
		}
		old, ok := before.records["projects"][key]
		if !ok {
			return false
		}
		old.version++
		if after.records["projects"][key] != old {
			return false
		}
	}
	return true
}

func appendColdProjectLink(ctx context.Context, b *metadataLogicalBuilder, item metadataLogicalNode, id string, links []string) error {
	if match, err := coldPublishItemID(ctx, item, id); err != nil {
		return err
	} else if !match {
		return coldPublishInexact("installed item key")
	}
	node, err := b.writeAggregateNode(metadataLogicalRetainedRecord{fresh: item, keep: map[string]metadataAggregateNode{
		"Initiatives": metadataAggregateNativeNode{reflect.ValueOf(links)},
	}}, 0)
	if err != nil {
		return err
	}
	b.collectionIndex.add(node)
	b.cost += node.cost + uint64(item.typ.Size())*2
	b.unavailable = b.unavailable || node.unavailable
	b.count++
	return nil
}
