package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestScaleP21SW0SnapshotAdditionalAdmission(t *testing.T) {
	s := &SQLiteStore{metadataResidency: newMetadataResidency()}
	s.metadataResidency.work = newMetadataWorkBudget(10)
	budget := s.metadataResidency.work
	parent, releaseParent, err := s.admitMetadataWork(t.Context(), 3)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseParent()
	child, releaseChild, err := s.tryAdmitAdditionalMetadataWork(parent, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseChild()
	parentLease := parent.Value(metadataWorkLeaseKey{}).(*metadataWorkLease)
	childLease := child.Value(metadataWorkLeaseKey{}).(*metadataWorkLease)
	if parentLease == childLease || parentLease.bytes != 3 || childLease.bytes != 4 || budget.used != 7 {
		t.Fatal("child allowance reused or enlarged the parent")
	}
	_, nestedRelease, err := s.admitMetadataWork(child, 4)
	if err != nil {
		t.Fatal(err)
	}
	nestedRelease()
	if budget.used != 7 {
		t.Fatal("nested child admission double charged work")
	}
	if _, _, err := s.admitMetadataWork(child, 5); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("nested operation escaped its child allowance", err)
	}
	deadline, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	if _, _, err := s.tryAdmitAdditionalMetadataWork(deadline, 4); !errors.Is(err, ErrMetadataWorkBudget) || deadline.Err() != nil {
		t.Fatal("snapshot admission queued instead of failing before its deadline", err)
	}
	if budget.used != 7 || budget.waiting != 0 || !parentLease.live {
		t.Fatal("failed admission changed an existing reservation")
	}
	releaseParent()
	if budget.used != 4 || !childLease.live {
		t.Fatal("child lifetime depended on its parent release")
	}
	releaseChild()
	releaseChild()
	if budget.used != 0 || childLease.live {
		t.Fatal("child release leaked or double released its reservation")
	}
	if _, _, err := s.tryAdmitAdditionalMetadataWork(child, 1); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("released child context remained usable", err)
	}
	canceled, cancelNow := context.WithCancel(t.Context())
	cancelNow()
	if _, _, err := s.tryAdmitAdditionalMetadataWork(canceled, 1); !errors.Is(err, context.Canceled) || budget.used != 0 {
		t.Fatal("cancellation changed admission", err)
	}
	for _, invalid := range []int{-1, 0, 11} {
		if _, _, err := s.tryAdmitAdditionalMetadataWork(t.Context(), invalid); !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatal("invalid reservation admitted", invalid, err)
		}
	}
	plain := &SQLiteStore{}
	if _, release, err := plain.tryAdmitAdditionalMetadataWork(t.Context(), 1); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
}

func TestScaleP21SW0SnapshotAdditionalAdmissionConcurrent(t *testing.T) {
	s := &SQLiteStore{metadataResidency: newMetadataResidency()}
	s.metadataResidency.work = newMetadataWorkBudget(10)
	budget := s.metadataResidency.work
	parent, releaseParent, err := s.admitMetadataWork(t.Context(), 3)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseParent()
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() {
			for range 20 {
				child, release, err := s.tryAdmitAdditionalMetadataWork(parent, 2)
				if errors.Is(err, ErrMetadataWorkBudget) {
					continue
				}
				if err != nil {
					t.Error(err)
					return
				}
				budget.mu.Lock()
				if budget.used > budget.limit || budget.used < 5 || budget.waiting != 0 {
					t.Error("concurrent admission exceeded its charge", budget.used)
				}
				budget.mu.Unlock()
				if _, nestedRelease, err := s.admitMetadataWork(child, 2); err != nil {
					t.Error(err)
				} else {
					nestedRelease()
				}
				release()
				release()
			}
		})
	}
	group.Wait()
	if budget.used != 3 || budget.waiting != 0 {
		t.Fatal("concurrent reservations leaked", budget.used)
	}
	releaseParent()
	if budget.used != 0 {
		t.Fatal("parent reservation leaked")
	}
}
