package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMetadataWorkRefusalDistinguishesLiveDemand(t *testing.T) {
	repo := &SQLiteStore{metadataResidency: &metadataResidency{work: newMetadataWorkBudget(10)}}
	ctx, demand := withMetadataWorkDemand(t.Context())
	_, release, err := repo.tryAdmitAdditionalMetadataWork(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.tryAdmitAdditionalMetadataWork(ctx, 3); !errors.Is(err, ErrMetadataWorkBudget) || MetadataWorkContended(err) {
		t.Fatal("an attempt too big for the entire budget must stay a size refusal")
	}
	release()

	_, releaseOther, err := repo.tryAdmitAdditionalMetadataWork(t.Context(), 8)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseOther()
	if _, _, err := repo.tryAdmitAdditionalMetadataWork(ctx, 3); !MetadataWorkContended(err) {
		t.Fatal("released work must not make a later refusal look permanent")
	}
	releaseOther()
	if !repo.awaitMetadataWork(ctx, demand, time.Now().Add(time.Second)) {
		t.Fatal("the attempt fits again after other work releases")
	}
}

func TestMetadataWorkFullQueueRefusesOnlyFittingWorkAsContention(t *testing.T) {
	budget := newMetadataWorkBudget(10)
	budget.waiting = 128
	if _, _, err := budget.acquire(t.Context(), 1); !MetadataWorkContended(err) {
		t.Fatal("a full wait queue must refuse fitting work as contention")
	}
	if _, _, err := budget.acquire(t.Context(), 11); !errors.Is(err, ErrMetadataWorkBudget) || MetadataWorkContended(err) {
		t.Fatal("a full wait queue must preserve size refusals")
	}
}

func TestMetadataWorkDeadlineWhileWaitingIsContention(t *testing.T) {
	budget := newMetadataWorkBudget(10)
	_, release, err := budget.acquire(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := budget.acquire(ctx, 1); !errors.Is(err, context.DeadlineExceeded) || !MetadataWorkContended(err) {
		t.Fatal("the request deadline expired while fitting work waited for admission")
	}
	if _, _, err := budget.acquire(ctx, 1); !errors.Is(err, context.DeadlineExceeded) || MetadataWorkContended(err) {
		t.Fatal("a deadline before admission must keep the original error")
	}
}
