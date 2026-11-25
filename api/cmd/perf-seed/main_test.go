package main

import (
	"errors"
	"math"
	"testing"

	"flow/api/internal/store"
)

func TestSeedContentBatchArgs(t *testing.T) {
	for _, test := range []struct {
		rows, columns, want int
	}{
		{0, 4, 0},
		{seedContentBatchSize, 4, seedContentBatchSize * 4},
		{seedContentBatchSize, 11, seedContentBatchSize * 11},
		{seedContentBatchSize + 1, 11, -1},
		{2, math.MaxInt, -1},
		{1, -1, -1},
	} {
		args, err := seedContentBatchArgs(make([][]any, test.rows), test.columns)
		if test.want < 0 {
			if !errors.Is(err, store.ErrTransactionWriteBudget) || args != nil {
				t.Fatalf("rows=%d columns=%d: got cap=%d err=%v, want write budget error", test.rows, test.columns, cap(args), err)
			}
		} else if err != nil || len(args) != 0 || cap(args) != test.want {
			t.Fatalf("rows=%d columns=%d: got cap=%d err=%v, want cap=%d", test.rows, test.columns, cap(args), err, test.want)
		}
	}
}
