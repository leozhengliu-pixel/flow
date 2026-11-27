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
	metadataRecordWorkLimitReason      = "record_work_limit"
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

// Work of one record.
//
// Normalizing a logical source materializes one record at a time: the
// decoded value, its normalized copy (normalize and display references run
// on a one-record Bootstrap) and the frame encoder's buffers live together
// with the dependency records it looks up. The normalizer reserves eight
// times the record's logical decode cost (node.cost, itself about twice the
// in-memory bytes of its strings and slices) plus 64 KiB for that, on top of
// the base work it holds for the whole source, before it decodes the record.
// The reservation goes through the process work budget like every other
// admitted operation, so peak memory stays within that budget.
//
// The first bounded release also compared the cost with a fixed eighth of
// metadataWorkBytes() (32 MiB) before it asked the budget, and capped the
// operation at metadataWorkBytes(), so a document whose decode cost passed
// 32 MiB (#264) was refused whatever FLOW_METADATA_WORK_BYTES allowed and the
// replica fell back to legacy reads. A record may now use as much work as
// the configured process work budget holds; a record that cannot fit it is
// refused with metadataRecordWorkError.

// metadataRecordWorkLimit is the most work one record's operation may
// reserve: the process work budget of the store's residency. Without a
// residency (preparation before bounded admission) nothing is reserved and
// an operation keeps at least the metadataWorkBytes() it always had.
func (s *SQLiteStore) metadataRecordWorkLimit() int {
	if s != nil && s.metadataResidency != nil && s.metadataResidency.work != nil {
		return s.metadataResidency.work.limit
	}
	return max(metadataWorkBytes(), metadataWorkBudgetBytes())
}

// metadataRecordWorkError is a record refused because the work it needs is
// over the per-record limit, before any of it is reserved. It is a metadata
// work budget refusal (errors.Is ErrMetadataWorkBudget) and keeps that
// error's text; the startup diagnostic reads its sizes. It is distinct from
// a record that fits the limit but finds the process budget in use by other
// work (metadata_work_budget, work contention) and from spool exhaustion.
type metadataRecordWorkError struct {
	// collection is the static Bootstrap field name of the record.
	collection string
	// cost is the record's logical decode cost and costLimit the largest
	// cost the limit admits beside the operation's base work.
	cost, costLimit uint64
	// work is what the record needs (base work included) and limit the
	// per-record work limit (the process work budget).
	work, limit uint64
}

func (e *metadataRecordWorkError) Error() string { return ErrMetadataWorkBudget.Error() }

func (e *metadataRecordWorkError) Is(target error) bool { return target == ErrMetadataWorkBudget }

// checkMetadataRecordWork refuses a record of decode cost cost whose work
// (cost*factor+overhead beside base) does not fit limit.
func checkMetadataRecordWork(collection string, cost uint64, factor, overhead, base, limit int) error {
	room := uint64(max(limit-base-overhead, 0))
	costLimit := room / uint64(factor)
	if cost <= costLimit {
		return nil
	}
	work := uint64(1<<64 - 1)
	if cost <= (work-uint64(base)-uint64(overhead))/uint64(factor) {
		work = cost*uint64(factor) + uint64(base) + uint64(overhead)
	}
	return &metadataRecordWorkError{collection: collection, cost: cost, costLimit: costLimit, work: work, limit: uint64(max(limit, 0))}
}

// boundedAdmissionFailure logs why bounded admission or startup preparation
// failed and what an operator can change. It logs a reason from a fixed set,
// static collection names and server capacities, never the error text
// (which can carry database driver messages).
func boundedAdmissionFailure(message string, err error) {
	attrs := []any{"reason", boundedAdmissionReason(err)}
	collection := ""
	var field *metadataLogicalFieldError
	if errors.As(err, &field) {
		collection = field.field
	}
	var record *metadataRecordWorkError
	if errors.As(err, &record) && record.collection != "" {
		// The refused record's own collection, also when a field
		// recovery around it is reported.
		collection = record.collection
	}
	if collection != "" {
		attrs = append(attrs, "collection", collection)
	}
	var capacity *metadataLogicalCapacityError
	if errors.As(err, &capacity) {
		attrs = append(attrs,
			"file_capacity_bytes", capacity.fileLimit, "file_offset_bytes", capacity.offset, "write_bytes", capacity.write,
			"spool_disk_budget_bytes", capacity.processLimit, "spool_disk_used_bytes", capacity.processUsed)
	} else if record != nil {
		attrs = append(attrs,
			"record_cost_bytes", record.cost, "record_cost_limit_bytes", record.costLimit,
			"record_work_bytes", record.work, "record_work_limit_bytes", record.limit)
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
	var record *metadataRecordWorkError
	switch {
	case err == nil:
		return "not_ready"
	case errors.As(err, &capacity):
		return capacity.kind
	case errors.As(err, &record):
		return metadataRecordWorkLimitReason
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
	var record *metadataRecordWorkError
	switch {
	case errors.As(err, &record):
		return "one record needs more work than the per-record limit, which is FLOW_METADATA_WORK_BYTES (Helm config.env; defaults to a quarter of FLOW_METADATA_MEMORY_BYTES): raise it to at least record_work_bytes; until then bounded admission keeps falling back to legacy reads"
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
