package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestScaleP21SW228GenericJSONOwnedDifferential(t *testing.T) {
	for _, input := range []string{
		`null`, `true`, `42`, `"scalar"`, `[]`, `{}`,
		`{"id":"first","id":"last","n":9007199254740993,"negative":-0,"small":1e-7}`,
		`{"x":"<>&\u2028","surrogate":"\ud800","invalid":"` + string([]byte{255}) + `"}`,
		`[null,{"empty":[],"nil":null},2]`,
		strings.Repeat("[", 160) + `{"x":"<>&"}` + strings.Repeat("]", 160),
		``, `{"unterminated"`, `[1,]`, `true false`,
		`{"n":1e999,"later":true}`, `{"n":1e999,"n":1}`,
	} {
		t.Run(fmt.Sprintf("bytes-%d/%q", len(input), input[:min(len(input), 40)]), func(t *testing.T) {
			repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
			decode, err := repo.BeginMetadataJSONRead(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer decode.Close()
			raw := []byte(input)
			var expected any
			literalErr := json.Unmarshal(raw, &expected)
			value, valid, err := decode.DecodeGeneric(t.Context(), raw)
			if err != nil || valid != (literalErr == nil) {
				t.Fatal("generic acceptance changed", literalErr, err, valid)
			}
			if !valid {
				if decode.Err() != nil {
					t.Fatal("swallowed legacy JSON failure poisoned its owner", decode.Err())
				}
				value, valid, err = decode.DecodeGeneric(t.Context(), []byte(`{"recovered":true}`))
				if err != nil || !valid {
					t.Fatal("syntax fallback prevented a later valid decode", err)
				}
				value = nil
				decode.Close()
				if repo.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("failed partial decoder or recovery retained work")
				}
				return
			}
			for index := range raw {
				raw[index] = 'x'
			}
			if !reflect.DeepEqual(value, expected) {
				t.Fatal("generic graph changed values or borrowed its input")
			}
			output, err := repo.BeginMetadataJSONRead(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			encoded, valid, err := output.EncodeGeneric(t.Context(), value)
			want, literalErr := json.Marshal(expected)
			if literalErr != nil || err != nil || !valid || !bytes.Equal(encoded, want) {
				t.Fatal("generic output changed exact encoding/json bytes", literalErr, err)
			}
			value, expected = nil, nil
			decode.Close()
			if repo.MetadataWorkUsedForTesting() == 0 || !bytes.Equal(encoded, want) {
				t.Fatal("decoded owner release also released live encoded output")
			}
			encoded = nil
			output.Close()
			output.Close()
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("generic owners retained work after close")
			}
		})
	}
	t.Run("marshal-fallback-is-not-a-budget-error", func(t *testing.T) {
		repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
		read, err := repo.BeginMetadataJSONRead(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		raw, valid, err := read.EncodeGeneric(t.Context(), map[string]any{"bad": math.NaN()})
		if err != nil || valid || raw != nil || read.Err() != nil {
			t.Fatal("legacy marshal failure poisoned fallback", err, read.Err())
		}
		if err := read.ReserveEncoded(t.Context(), 3); err != nil {
			t.Fatal("fallback network bytes could not be admitted", err)
		}
		read.Close()
		if repo.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("marshal fallback leaked")
		}
	})
}

type scaleP21SW228CancelContext struct {
	context.Context
	cancel context.CancelFunc
	target string
}

func (ctx *scaleP21SW228CancelContext) Err() error {
	var callers [24]uintptr
	count := runtime.Callers(2, callers[:])
	for _, caller := range callers[:count] {
		if function := runtime.FuncForPC(caller); function != nil && strings.Contains(function.Name(), ctx.target) {
			ctx.cancel()
			break
		}
	}
	return ctx.Context.Err()
}

func TestScaleP21SW228GenericOwnershipCancellationAndBudgetRecovery(t *testing.T) {
	repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
	raw := json.RawMessage(`{"body":"` + strings.Repeat("<>&", 1<<20) + `"}`)
	for _, phase := range []string{"decode", "encode"} {
		t.Run(phase, func(t *testing.T) {
			read, err := repo.BeginMetadataJSONRead(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			if phase == "decode" {
				_, _, err = read.DecodeGeneric(cancelled, raw)
			} else {
				_, _, err = read.EncodeGeneric(cancelled, map[string]any{"body": strings.Repeat("<>&", 1<<20)})
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("generic operation lost cancellation", err)
			}
			read.Close()
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("cancelled generic operation retained work")
			}
		})
	}
	for _, phase := range []string{"decode", "encode"} {
		t.Run("cancel-during/"+phase, func(t *testing.T) {
			read, err := repo.BeginMetadataJSONRead(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			cancelled, cancel := context.WithCancel(t.Context())
			defer cancel()
			target := "boundedcrdt/internal/meter.jsonStringWidth"
			if phase == "encode" {
				target = "metadataLogicalByteReader).Read"
			}
			ctx := &scaleP21SW228CancelContext{Context: cancelled, cancel: cancel, target: target}
			if phase == "decode" {
				_, _, err = read.DecodeGeneric(ctx, raw)
			} else {
				_, _, err = read.EncodeGeneric(ctx, map[string]any{"body": strings.Repeat("<>&", 1<<20)})
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("in-flight generic cancellation changed its error", err)
			}
			read.Close()
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("in-flight generic cancellation retained work")
			}
		})
	}
	for _, phase := range []string{"decode", "encode"} {
		t.Run("denial-recovery/"+phase, func(t *testing.T) {
			read, err := repo.BeginMetadataJSONRead(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			repo.metadataResidency.work.limit = repo.MetadataWorkUsedForTesting() + (32 << 10)
			if phase == "decode" {
				_, _, err = read.DecodeGeneric(t.Context(), raw)
			} else {
				_, _, err = read.EncodeGeneric(t.Context(), map[string]any{"body": strings.Repeat("<>&", 1<<20)})
			}
			if !errors.Is(err, ErrMetadataWorkBudget) || !errors.Is(read.Err(), ErrMetadataWorkBudget) {
				t.Fatal("generic admission failure was swallowed as a legacy JSON fallback", err, read.Err())
			}
			read.Close()
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("partial generic allocation retained work after denial")
			}
			repo.metadataResidency.work.limit = metadataWorkBytes
			read, err = repo.BeginMetadataJSONRead(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if phase == "decode" {
				_, valid, err := read.DecodeGeneric(t.Context(), raw)
				if err != nil || !valid {
					t.Fatal("generic decode did not recover under an independent owner", err)
				}
			} else {
				_, valid, err := read.EncodeGeneric(t.Context(), map[string]any{"body": strings.Repeat("<>&", 1<<20)})
				if err != nil || !valid {
					t.Fatal("generic encoding did not recover under an independent owner", err)
				}
			}
			read.Close()
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("generic recovery retained work after close")
			}
		})
	}
}
