package store

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"flow/api/internal/domain"
)

type advancedScheduleState struct {
	now       time.Time
	retention time.Duration
	source    *metadataLogicalSource
	owner     *ResourceRead
	before    domain.Bootstrap
	first     *RecordSpool
	calls     int
	err       error
}

func advancedScheduleFingerprintJSONError(err error) bool {
	var marshaler *json.MarshalerError
	var value *json.UnsupportedValueError
	var kind *json.UnsupportedTypeError
	var syntax *json.SyntaxError
	var codec *metadataAggregateCodecError
	return errors.As(err, &marshaler) || errors.As(err, &value) ||
		errors.As(err, &kind) || errors.As(err, &syntax) || errors.As(err, &codec) ||
		errors.Is(err, errMetadataCollectionJSON)
}

func advancedScheduleFingerprintNode(ctx context.Context, owner *ResourceRead, node metadataLogicalNode, output io.Writer) (err error) {
	stream := &logicalJSONStream{
		ctx: ctx, owner: owner, view: &WebhookAggregateRead{store: owner.store},
		out: bufio.NewWriterSize(output, 64<<10), classifyCodecErrors: true,
	}
	defer func() {
		for _, release := range stream.view.releases {
			release()
		}
		if stream.stage != nil {
			err = errors.Join(err, stream.stage.Close())
		}
	}()
	if err := stream.write(webhookJSONValue{node: node}, 0); err != nil {
		return err
	}
	return stream.out.Flush()
}

// AdvancedScheduleFingerprint serves only the ticker's two-call equality
// check. It compares exact complete encoded bytes in disk spools; returned
// tokens carry equality, never a lossy hash. Literal Marshal failures remain
// nil. Storage, admission and cancellation errors abort the enclosing write.
func AdvancedScheduleFingerprint(ctx context.Context, data domain.Bootstrap) []byte {
	state := advancedScheduleMaintenance(ctx)
	if state == nil || state.owner == nil || state.source == nil || state.calls >= 2 {
		if state != nil {
			state.err = ErrBoundedMetadataCoverage
		}
		return nil
	}
	state.calls++
	image, err := (advancedScheduleImageProfile{}).image(ctx, state.owner, state.source, state.before, data)
	if err != nil {
		state.err = err
		return nil
	}
	defer image.Release()
	content, err := boundedDiscussionEventSource(ctx, image, data)
	if err != nil {
		state.err = err
		return nil
	}
	defer content.Release()
	spool, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		state.err = err
		return nil
	}
	retained := false
	defer func() {
		if !retained {
			_ = spool.Close()
		}
	}()
	write := func(part string) error {
		_, err := io.WriteString(spool, part)
		return err
	}
	err = write("[")
	for ordinal, name := range []string{"projects", "projectUpdates", "notifications", "notificationDeliveries", "trash", "issueSlas", "slaEvents"} {
		if err != nil {
			break
		}
		if ordinal != 0 {
			err = write(",")
		}
		if err == nil {
			index, _, _ := metadataLogicalField(name)
			err = advancedScheduleFingerprintNode(ctx, state.owner, content.fields[index], spool)
		}
	}
	if err == nil {
		err = write("]")
	}
	if err != nil {
		if !advancedScheduleFingerprintJSONError(err) {
			state.err = err
		}
		return nil
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		state.err = err
		return nil
	}
	if state.calls == 1 {
		state.first, retained = spool, true
		return []byte{1}
	}
	if state.first == nil {
		return []byte{1}
	}
	equal, err := legacyProjectEqualSpools(ctx, state.first, spool)
	if err != nil {
		state.err = err
		return nil
	}
	if equal {
		return []byte{1}
	}
	return []byte{2}
}
