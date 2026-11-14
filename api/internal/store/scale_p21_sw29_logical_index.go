package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
)

const sw29LogicalIndexDepth = 128
const sw29LogicalIndexKeyBytes = 2048
const sw29LogicalIndexValueBytes = 512

type sw29LogicalIndex struct {
	stage         *sw26MigrationStage
	seed          [32]byte
	reads, writes uint64
	tx            *sql.Tx
	exec          func(context.Context, int64, string, ...any) (sql.Result, error)
	unit          func([]byte) uint64
	otherUnit     func([]byte) uint64
}

type sw29LogicalIndexNode struct {
	id, left, right uint64
	key, value      []byte
	priority        [32]byte
}

func sw29NewLogicalIndex(ctx context.Context) (*sw29LogicalIndex, error) {
	stage, err := sw26NewMigrationStage(ctx)
	if err != nil {
		return nil, err
	}
	index := &sw29LogicalIndex{stage: stage}
	if _, err := rand.Read(index.seed[:]); err != nil {
		stage.Close()
		return nil, err
	}
	if _, err := stage.exec(ctx, 4096, `CREATE TABLE logical_nodes(id INTEGER PRIMARY KEY,key BLOB NOT NULL,value BLOB NOT NULL,lft INTEGER NOT NULL,rgt INTEGER NOT NULL,priority BLOB NOT NULL)`); err != nil {
		stage.Close()
		return nil, err
	}
	return index, nil
}

func (index *sw29LogicalIndex) Close() error { return index.stage.Close() }

func (index *sw29LogicalIndex) priority(key []byte) [32]byte {
	h := sha256.New()
	h.Write(index.seed[:])
	h.Write(key)
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

func (index *sw29LogicalIndex) read(ctx context.Context, id uint64) (sw29LogicalIndexNode, error) {
	var node sw29LogicalIndexNode
	if id == 0 {
		return node, nil
	}
	if err := ctx.Err(); err != nil {
		return node, err
	}
	var priority []byte
	index.reads++
	var row *sql.Row
	if index.tx != nil {
		row = index.tx.QueryRowContext(ctx, `SELECT key,value,lft,rgt,priority FROM logical_nodes WHERE id=?`, id)
	} else {
		row = index.stage.db.QueryRowContext(ctx, `SELECT key,value,lft,rgt,priority FROM logical_nodes WHERE id=?`, id)
	}
	err := row.Scan(&node.key, &node.value, &node.left, &node.right, &priority)
	if err != nil {
		return node, err
	}
	if len(node.key) > sw29LogicalIndexKeyBytes || len(node.value) > sw29LogicalIndexValueBytes || len(priority) != len(node.priority) {
		return node, ErrMetadataWorkBudget
	}
	node.id = id
	copy(node.priority[:], priority)
	return node, nil
}

func (index *sw29LogicalIndex) write(ctx context.Context, node sw29LogicalIndexNode) (uint64, error) {
	if len(node.key) > sw29LogicalIndexKeyBytes || len(node.value) > sw29LogicalIndexValueBytes {
		return 0, ErrMetadataWorkBudget
	}
	exec := index.stage.exec
	query := `INSERT INTO logical_nodes(key,value,lft,rgt,priority) VALUES(?,?,?,?,?)`
	args := []any{node.key, node.value, node.left, node.right, node.priority[:]}
	if index.exec != nil {
		exec = index.exec
	}
	if index.unit != nil {
		query = `INSERT INTO logical_nodes(key,value,lft,rgt,priority,unit,otherunit) VALUES(?,?,?,?,?,?,?)`
		args = append(args, index.unit(node.value))
		other := uint64(0)
		if index.otherUnit != nil {
			other = index.otherUnit(node.value)
		}
		args = append(args, other)
	}
	result, err := exec(ctx, int64(len(node.key)+len(node.value)+128), query, args...)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err == nil {
		index.writes++
	}
	return uint64(id), err
}

func (index *sw29LogicalIndex) Put(ctx context.Context, root uint64, key, value []byte) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(key) > sw29LogicalIndexKeyBytes || len(value) > sw29LogicalIndexValueBytes {
		return 0, ErrMetadataWorkBudget
	}
	return index.put(ctx, root, key, value, index.priority(key), 0)
}

