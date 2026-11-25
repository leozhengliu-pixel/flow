package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"flow/api/internal/domain"
)

func scaleP21SW348Spool(t *testing.T, raw []byte) *RecordSpool {
	t.Helper()
	disk := metadataLogicalDiskUsed.Load()
	spool, err := newMetadataLogicalRecordSpool(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := spool.Close(); err != nil || metadataLogicalDiskUsed.Load() != disk {
			t.Error("binary spool storage was not released", err)
		}
	})
	if n, err := spool.Write(raw); err != nil || n != len(raw) {
		t.Fatal("write binary witness", n, err)
	}
	return spool
}

type scaleP21SW348CancelContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *scaleP21SW348CancelContext) Err() error {
	c.checks++
	if c.checks == 3 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestScaleP21SW348IntegrationBinarySpoolUTF8(t *testing.T) {
	repo := scaleP21HotLogoStore(t, nil)
	type witness struct {
		name string
		raw  []byte
	}
	valid := []witness{
		{"empty", nil},
		{"ascii", []byte(`{"body":"ordinary"}`)},
		{"replacement-rune", []byte("\ufffd")},
		{"native-nul", []byte{'a', 0, 'b'}},
		{"escaped-invalid-text", []byte(`{"body":"\u00ff"}`)},
	}
	for _, runeText := range []string{"\u00a2", "\u20ac", "\U0001f600"} {
		for split := 1; split < len(runeText); split++ {
			valid = append(valid, witness{
				fmt.Sprintf("rune-%d/split-%d", len(runeText), split),
				[]byte(strings.Repeat("x", metadataRecordChunkBytes-split) + runeText + strings.Repeat("y", metadataRecordChunkBytes+11)),
			})
		}
	}
	valid = append(valid, witness{
		"final-full-rune", []byte(strings.Repeat("x", metadataRecordChunkBytes-2) + "\U0001f600"),
	})
	invalid := []witness{
		{"raw-ff", []byte(`{"body":"` + "\xff" + `"}`)},
		{"continuation", []byte{0x80}},
		{"overlong", []byte{0xc0, 0xaf}},
		{"surrogate", []byte{0xed, 0xa0, 0x80}},
		{"above-unicode", []byte{0xf4, 0x90, 0x80, 0x80}},
		{"truncated-two", []byte{0xc2}},
		{"truncated-three", []byte{0xe2, 0x82}},
		{"truncated-four", []byte{0xf0, 0x9f, 0x98}},
		{"window-truncated", append(bytes.Repeat([]byte("x"), metadataRecordChunkBytes-1), 0xf0, 0x9f, 0x98)},
		{"later-window-ff", append(bytes.Repeat([]byte("x"), 2*metadataRecordChunkBytes+7), 0xff)},
	}
	native, err := json.Marshal(domain.IntegrationDelivery{
		ID: "raw-payload", Payload: json.RawMessage(`{"body":"` + "\xff" + `"}`),
	})
	if err != nil || utf8.Valid(native) || !bytes.Contains(native, []byte{0xff}) {
		t.Fatal("legacy native producer lost the invalid-byte witness", err)
	}
	invalid = append(invalid, witness{"native-raw-message", native})
	for _, event := range []string{"integration_delivery.claimed", "integration_delivery.retry"} {
		for _, group := range []struct {
			name    string
			values  []witness
			handled bool
		}{{"valid", valid, false}, {"invalid", invalid, true}} {
			for _, value := range group.values {
				t.Run(event+"/"+group.name+"/"+value.name, func(t *testing.T) {
					if utf8.Valid(value.raw) == group.handled {
						t.Fatal("incorrect UTF-8 witness")
					}
					spool := scaleP21SW348Spool(t, value.raw)
					owner := &ResourceRead{store: repo}
					defer owner.Close()
					beforeDisk, beforeWork := metadataLogicalDiskUsed.Load(), repo.MetadataWorkUsedForTesting()
					got, handled, err := integrationDeliveryBinarySpool(withIntegrationDeliveryStream(t.Context(), event), owner, spool)
					if err != nil || handled != group.handled || group.handled && !bytes.Equal(got, value.raw) || !group.handled && got != nil {
						t.Fatal("binary admission changed bytes or TEXT selection", handled, err)
					}
					wantWork := beforeWork
					if group.handled {
						wantWork += 4*len(value.raw) + (64 << 10)
					}
					if repo.MetadataWorkUsedForTesting() != wantWork || metadataLogicalDiskUsed.Load() != beforeDisk {
						t.Fatal("binary admission changed its owner bill or borrowed spool", repo.MetadataWorkUsedForTesting(), wantWork)
					}
					actual := make([]byte, len(value.raw))
					if n, err := spool.ReadAt(actual, 0); n != len(actual) || err != nil && !errors.Is(err, io.EOF) || !bytes.Equal(actual, value.raw) {
						t.Fatal("binary admission consumed or closed its borrowed spool", n, err)
					}
					owner.Close()
					if repo.MetadataWorkUsedForTesting() != beforeWork {
						t.Fatal("binary caller ownership was not released")
					}
				})
			}
		}
	}
	for _, event := range []string{"", "integration_delivery.other", "workflow.completed"} {
		t.Run("unselected/"+event, func(t *testing.T) {
			spool := scaleP21SW348Spool(t, []byte{0xff})
			owner := &ResourceRead{store: repo}
			defer owner.Close()
			got, handled, err := integrationDeliveryBinarySpool(withIntegrationDeliveryStream(t.Context(), event), owner, spool)
			if !errors.Is(err, ErrBoundedMetadataCoverage) || handled || got != nil || repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("binary compatibility path escaped integration claimed/retry", handled, err)
			}
		})
	}
}

