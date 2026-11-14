package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"sync"
	"time"
)

const metadataLogicalUnitChunk = 32 << 10
const metadataLogicalUnitArenaDescriptor = 1 << 20
const metadataLogicalUnitRootDescriptor = 64 << 10

type metadataLogicalUnitTrees [6]uint64

type metadataLogicalUnitRegistry struct {
	types     []reflect.Type
	times     []time.Time
	locations []*time.Location
	charge    int64
}

// All mutable arena state is serialized by mu. Published roots and units are
// immutable; only graph reference counts and unreachable rows are changed.
type metadataLogicalUnitsArena struct {
	mu         sync.Mutex
	index      *sw29LogicalIndex
	registries map[uint64]*metadataLogicalUnitRegistry
	imports    map[*metadataLogicalStorage]uint64
	roots      int
	closed     bool
	unitBytes  uint64
	cleanupErr error
	types      [4096]reflect.Type
	typeIDs    map[reflect.Type]uint32
	chunks     metadataLogicalUnitChunkCache
}

type metadataLogicalUnitsRoot struct {
	arena      *metadataLogicalUnitsArena
	id         uint64
	refs       int
	trees      metadataLogicalUnitTrees
	stamp      copyStamp
	generation [16]byte
	fields     []metadataLogicalUnitsField
}

func newMetadataLogicalUnitsArena(ctx context.Context) (_ *metadataLogicalUnitsArena, err error) {
	if !reserveMetadataLogicalBytes(&metadataLogicalDescriptorsUsed, metadataLogicalUnitArenaDescriptor, metadataLogicalDescriptorLimit) {
		return nil, ErrMetadataWorkBudget
	}
	a := &metadataLogicalUnitsArena{
		registries: map[uint64]*metadataLogicalUnitRegistry{},
		imports:    map[*metadataLogicalStorage]uint64{},
		typeIDs:    map[reflect.Type]uint32{},
	}
	defer func() {
		if err != nil {
			_ = a.closeLocked()
		}
	}()
	config, _ := ctx.Value(metadataLogicalScratchKey{}).(metadataLogicalScratch)
	config.logical = true
	ctx = context.WithValue(ctx, metadataLogicalScratchKey{}, config)
	a.index, err = sw29NewLogicalIndex(ctx)
	if err != nil {
		return nil, err
	}
	// Rollback journal space shares the reservation with the main file.
	// A point transaction may update reference-count pages but never bodies.
	for _, query := range []string{
		"PRAGMA journal_mode=DELETE",
		"ALTER TABLE logical_nodes ADD COLUMN refs INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE logical_nodes ADD COLUMN unit INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE logical_nodes ADD COLUMN otherunit INTEGER NOT NULL DEFAULT 0",
		"CREATE INDEX logical_nodes_refs ON logical_nodes(refs)",
		"CREATE TABLE logical_unit_registries(id INTEGER PRIMARY KEY,refs INTEGER NOT NULL DEFAULT 0)",
		"CREATE TABLE logical_units(id INTEGER PRIMARY KEY AUTOINCREMENT,registry INTEGER NOT NULL,size INTEGER NOT NULL,refs INTEGER NOT NULL DEFAULT 0)",
		"CREATE INDEX logical_units_refs ON logical_units(refs)",
		"CREATE TABLE logical_unit_chunks(unit INTEGER NOT NULL,chunk INTEGER NOT NULL,data BLOB NOT NULL,PRIMARY KEY(unit,chunk))",
		"CREATE TABLE logical_unit_facts(unit INTEGER NOT NULL,tree INTEGER NOT NULL,key BLOB NOT NULL,PRIMARY KEY(unit,tree,key))",
		"CREATE TABLE logical_unit_roots(id INTEGER PRIMARY KEY,a INTEGER NOT NULL,b INTEGER NOT NULL,c INTEGER NOT NULL,d INTEGER NOT NULL,e INTEGER NOT NULL,f INTEGER NOT NULL,generation BLOB NOT NULL)",
		`CREATE TRIGGER logical_node_add AFTER INSERT ON logical_nodes BEGIN
			UPDATE logical_nodes SET refs=refs+1 WHERE id=NEW.lft;
			UPDATE logical_nodes SET refs=refs+1 WHERE id=NEW.rgt;
			UPDATE logical_units SET refs=refs+1 WHERE id=NEW.unit;
			UPDATE logical_units SET refs=refs+1 WHERE id=NEW.otherunit; END`,
		`CREATE TRIGGER logical_node_remove AFTER DELETE ON logical_nodes BEGIN
			UPDATE logical_nodes SET refs=refs-1 WHERE id=OLD.lft;
			UPDATE logical_nodes SET refs=refs-1 WHERE id=OLD.rgt;
			UPDATE logical_units SET refs=refs-1 WHERE id=OLD.unit;
			UPDATE logical_units SET refs=refs-1 WHERE id=OLD.otherunit; END`,
		`CREATE TRIGGER logical_unit_add AFTER INSERT ON logical_units BEGIN
			UPDATE logical_unit_registries SET refs=refs+1 WHERE id=NEW.registry; END`,
		`CREATE TRIGGER logical_unit_remove AFTER DELETE ON logical_units BEGIN
			DELETE FROM logical_unit_chunks WHERE unit=OLD.id;
			DELETE FROM logical_unit_facts WHERE unit=OLD.id;
			UPDATE logical_unit_registries SET refs=refs-1 WHERE id=OLD.registry; END`,
		`CREATE TRIGGER logical_root_add AFTER INSERT ON logical_unit_roots BEGIN
			UPDATE logical_nodes SET refs=refs+1 WHERE id=NEW.a;
			UPDATE logical_nodes SET refs=refs+1 WHERE id=NEW.b;
			UPDATE logical_nodes SET refs=refs+1 WHERE id=NEW.c;
			UPDATE logical_nodes SET refs=refs+1 WHERE id=NEW.d;
			UPDATE logical_nodes SET refs=refs+1 WHERE id=NEW.e;
			UPDATE logical_nodes SET refs=refs+1 WHERE id=NEW.f; END`,
		`CREATE TRIGGER logical_root_remove AFTER DELETE ON logical_unit_roots BEGIN
			UPDATE logical_nodes SET refs=refs-1 WHERE id=OLD.a;
			UPDATE logical_nodes SET refs=refs-1 WHERE id=OLD.b;
			UPDATE logical_nodes SET refs=refs-1 WHERE id=OLD.c;
			UPDATE logical_nodes SET refs=refs-1 WHERE id=OLD.d;
			UPDATE logical_nodes SET refs=refs-1 WHERE id=OLD.e;
			UPDATE logical_nodes SET refs=refs-1 WHERE id=OLD.f; END`,
	} {
		if _, err := a.index.stage.exec(ctx, 4096, query); err != nil {
			return nil, err
		}
	}
	a.index.unit = func(value []byte) uint64 {
		if len(value) >= 9 && (value[0] == 1 || value[0] == 3) {
			return binary.BigEndian.Uint64(value[1:9])
		}
		return 0
	}
	a.index.otherUnit = func(value []byte) uint64 {
		if len(value) == 96 && (value[0] == 1 || value[0] == 2) && value[48] == 1 {
			return binary.BigEndian.Uint64(value[49:57])
		}
		return 0
	}
	return a, nil
}

