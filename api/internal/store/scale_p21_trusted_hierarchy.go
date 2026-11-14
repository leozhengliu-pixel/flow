package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

const metadataHierarchyWork = 16 << 20
const metadataHierarchyChunk = 32 << 10

// TrustedWorkflowHierarchyIndependent evaluates the legacy root/descendant
// inheritance predicate against this installed generation. It does not
// authorize a viewer. Keys, graph membership and the traversal queue are
// disk-backed; neither key size nor graph cardinality expands Go working memory.
func (r *InstalledMetadataRead) TrustedWorkflowHierarchyIndependent(ctx context.Context, root string) (bool, error) {
	bound, err := r.Context(ctx)
	if err != nil {
		return false, err
	}
	work, release, err := r.store.tryAdmitAdditionalMetadataWork(bound, metadataHierarchyWork)
	if err != nil {
		return false, err
	}
	defer release()
	index, err := newMetadataHierarchyIndex(work)
	if err != nil {
		return false, err
	}
	defer index.close()
	emptyID, err := index.intern(work, strings.NewReader(""))
	if err != nil {
		return false, err
	}
	rootID, err := index.intern(work, strings.NewReader(root))
	if err != nil {
		return false, err
	}
	_, err = r.Visit(work, []string{"teamSettings"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
		id, err := index.internRef(ctx, entry.Key)
		if err != nil {
			return err
		}
		var parent int64
		if err := entry.Value.Child(ctx, []string{"parentTeamId"}, func(ctx context.Context, shape InstalledMetadataShape, ref InstalledMetadataRef) error {
			if !shape.Found {
				return ErrMetadataRecordQuery
			}
			var err error
			parent, err = index.internRef(ctx, ref)
			return err
		}); err != nil {
			return err
		}
		inherits := false
		if err := entry.Value.Child(ctx, []string{"inheritWorkflowStatuses"}, func(ctx context.Context, shape InstalledMetadataShape, ref InstalledMetadataRef) error {
			if !shape.Found {
				return ErrMetadataRecordQuery
			}
			return ref.WithValue(ctx, 0, func(value any) error {
				inherits = value.(bool)
				return nil
			})
		}); err != nil {
			return err
		}
		_, err = index.exec(ctx, `UPDATE nodes SET parent=?,inherits=? WHERE id=?`, parent, inherits, id)
		return err
	})
	if err != nil {
		return false, err
	}
	_, err = r.Visit(work, []string{"teams"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
		var id int64
		if err := entry.Value.Child(ctx, []string{"id"}, func(ctx context.Context, shape InstalledMetadataShape, ref InstalledMetadataRef) error {
			if !shape.Found {
				return ErrMetadataRecordQuery
			}
			var err error
			id, err = index.internRef(ctx, ref)
			return err
		}); err != nil {
			return err
		}
		active := true
		if err := entry.Value.Child(ctx, []string{"retiredAt"}, func(ctx context.Context, shape InstalledMetadataShape, _ InstalledMetadataRef) error {
			active = !shape.Found || shape.Nil
			return nil
		}); err != nil {
			return err
		}
		// RebuildTeamDirectory uses the last duplicate ID for retirement.
		// A last active row also supplies the nonretired child edge; every
		// occurrence uses the same settings map entry for its parent.
		_, err := index.exec(ctx, `UPDATE nodes SET has_team=1,active=?,parent=COALESCE(parent,?) WHERE id=?`, active, emptyID, id)
		return err
	})
	if err != nil {
		return false, err
	}
	return index.independent(work, rootID)
}