func TestScaleP21SW348IntegrationBinarySpoolFailureOwnership(t *testing.T) {
	repo := scaleP21HotLogoStore(t, nil)
	t.Run("cancelled-during-window-scan", func(t *testing.T) {
		spool := scaleP21SW348Spool(t, bytes.Repeat([]byte("x"), 8*metadataRecordChunkBytes))
		bound, cancel := context.WithCancel(withIntegrationDeliveryStream(t.Context(), "integration_delivery.claimed"))
		defer cancel()
		ctx := &scaleP21SW348CancelContext{Context: bound, cancel: cancel}
		owner := &ResourceRead{store: repo}
		defer owner.Close()
		got, _, err := integrationDeliveryBinarySpool(ctx, owner, spool)
		if !errors.Is(err, context.Canceled) || got != nil || repo.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("UTF-8 windows ignored cancellation or admitted a result", err, ctx.checks)
		}
	})
	for _, invalid := range []bool{false, true} {
		for _, failure := range []string{"cancelled", "closed-owner", "short-spool"} {
			t.Run(fmt.Sprintf("%s/invalid-%t", failure, invalid), func(t *testing.T) {
				raw := bytes.Repeat([]byte("x"), 2*metadataRecordChunkBytes+11)
				if invalid {
					raw[len(raw)-1] = 0xff
				}
				spool := scaleP21SW348Spool(t, raw)
				ctx, cancel := context.WithCancel(withIntegrationDeliveryStream(t.Context(), "integration_delivery.retry"))
				defer cancel()
				owner := &ResourceRead{store: repo}
				defer owner.Close()
				var want error
				switch failure {
				case "cancelled":
					cancel()
					want = context.Canceled
				case "closed-owner":
					owner.Close()
					want = errMetadataLogicalClosed
				case "short-spool":
					if err := spool.file.Truncate(int64(len(raw) - 1)); err != nil {
						t.Fatal(err)
					}
				}
				disk := metadataLogicalDiskUsed.Load()
				got, _, err := integrationDeliveryBinarySpool(ctx, owner, spool)
				if err == nil || got != nil || want != nil && !errors.Is(err, want) ||
					want == nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatal("binary failure returned materialized bytes or lost its cause", failure, err)
				}
				if want != nil && repo.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("cancelled or closed owner admitted materialization")
				}
				owner.Close()
				if repo.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk {
					t.Fatal("binary failure leaked work or consumed its borrowed input")
				}
			})
		}
	}
}

func TestScaleP21SW348IntegrationBinarySpoolAdmissionBoundary(t *testing.T) {
	repo := scaleP21HotLogoStore(t, nil)
	raw := append(bytes.Repeat([]byte("x"), 2*metadataRecordChunkBytes+17), 0xff)
	spool := scaleP21SW348Spool(t, raw)
	cost := 4*len(raw) + (64 << 10)
	budget := repo.metadataResidency.work
	limit := budget.limit
	defer func() { budget.limit = limit }()
	for _, admitted := range []bool{false, true} {
		t.Run(fmt.Sprintf("admitted-%t", admitted), func(t *testing.T) {
			budget.limit = limit
			owner := &ResourceRead{store: repo}
			defer owner.Close()
			if err := owner.reserve(t.Context(), 4096); err != nil {
				t.Fatal(err)
			}
			base := repo.MetadataWorkUsedForTesting()
			budget.limit = base + cost
			if !admitted {
				budget.limit--
			}
			disk := metadataLogicalDiskUsed.Load()
			got, handled, err := integrationDeliveryBinarySpool(withIntegrationDeliveryStream(t.Context(), "integration_delivery.claimed"), owner, spool)
			if admitted {
				if err != nil || !handled || !bytes.Equal(got, raw) || repo.MetadataWorkUsedForTesting() != base+cost {
					t.Fatal("exact admission boundary refused or misbilled the binary value", handled, err)
				}
			} else if !errors.Is(err, ErrMetadataWorkBudget) || got != nil || repo.MetadataWorkUsedForTesting() != base {
				t.Fatal("pressure materialized an unadmitted binary value", handled, err)
			}
			owner.Close()
			if repo.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk {
				t.Fatal("binary admission boundary retained work or released the input")
			}
		})
	}
}