func (a *metadataLogicalUnitsArena) beginLocked(ctx context.Context) (*sql.Tx, error) {
	if a.closed {
		return nil, errMetadataLogicalClosed
	}
	tx, err := a.index.stage.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	a.index.tx = tx
	a.index.exec = func(ctx context.Context, bytes int64, query string, args ...any) (sql.Result, error) {
		return a.execLocked(ctx, tx, bytes, query, args...)
	}
	return tx, nil
}

func (a *metadataLogicalUnitsArena) endLocked() {
	a.index.tx, a.index.exec = nil, nil
}

func (a *metadataLogicalUnitsArena) execLocked(ctx context.Context, tx *sql.Tx, bytes int64, query string, args ...any) (sql.Result, error) {
	var pages, pageSize int64
	if err := tx.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return nil, err
	}
	if bytes < 0 || bytes > metadataPolicyBytes {
		return nil, ErrMetadataWorkBudget
	}
	stage := a.index.stage
	want := 2 * (pages*pageSize + bytes*4 + 64<<10)
	if want > stage.reserved {
		grow := ((want - stage.reserved + sw26MigrationStageChunk - 1) / sw26MigrationStageChunk) * sw26MigrationStageChunk
		if err := stage.reserve(grow); err != nil {
			return nil, err
		}
		stage.reserved += grow
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA max_page_count=%d", stage.reserved/(2*pageSize))); err != nil {
		return nil, err
	}
	return tx.ExecContext(ctx, query, args...)
}

