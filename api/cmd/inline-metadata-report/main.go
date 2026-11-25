// Command inline-metadata-report lists, per workspace, the metadata
// collections the database keeps inline (or, for project and initiative
// updates, one row per project) because an entry's ID is shared, empty or
// longer than a row key. Edits to such collections could be lost before the
// inline metadata fixes; the report shows how many workspaces are affected.
//
// It only reads: SQLite is opened read-only and MySQL/PostgreSQL queries run
// in a read-only transaction. It changes and repairs nothing. Build it
// outside the source tree and point it at a copy or replica:
//
//	cd api && go build -o /tmp/inline-metadata-report ./cmd/inline-metadata-report
//	/tmp/inline-metadata-report -driver sqlite -dsn /path/to/copy-of-flow.db
//	/tmp/inline-metadata-report -driver postgres -dsn 'postgres://user:pass@host/flow'
//	/tmp/inline-metadata-report -driver mysql -dsn 'user:pass@tcp(host:3306)/flow' -json
//
// The mysql DSN is the go-sql-driver form, not a URL. Output is one line per
// affected field (workspace, field, kind, stored form and the entry counts
// that keep it inline) and a summary; -json prints the same as JSON.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"flow/api/internal/store"

	_ "github.com/go-sql-driver/mysql"
)

type workspaceReport struct {
	Workspace string                      `json:"workspace"`
	Fields    []store.InlineMetadataField `json:"fields"`
}

func main() {
	driver := flag.String("driver", "sqlite", "sqlite, postgres or mysql")
	dsn := flag.String("dsn", "", "database file (sqlite) or connection string")
	asJSON := flag.Bool("json", false, "print JSON")
	flag.Parse()
	if strings.TrimSpace(*dsn) == "" {
		log.Fatal("-dsn is required")
	}
	reports, err := run(context.Background(), *driver, *dsn)
	if err != nil {
		log.Fatal(err)
	}
	if *asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(reports); err != nil {
			log.Fatal(err)
		}
		return
	}
	fields := 0
	for _, report := range reports {
		for _, field := range report.Fields {
			fields++
			fmt.Printf("%s\t%s\t%s\t%s\tentries=%d duplicate=%d empty=%d overlong=%d unreadable=%d\n",
				report.Workspace, field.Field, field.Kind, field.Stored, field.Entries, field.DuplicateIDs, field.EmptyIDs, field.OverlongIDs, field.Unreadable)
		}
	}
	fmt.Printf("affected workspaces=%d fields=%d\n", len(reports), fields)
}

func run(ctx context.Context, driver, dsn string) ([]workspaceReport, error) {
	sqlDriver, placeholder := driver, "?"
	switch driver {
	case "sqlite":
		dsn = "file:" + dsn + "?mode=ro"
	case "postgres", "postgresql":
		sqlDriver, placeholder = "pgx", "$1"
	case "mysql":
	default:
		return nil, fmt.Errorf("unsupported driver %q", driver)
	}
	db, err := sql.Open(sqlDriver, dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT workspace_key,data FROM workspace_states ORDER BY workspace_key`)
	if err != nil {
		return nil, err
	}
	type root struct {
		workspace string
		data      []byte
	}
	var roots []root
	for rows.Next() {
		var item root
		if err := rows.Scan(&item.workspace, &item.data); err != nil {
			rows.Close()
			return nil, err
		}
		roots = append(roots, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var reports []workspaceReport
	for _, item := range roots {
		perParent := func(field string) (map[string]json.RawMessage, error) {
			rows, err := tx.QueryContext(ctx, `SELECT record_key,data FROM workspace_metadata_records WHERE workspace_key=`+placeholder+` AND field='`+field+`'`, item.workspace)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			result := map[string]json.RawMessage{}
			for rows.Next() {
				var key string
				var data []byte
				if err := rows.Scan(&key, &data); err != nil {
					return nil, err
				}
				result[key] = data
			}
			return result, rows.Err()
		}
		fields, err := store.InlineMetadataFields(item.data, perParent)
		if err != nil {
			return nil, fmt.Errorf("workspace %s: %w", item.workspace, err)
		}
		if len(fields) > 0 {
			reports = append(reports, workspaceReport{Workspace: item.workspace, Fields: fields})
		}
	}
	return reports, tx.Commit()
}