func (index *metadataHierarchyIndex) independent(ctx context.Context, rootID int64) (bool, error) {
	if _, err := index.exec(ctx, `DELETE FROM queue`); err != nil {
		return false, err
	}
	if _, err := index.exec(ctx, `INSERT INTO queue(node) VALUES(?)`, rootID); err != nil {
		return false, err
	}
	last := int64(0)
	for {
		var slot, id int64
		err := index.tx.QueryRowContext(ctx, `SELECT slot,node FROM queue WHERE slot>? ORDER BY slot LIMIT 1`, last).Scan(&slot, &id)
		if errors.Is(err, sql.ErrNoRows) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		last = slot
		var inherits bool
		var parentBytes int64
		if err := index.tx.QueryRowContext(ctx, `SELECT n.inherits,COALESCE(p.size,0) FROM nodes n LEFT JOIN nodes p ON p.id=n.parent WHERE n.id=?`, id).Scan(&inherits, &parentBytes); err != nil {
			return false, err
		}
		if inherits && parentBytes != 0 {
			return false, nil
		}
		previous := int64(0)
		for {
			var child int64
			err := index.tx.QueryRowContext(ctx, `SELECT id FROM nodes WHERE parent=? AND has_team=1 AND active=1 AND id>? ORDER BY id LIMIT 1`, id, previous).Scan(&child)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				return false, err
			}
			previous = child
			// The disk unique index terminates cycles and duplicate edges.
			if _, err := index.exec(ctx, `INSERT OR IGNORE INTO queue(node) VALUES(?)`, child); err != nil {
				return false, err
			}
		}
	}
}

type metadataHierarchyIndex struct {
	directory string
	db        *sql.DB
	tx        *sql.Tx
	keys      *os.File
	buffer    [metadataHierarchyChunk]byte
	left      [metadataHierarchyChunk]byte
	right     [metadataHierarchyChunk]byte
	dbBytes   int64
	keyBytes  int64
}