func (a *metadataLogicalUnitsArena) registryLocked(ctx context.Context, tx *sql.Tx, source *metadataLogicalStorage) (uint64, error) {
	if id := a.imports[source]; id != 0 {
		return id, nil
	}
	for id, registry := range a.registries {
		if slices.Equal(registry.types, source.types) && slices.Equal(registry.times, source.times) &&
			slices.Equal(registry.locations, source.locations) {
			a.imports[source] = id
			return id, nil
		}
	}
	charge := int64(metadataLogicalDescriptorBytes) + source.locationBytes
	if !reserveMetadataLogicalBytes(&metadataLogicalDescriptorsUsed, charge, metadataLogicalDescriptorLimit) {
		return 0, ErrMetadataWorkBudget
	}
	result, err := a.execLocked(ctx, tx, 128, `INSERT INTO logical_unit_registries DEFAULT VALUES`)
	if err != nil {
		metadataLogicalDescriptorsUsed.Add(-charge)
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		metadataLogicalDescriptorsUsed.Add(-charge)
		return 0, err
	}
	a.registries[uint64(id)] = &metadataLogicalUnitRegistry{
		types: slices.Clone(source.types), times: slices.Clone(source.times),
		locations: slices.Clone(source.locations), charge: charge,
	}
	a.imports[source] = uint64(id)
	return uint64(id), nil
}

func (a *metadataLogicalUnitsArena) importLocked(ctx context.Context, tx *sql.Tx, node metadataLogicalNode) (uint64, error) {
	registry, err := a.registryLocked(ctx, tx, node.source)
	if err != nil {
		return 0, err
	}
	result, err := a.execLocked(ctx, tx, 128, `INSERT INTO logical_units(registry,size) VALUES(?,?)`, registry, node.size)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	var scratch [metadataLogicalUnitChunk]byte
	for offset := int64(0); offset < node.size; {
		n := min(int64(len(scratch)), node.size-offset)
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if _, err := metadataLogicalReadAt(ctx, node.source.file, scratch[:n], node.offset+offset); err != nil {
			return 0, err
		}
		if _, err := a.execLocked(ctx, tx, n+64, `INSERT INTO logical_unit_chunks(unit,chunk,data) VALUES(?,?,?)`, id, offset/metadataLogicalUnitChunk, scratch[:n]); err != nil {
			return 0, err
		}
		offset += n
		a.unitBytes += uint64(n)
	}
	return uint64(id), nil
}

func (a *metadataLogicalUnitsArena) rootLocked(ctx context.Context, tx *sql.Tx, trees metadataLogicalUnitTrees, stamp copyStamp) (*metadataLogicalUnitsRoot, error) {
	if !reserveMetadataLogicalBytes(&metadataLogicalDescriptorsUsed, metadataLogicalUnitRootDescriptor, metadataLogicalDescriptorLimit) {
		return nil, ErrMetadataWorkBudget
	}
	success := false
	defer func() {
		if !success {
			metadataLogicalDescriptorsUsed.Add(-metadataLogicalUnitRootDescriptor)
		}
	}()
	root := &metadataLogicalUnitsRoot{arena: a, trees: trees, stamp: stamp, refs: 1}
	if _, err := rand.Read(root.generation[:]); err != nil {
		return nil, err
	}
	result, err := a.execLocked(ctx, tx, 128, `INSERT INTO logical_unit_roots(a,b,c,d,e,f,generation) VALUES(?,?,?,?,?,?,?)`,
		trees[0], trees[1], trees[2], trees[3], trees[4], trees[5], root.generation[:])
	if err != nil {
		return nil, err
	}
	id, err := result.LastInsertId()
	root.id = uint64(id)
	success = err == nil
	return root, err
}

func (r *metadataLogicalUnitsRoot) Retain() error {
	a := r.arena
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || r.refs == 0 {
		return errMetadataLogicalClosed
	}
	r.refs++
	return nil
}

