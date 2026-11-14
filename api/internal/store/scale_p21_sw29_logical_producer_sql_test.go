package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sync/atomic"
	"testing"

	"flow/api/internal/domain"

	"modernc.org/sqlite"
)

var sw29ProducerDriverSequence atomic.Uint64

type sw29ProducerSQLDriver struct {
	base  sqlite.Driver
	calls atomic.Int64
}

func (d *sw29ProducerSQLDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &sw29ProducerSQLConn{Conn: conn, driver: d}, nil
}

type sw29ProducerSQLConn struct {
	driver.Conn
	driver *sw29ProducerSQLDriver
}

func (c *sw29ProducerSQLConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.driver.calls.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *sw29ProducerSQLConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.driver.calls.Add(1)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *sw29ProducerSQLConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func TestScaleP21SW29LogicalProducerPointLocalSQL(t *testing.T) {
	for _, count := range []int{32, 256} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ctx := t.Context()
			data := domain.Bootstrap{Workspace: domain.Workspace{ID: "sql-count"}}
			for i := 0; i < count; i++ {
				data.CustomEmojis = append(data.CustomEmojis, domain.CustomEmoji{ID: fmt.Sprint(i), Name: "point"})
			}
			typed, err := captureMetadataLogicalSource(ctx, data, copyStamp{"sql-count", -1})
			if err != nil {
				t.Fatal(err)
			}
			defer typed.Release()
			packed, err := metadataLogicalUnitsImport(ctx, typed)
			if err != nil {
				t.Fatal(err)
			}
			defer packed.Release()
			counter := &sw29ProducerSQLDriver{}
			name := fmt.Sprintf("scale-p21-sw29-count-%d", sw29ProducerDriverSequence.Add(1))
			sql.Register(name, counter)
			a := packed.units.arena
			counted, err := sql.Open(name, a.index.stage.path)
			if err != nil {
				t.Fatal(err)
			}
			counted.SetMaxOpenConns(1)
			original := a.index.stage.db
			a.index.stage.db = counted
			for _, node := range packed.fields {
				if node.source == nil {
					continue
				}
				if file, ok := node.source.file.(*metadataLogicalUnitFile); ok {
					file.db = counted
				}
			}
			defer func() {
				a.index.stage.db = original
				for _, node := range packed.fields {
					if node.source == nil {
						continue
					}
					if file, ok := node.source.file.(*metadataLogicalUnitFile); ok {
						file.db = original
					}
				}
				if err := counted.Close(); err != nil {
					t.Error(err)
				}
			}()
			scope := MetadataRecordScope{Read: []RecordKey{{Kind: "customEmojis", ID: "0"}}}
			if _, err := typed.Select(ctx, scope, 2<<20); err != nil {
				t.Fatal(err)
			}
			if counter.calls.Load() != 0 {
				t.Fatal("typed source unexpectedly used the packed arena")
			}
			got, err := packed.Select(ctx, scope, 2<<20)
			if err != nil || len(got.CustomEmojis) != 1 || got.CustomEmojis[0].ID != "0" {
				t.Fatal("counted point selection changed its result", err)
			}
			calls := counter.calls.Load()
			t.Logf("records=%d typed_local_sql=0 packed_local_sql=%d canonical_sql=0", count, calls)
			// The index has a hard 128-node path limit. This small scalar
			// fixture allows for both range walks plus all framed values.
			if calls == 0 || calls > 512 {
				t.Fatal("point selection exceeded its bounded local SQL allowance", calls)
			}
		})
	}
}