func newMetadataHierarchyIndex(ctx context.Context) (_ *metadataHierarchyIndex, err error) {
	index := &metadataHierarchyIndex{}
	config, _ := ctx.Value(metadataLogicalScratchKey{}).(metadataLogicalScratch)
	index.directory, err = os.MkdirTemp(config.directory, "flow-p21-hierarchy-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			index.close()
		}
	}()
	if !reserveMetadataLogicalBytes(&metadataLogicalDiskUsed, 1<<20, metadataLogicalDiskBytes) {
		return nil, ErrRecordSpoolBudget
	}
	index.dbBytes = 1 << 20
	index.keys, err = os.OpenFile(filepath.Join(index.directory, "keys"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	index.db, err = sql.Open("sqlite", filepath.Join(index.directory, "graph.db"))
	if err != nil {
		return nil, err
	}
	index.db.SetMaxOpenConns(1)
	index.db.SetMaxIdleConns(1)
	for _, statement := range []string{
		`PRAGMA journal_mode=OFF`,
		`PRAGMA synchronous=OFF`,
		`PRAGMA page_size=4096`,
		`PRAGMA cache_size=-1024`,
		`PRAGMA temp_store=FILE`,
		`PRAGMA temp.cache_size=-128`,
		`PRAGMA mmap_size=0`,
		`PRAGMA cache_spill=ON`,
		`PRAGMA max_page_count=256`,
		`CREATE TABLE nodes(id INTEGER PRIMARY KEY,hash BLOB NOT NULL,offset INTEGER NOT NULL,size INTEGER NOT NULL,has_team INTEGER NOT NULL DEFAULT 0,active INTEGER NOT NULL DEFAULT 0,parent INTEGER,inherits INTEGER NOT NULL DEFAULT 0)`,
		`CREATE INDEX node_hash ON nodes(hash,id)`,
		`CREATE INDEX node_parent ON nodes(parent,has_team,active,id)`,
		`CREATE TABLE queue(slot INTEGER PRIMARY KEY AUTOINCREMENT,node INTEGER NOT NULL UNIQUE)`,
	} {
		if _, err = index.db.ExecContext(ctx, statement); err != nil {
			return nil, err
		}
	}
	index.tx, err = index.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return index, nil
}

func (i *metadataHierarchyIndex) close() {
	if i.tx != nil {
		_ = i.tx.Rollback()
	}
	if i.db != nil {
		_ = i.db.Close()
	}
	if i.keys != nil {
		_ = i.keys.Close()
	}
	if i.directory != "" {
		if err := os.RemoveAll(i.directory); err == nil || errors.Is(err, os.ErrNotExist) {
			metadataLogicalDiskUsed.Add(-i.dbBytes - i.keyBytes)
			i.dbBytes, i.keyBytes = 0, 0
			i.directory = ""
		}
	}
}

func (i *metadataHierarchyIndex) exec(ctx context.Context, statement string, args ...any) (sql.Result, error) {
	var pages int64
	if err := i.tx.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return nil, err
	}
	// Each mutation writes fixed-size keys/integers only. Reserve room for
	// B-tree splits before SQLite can grow its enforced physical page cap.
	if pages*4096+(64<<10) > i.dbBytes {
		if !reserveMetadataLogicalBytes(&metadataLogicalDiskUsed, 1<<20, metadataLogicalDiskBytes) {
			return nil, ErrRecordSpoolBudget
		}
		i.dbBytes += 1 << 20
		if _, err := i.tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA max_page_count=%d`, i.dbBytes/4096)); err != nil {
			return nil, err
		}
	}
	return i.tx.ExecContext(ctx, statement, args...)
}

func (i *metadataHierarchyIndex) internRef(ctx context.Context, ref InstalledMetadataRef) (int64, error) {
	var id int64
	err := metadataHierarchyRefBytes(ctx, ref, func(reader io.Reader) error {
		var err error
		id, err = i.intern(ctx, reader)
		return err
	})
	return id, err
}

func metadataHierarchyRefBytes(ctx context.Context, ref InstalledMetadataRef, visit func(io.Reader) error) error {
	if !ref.native.IsValid() {
		return ref.StreamBytes(ctx, visit)
	}
	if ref.borrow == nil || ref.native.Kind() != reflect.String {
		return ErrMetadataRecordQuery
	}
	ref.borrow.mu.RLock()
	defer ref.borrow.mu.RUnlock()
	if err := ref.borrow.live(ctx); err != nil {
		return err
	}
	if err := visit(strings.NewReader(ref.native.String())); err != nil {
		return err
	}
	return ref.borrow.live(ctx)
}

func (i *metadataHierarchyIndex) intern(ctx context.Context, reader io.Reader) (int64, error) {
	offset, err := i.keys.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	hash := sha256.New()
	size := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, readErr := reader.Read(i.buffer[:])
		if n != 0 {
			if !reserveMetadataLogicalBytes(&metadataLogicalDiskUsed, int64(n), metadataLogicalDiskBytes) {
				return 0, ErrRecordSpoolBudget
			}
			i.keyBytes += int64(n)
			if written, err := i.keys.Write(i.buffer[:n]); err != nil {
				return 0, err
			} else if written != n {
				return 0, io.ErrShortWrite
			}
			_, _ = hash.Write(i.buffer[:n])
			size += int64(n)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return 0, readErr
		}
	}
	digest := hash.Sum(nil)
	previous := int64(0)
	for {
		var id, storedOffset, storedSize int64
		err := i.tx.QueryRowContext(ctx, `SELECT id,offset,size FROM nodes WHERE hash=? AND id>? ORDER BY id LIMIT 1`, digest, previous).Scan(&id, &storedOffset, &storedSize)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return 0, err
		}
		previous = id
		equal := storedSize == size
		for position := int64(0); equal && position < size; {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			length := min(int64(len(i.left)), size-position)
			if _, err := i.keys.ReadAt(i.left[:length], storedOffset+position); err != nil {
				return 0, err
			}
			if _, err := i.keys.ReadAt(i.right[:length], offset+position); err != nil {
				return 0, err
			}
			equal = bytes.Equal(i.left[:length], i.right[:length])
			position += length
		}
		if equal {
			if err := i.keys.Truncate(offset); err != nil {
				return 0, err
			}
			metadataLogicalDiskUsed.Add(-size)
			i.keyBytes -= size
			return id, nil
		}
	}
	result, err := i.exec(ctx, `INSERT INTO nodes(hash,offset,size) VALUES(?,?,?)`, digest, offset, size)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}