func (r *metadataLogicalUnitsRoot) Release() {
	if r == nil {
		return
	}
	a := r.arena
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.refs == 0 {
		return
	}
	r.refs--
	if r.refs != 0 {
		return
	}
	metadataLogicalDescriptorsUsed.Add(-metadataLogicalUnitRootDescriptor)
	// Close is unconditional local cleanup: no canceled request context may
	// prevent releasing a root edge. This does not create background work.
	if _, err := a.index.stage.db.Exec(`DELETE FROM logical_unit_roots WHERE id=?`, r.id); err != nil {
		a.cleanupErr = errors.Join(a.cleanupErr, err)
	}
	a.roots--
	if a.roots == 0 {
		_ = a.closeLocked()
		return
	}
	for _, table := range []string{"logical_nodes", "logical_units"} {
		for {
			result, err := a.index.stage.db.Exec(`DELETE FROM ` + table + ` WHERE id IN(SELECT id FROM ` + table + ` WHERE refs=0 LIMIT 32)`)
			if err != nil {
				a.cleanupErr = errors.Join(a.cleanupErr, err)
				return
			}
			count, err := result.RowsAffected()
			if err != nil {
				a.cleanupErr = errors.Join(a.cleanupErr, err)
				return
			}
			if count == 0 {
				break
			}
		}
	}
	if err := a.sweepRegistriesLocked(); err != nil {
		a.cleanupErr = errors.Join(a.cleanupErr, err)
	}
}

func (a *metadataLogicalUnitsArena) collectLocked(ctx context.Context, tx *sql.Tx) error {
	for {
		result, err := a.execLocked(ctx, tx, 0, `DELETE FROM logical_nodes WHERE id IN(SELECT id FROM logical_nodes WHERE refs=0 LIMIT 32)`)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			break
		}
	}
	for {
		result, err := a.execLocked(ctx, tx, 0, `DELETE FROM logical_units WHERE id IN(SELECT id FROM logical_units WHERE refs=0 LIMIT 32)`)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			break
		}
	}
	return nil
}

// Synchronize Go-only typed receipts only after SQL commit or rollback. Their
// identity cannot be serialized or reconstructed from JSON.
func (a *metadataLogicalUnitsArena) sweepRegistriesLocked() error {
	rows, err := a.index.stage.db.Query(`SELECT id FROM logical_unit_registries WHERE refs=0`)
	if err != nil {
		return err
	}
	var zero []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		zero = append(zero, id)
		if len(zero) > 512 {
			rows.Close()
			return ErrMetadataWorkBudget
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, id := range zero {
		if _, err := a.index.stage.db.Exec(`DELETE FROM logical_unit_registries WHERE id=? AND refs=0`, id); err != nil {
			return err
		}
	}
	for id, registry := range a.registries {
		var live int
		err := a.index.stage.db.QueryRow(`SELECT 1 FROM logical_unit_registries WHERE id=?`, id).Scan(&live)
		if errors.Is(err, sql.ErrNoRows) {
			metadataLogicalDescriptorsUsed.Add(-registry.charge)
			delete(a.registries, id)
			for storage, imported := range a.imports {
				if imported == id {
					delete(a.imports, storage)
				}
			}
		} else if err != nil {
			return err
		}
	}
	return nil
}

func (a *metadataLogicalUnitsArena) closeLocked() error {
	if a.closed {
		return nil
	}
	a.closed = true
	var err error
	if a.index != nil {
		stage := a.index.stage
		if stage.db != nil {
			err = stage.db.Close()
			stage.db = nil
		}
		removed := true
		for _, name := range []string{stage.path + "-journal", stage.path} {
			if name == "" || name == "-journal" {
				continue
			}
			if removeErr := os.Remove(name); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, removeErr)
				removed = false
			}
		}
		if removed {
			metadataLogicalDiskUsed.Add(-stage.reserved)
			stage.reserved = 0
		}
	}
	for id, registry := range a.registries {
		metadataLogicalDescriptorsUsed.Add(-registry.charge)
		delete(a.registries, id)
	}
	a.imports = nil
	metadataLogicalDescriptorsUsed.Add(-metadataLogicalUnitArenaDescriptor)
	return err
}

type metadataLogicalFile interface {
	io.ReaderAt
	io.Writer
	io.WriterAt
	io.Closer
	Name() string
}

type metadataLogicalContextFile interface {
	ReadAtContext(context.Context, []byte, int64) (int, error)
}

func metadataLogicalReadAt(ctx context.Context, file io.ReaderAt, output []byte, offset int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if contextual, ok := file.(metadataLogicalContextFile); ok {
		return contextual.ReadAtContext(ctx, output, offset)
	}
	return file.ReadAt(output, offset)
}

type metadataLogicalContextReaderAt struct {
	ctx  context.Context
	file io.ReaderAt
}

func (r metadataLogicalContextReaderAt) ReadAt(p []byte, off int64) (int, error) {
	return metadataLogicalReadAt(r.ctx, r.file, p, off)
}