func (index *sw29LogicalIndex) put(ctx context.Context, root uint64, key, value []byte, priority [32]byte, depth int) (uint64, error) {
	if depth >= sw29LogicalIndexDepth {
		return 0, ErrMetadataWorkBudget
	}
	if root == 0 {
		return index.write(ctx, sw29LogicalIndexNode{key: key, value: value, priority: priority})
	}
	node, err := index.read(ctx, root)
	if err != nil {
		return 0, err
	}
	order := bytes.Compare(key, node.key)
	if order == 0 {
		if bytes.Equal(value, node.value) {
			return root, nil
		}
		node.value = value
		return index.write(ctx, node)
	}
	if order < 0 {
		node.left, err = index.put(ctx, node.left, key, value, priority, depth+1)
		if err != nil {
			return 0, err
		}
		left, err := index.read(ctx, node.left)
		if err != nil {
			return 0, err
		}
		if bytes.Compare(left.priority[:], node.priority[:]) < 0 {
			node.left = left.right
			left.right, err = index.write(ctx, node)
			if err != nil {
				return 0, err
			}
			return index.write(ctx, left)
		}
	} else {
		node.right, err = index.put(ctx, node.right, key, value, priority, depth+1)
		if err != nil {
			return 0, err
		}
		right, err := index.read(ctx, node.right)
		if err != nil {
			return 0, err
		}
		if bytes.Compare(right.priority[:], node.priority[:]) < 0 {
			node.right = right.left
			right.left, err = index.write(ctx, node)
			if err != nil {
				return 0, err
			}
			return index.write(ctx, right)
		}
	}
	return index.write(ctx, node)
}

func (index *sw29LogicalIndex) Delete(ctx context.Context, root uint64, key []byte) (uint64, error) {
	return index.remove(ctx, root, key, 0)
}

func (index *sw29LogicalIndex) remove(ctx context.Context, root uint64, key []byte, depth int) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if depth >= sw29LogicalIndexDepth {
		return 0, ErrMetadataWorkBudget
	}
	if root == 0 {
		return 0, nil
	}
	node, err := index.read(ctx, root)
	if err != nil {
		return 0, err
	}
	order := bytes.Compare(key, node.key)
	if order == 0 {
		return index.merge(ctx, node.left, node.right, depth+1)
	}
	before := node.left
	if order < 0 {
		node.left, err = index.remove(ctx, node.left, key, depth+1)
		if err != nil {
			return 0, err
		}
		if before == node.left {
			return root, nil
		}
	} else {
		before = node.right
		node.right, err = index.remove(ctx, node.right, key, depth+1)
		if err != nil {
			return 0, err
		}
		if before == node.right {
			return root, nil
		}
	}
	return index.write(ctx, node)
}

func (index *sw29LogicalIndex) merge(ctx context.Context, left, right uint64, depth int) (uint64, error) {
	if depth >= sw29LogicalIndexDepth {
		return 0, ErrMetadataWorkBudget
	}
	if left == 0 {
		return right, nil
	}
	if right == 0 {
		return left, nil
	}
	a, err := index.read(ctx, left)
	if err != nil {
		return 0, err
	}
	b, err := index.read(ctx, right)
	if err != nil {
		return 0, err
	}
	if bytes.Compare(a.priority[:], b.priority[:]) < 0 {
		a.right, err = index.merge(ctx, a.right, right, depth+1)
		if err != nil {
			return 0, err
		}
		return index.write(ctx, a)
	}
	b.left, err = index.merge(ctx, left, b.left, depth+1)
	if err != nil {
		return 0, err
	}
	return index.write(ctx, b)
}

func (index *sw29LogicalIndex) Get(ctx context.Context, root uint64, key []byte) ([]byte, bool, error) {
	for depth := 0; root != 0; depth++ {
		if depth >= sw29LogicalIndexDepth {
			return nil, false, ErrMetadataWorkBudget
		}
		node, err := index.read(ctx, root)
		if err != nil {
			return nil, false, err
		}
		order := bytes.Compare(key, node.key)
		if order == 0 {
			return node.value, true, nil
		}
		if order < 0 {
			root = node.left
		} else {
			root = node.right
		}
	}
	return nil, false, ctx.Err()
}

var sw29LogicalIndexStop = errors.New("logical index visitor stopped")

func (index *sw29LogicalIndex) Range(ctx context.Context, root uint64, lo, hi []byte, visit func([]byte, []byte) error) error {
	var walk func(uint64, int) error
	walk = func(id uint64, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth >= sw29LogicalIndexDepth {
			return ErrMetadataWorkBudget
		}
		if id == 0 {
			return nil
		}
		node, err := index.read(ctx, id)
		if err != nil {
			return err
		}
		before := lo != nil && bytes.Compare(node.key, lo) < 0
		after := hi != nil && bytes.Compare(node.key, hi) >= 0
		if !before {
			if err := walk(node.left, depth+1); err != nil {
				return err
			}
		}
		if !before && !after {
			if err := visit(node.key, node.value); err != nil {
				return err
			}
		}
		if !after {
			return walk(node.right, depth+1)
		}
		return nil
	}
	err := walk(root, 0)
	if errors.Is(err, sw29LogicalIndexStop) {
		return nil
	}
	return err
}
