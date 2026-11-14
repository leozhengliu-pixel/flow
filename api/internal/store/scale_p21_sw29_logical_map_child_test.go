package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW29LogicalMapChildNativeKeyLargeSibling(t *testing.T) {
	for _, packed := range []bool{false, true} {
		t.Run(fmt.Sprint(packed), func(t *testing.T) {
			ctx := t.Context()
			key := "native\x00key"
			data := domain.Bootstrap{
				Workspace: domain.Workspace{ID: "map-child"},
				UserSettings: map[string]domain.UserSettings{
					key: {UserID: "different-body-user", Language: "zh-CN", CodingPromptTemplate: strings.Repeat("p", 3<<20)},
				},
			}
			for i := 0; i < 256; i++ {
				data.UserSettings[fmt.Sprint(i)] = domain.UserSettings{UserID: "other", Language: "en"}
			}
			source, err := captureMetadataLogicalSource(ctx, data, copyStamp{"map-child", -1})
			if err != nil {
				t.Fatal(err)
			}
			if packed {
				next, err := metadataLogicalUnitsImport(ctx, source)
				source.Release()
				if err != nil {
					t.Fatal(err)
				}
				source = next
			}
			defer source.Release()
			var field metadataLogicalNode
			if err := source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
				if name == "userSettings" {
					field = node
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			restore := func() {}
			var counter *sw29ProducerSQLDriver
			if packed {
				counter = &sw29ProducerSQLDriver{}
				driver := fmt.Sprintf("scale-p21-map-child-%d", sw29ProducerDriverSequence.Add(1))
				sql.Register(driver, counter)
				arena := source.units.arena
				counted, err := sql.Open(driver, arena.index.stage.path)
				if err != nil {
					t.Fatal(err)
				}
				counted.SetMaxOpenConns(1)
				original := arena.index.stage.db
				arena.index.stage.db = counted
				restored := false
				restore = func() {
					if !restored {
						restored = true
						arena.index.stage.db = original
						if err := counted.Close(); err != nil {
							t.Error(err)
						}
					}
				}
				defer restore()
			}
			user, found, err := field.MapChild(ctx, key)
			if err != nil || !found || user.typ != reflect.TypeFor[domain.UserSettings]() {
				t.Fatal("exact native map key did not resolve its typed node", packed, found, err)
			}
			if counter != nil {
				calls := counter.calls.Load()
				t.Logf("native-map-records=257 exact-key-local-sql=%d canonical-sql=0", calls)
				if calls == 0 || calls > 512 {
					t.Fatal("map child walked unrelated native map units", calls)
				}
			}
			if _, err := user.Value(ctx, 2<<20); !errors.Is(err, ErrMetadataWorkBudget) {
				t.Fatal("large-record control unexpectedly fit the old point decoder", err)
			}
			var language string
			if err := user.Children(ctx, func(name reflect.Value, child metadataLogicalNode) error {
				if name.String() != "Language" {
					return nil
				}
				return child.StreamBytes(ctx, func(reader io.Reader) error {
					value, err := io.ReadAll(io.LimitReader(reader, 8))
					language = string(value)
					return err
				})
			}); err != nil || language != "zh-CN" {
				t.Fatal("scalar child required its legal-large sibling", packed, language, err)
			}
			if _, found, err := field.MapChild(ctx, "different-body-user"); err != nil || found {
				t.Fatal("native map key was replaced by an inner UserID", packed, found, err)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, found, err := field.MapChild(cancelled, key); found || !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled indexed child lookup returned a node", found, err)
			}
			restore()
			source.Release()
			if _, found, err := field.MapChild(ctx, key); found || !errors.Is(err, errMetadataLogicalClosed) {
				t.Fatal("closed source revived its native map child", found, err)
			}
		})
	}
}
