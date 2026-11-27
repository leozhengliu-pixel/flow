package store

import (
	"context"
	"errors"
	"log/slog"
)

// Capacity of a logical file.
//
// A logical source spools a workspace's typed frames to one temporary file
// per storage. Two separate bounds apply to it:
//
//   - Disk: every byte written is charged to the process spool disk budget
//     (metadataLogicalDiskCap, FLOW_METADATA_SOURCE_DISK_BYTES, which
//     defaults to the metadata memory target) before it is appended. One
//     file may use as much of that budget as is free; there is no smaller
//     per-file share.
//   - Addressing: offsets, sizes and the 16-byte frame header (uint64 size,
//     uint64 cost with three flag bits) are 64-bit, and every memo, child
//     index, copy and borrow record keeps int64 offsets. A file is capped at
//     metadataLogicalFileAddressBytes (256 TiB) so offset+size sums and the
//     61-bit cost of a frame spanning the whole file (a small multiple of
//     its bytes) stay far from overflow. A file's offsets include borrowed
//     ranges, which take no disk bytes.
//
// The first bounded release used one constant for both: its fixed 1 GiB
// process spool budget was also checked per file, and stayed a fixed
// per-file limit after the process budget became configurable. A workspace
// whose typed frames passed 1 GiB (deep project progress histories, #262)
// was refused admission whatever the configured budget was.
//
// metadataLogicalFrameBytes (1 GiB) remains the bound on a single row or
// record frame that readers of SQL rows and spools accept.

const (
	metadataLogicalCapacityFile        = "logical_file_capacity"
	metadataLogicalCapacityProcessDisk = "process_spool_disk_budget"
)

// metadataLogicalCapacityError is a refused logical spool write. It is a
// metadata work budget refusal (errors.Is ErrMetadataWorkBudget) and keeps
// that error's text: clients see the same error as before. The startup
// diagnostic reads its capacities.
type metadataLogicalCapacityError struct {
	kind string
	// fileLimit is the per-file capacity, offset the file's length and write
	// the size of the refused write. processLimit and processUsed are the
	// process spool disk budget and its use when the write was refused.
	fileLimit, offset, write, processLimit, processUsed int64
}

func (e *metadataLogicalCapacityError) Error() string { return ErrMetadataWorkBudget.Error() }

func (e *metadataLogicalCapacityError) Is(target error) bool { return target == ErrMetadataWorkBudget }

func (b *metadataLogicalBuilder) capacityError(kind string, write int64) error {
	return &metadataLogicalCapacityError{kind: kind, fileLimit: b.fileLimit, offset: b.offset, write: write,
		processLimit: b.diskLimit, processUsed: metadataLogicalDiskUsed.Load()}
}

// checkFileCapacity refuses a write of size bytes that would take the file
// past its addressable capacity.
func (b *metadataLogicalBuilder) checkFileCapacity(size int64) error {
	if b.offset < 0 || size < 0 || size > b.fileLimit-b.offset {
		return b.capacityError(metadataLogicalCapacityFile, size)
	}
	return nil
}

// metadataLogicalFieldError is a failed recovery of one Bootstrap field; the
// field is a static JSON field name.
type metadataLogicalFieldError struct {
	field string
	err   error
}

func (e *metadataLogicalFieldError) Error() string {
	return "recover logical " + e.field + ": " + e.err.Error()
}

func (e *metadataLogicalFieldError) Unwrap() error { return e.err }

// boundedAdmissionFailure logs why bounded admission or startup preparation
// failed and what an operator can change. It logs a reason from a fixed set,
// static collection names and server capacities, never the error text
// (which can carry database driver messages).
func boundedAdmissionFailure(message string, err error) {
	attrs := []any{"reason", boundedAdmissionReason(err)}
	var field *metadataLogicalFieldError
	if errors.As(err, &field) {
		attrs = append(attrs, "collection", field.field)
	}
	var capacity *metadataLogicalCapacityError
	if errors.As(err, &capacity) {
		attrs = append(attrs,
			"file_capacity_bytes", capacity.fileLimit, "file_offset_bytes", capacity.offset, "write_bytes", capacity.write,
			"spool_disk_budget_bytes", capacity.processLimit, "spool_disk_used_bytes", capacity.processUsed)
	} else if errors.Is(err, ErrMetadataWorkBudget) {
		attrs = append(attrs, "work_budget_bytes", metadataWorkBudgetBytes(), "spool_disk_budget_bytes", metadataLogicalDiskCap())
	}
	if action := boundedAdmissionAction(err); action != "" {
		attrs = append(attrs, "action", action)
	}
	slog.Warn(message, attrs...)
}

func boundedAdmissionReason(err error) string {
	var capacity *metadataLogicalCapacityError
	switch {
	case err == nil:
		return "not_ready"
	case errors.As(err, &capacity):
		return capacity.kind
	case errors.Is(err, ErrRecordSpoolContended):
		return "spool_disk_contended"
	case errors.Is(err, ErrRecordSpoolBudget):
		return "record_spool_budget"
	case errors.Is(err, ErrMetadataWorkBudget):
		return "metadata_work_budget"
	case errors.Is(err, ErrBoundedMetadataCoverage):
		return "coverage_incomplete"
	case errors.Is(err, ErrRecordJournalUnavailable):
		return "record_journal_unavailable"
	case errors.Is(err, ErrRecordJournalCoverage):
		return "record_journal_coverage"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	default:
		return "error"
	}
}

func boundedAdmissionAction(err error) string {
	var capacity *metadataLogicalCapacityError
	switch {
	case errors.As(err, &capacity) && capacity.kind == metadataLogicalCapacityFile:
		return "the workspace's logical source exceeds the hard per-file capacity of the spool encoding; it keeps legacy reads"
	case errors.As(err, &capacity):
		return "raise FLOW_METADATA_SOURCE_DISK_BYTES (Helm config.env; defaults to FLOW_METADATA_MEMORY_BYTES) above the workspaces' spooled sources and check free space in the metadata spool directory"
	case errors.Is(err, ErrRecordSpoolBudget), errors.Is(err, ErrRecordSpoolContended):
		return "raise FLOW_METADATA_SOURCE_DISK_BYTES (Helm config.env) and check free space in the metadata spool directory"
	case errors.Is(err, ErrMetadataWorkBudget):
		return "raise FLOW_METADATA_MEMORY_BYTES or FLOW_METADATA_WORK_BYTES (Helm config.env)"
	case err == nil:
		return "see the startup metadata preparation log line for incomplete collections and uncovered workspaces"
	default:
		return ""
	}
}
