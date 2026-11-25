package main

import (
	"context"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21RequestReadClosers(t *testing.T) {
	for _, exit := range []string{"normal", "cancel", "panic"} {
		t.Run(exit, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			request, release := beginRequestResourceReads(httptest.NewRequest("GET", "/", nil).WithContext(ctx))
			order := []int{}
			if !registerRequestReadClose(request.Context(), func() { order = append(order, 1) }) ||
				!registerRequestReadClose(context.WithoutCancel(request.Context()), func() { order = append(order, 2) }) {
				t.Fatal("response owner was not retained")
			}
			nested, closeNested := beginRequestResourceReads(request)
			closeNested()
			if nested != request || len(order) != 0 {
				t.Fatal("nested scope released its parent's values")
			}
			func() {
				defer func() { _ = recover() }()
				defer release()
				if exit == "cancel" {
					cancel()
				}
				if len(order) != 0 {
					t.Fatal("owner released before response work finished")
				}
				if exit == "panic" {
					panic("encoding")
				}
			}()
			release()
			if !reflect.DeepEqual(order, []int{2, 1}) {
				t.Fatal("owners were not closed once in reverse order", order)
			}
			if registerRequestReadClose(request.Context(), func() { t.Error("closed scope retained another owner") }) {
				t.Fatal("closed scope accepted another owner")
			}
		})
	}
	if registerRequestReadClose(context.Background(), func() {}) {
		t.Fatal("unscoped read reported request ownership")
	}
}

func TestScaleP21RequestReadCloserRace(t *testing.T) {
	request, release := beginRequestResourceReads(httptest.NewRequest("GET", "/", nil))
	var calls atomic.Int64
	var workers sync.WaitGroup
	for range 100 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			close := func() { calls.Add(1) }
			if !registerRequestReadClose(request.Context(), close) {
				close()
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		release()
	}()
	workers.Wait()
	release()
	if calls.Load() != 100 {
		t.Fatal("racing scope close lost or repeated a release", calls.Load())
	}
}

func TestScaleP21RequestHandoffOwnership(t *testing.T) {
	for _, consumed := range []bool{false, true} {
		request, release := beginRequestResourceReads(httptest.NewRequest("GET", "/", nil))
		input := domain.Bootstrap{Projects: []domain.Project{{ID: "owned", Description: "response"}}}
		handOffPagedMetadata(request, "workspace", "viewer", input)
		handoff := request.Context().Value(pagedMetadataHandoffKey{}).(*pagedMetadataHandoff)
		if consumed {
			output, ok := takePagedMetadata(request, "workspace", "viewer")
			if !ok || !reflect.DeepEqual(output, input) {
				t.Fatal("taking a request handoff changed its values")
			}
		}
		release()
		if !reflect.DeepEqual(handoff.data, domain.Bootstrap{}) {
			t.Fatal("request completion retained handed-off values", consumed)
		}
		if _, ok := takePagedMetadata(request, "workspace", "viewer"); ok {
			t.Fatal("completed request served its old handoff")
		}
	}
	request := httptest.NewRequest("GET", "/", nil)
	handOffPagedMetadata(request, "workspace", "viewer", domain.Bootstrap{ViewerRole: "member"})
	output, ok := takePagedMetadata(request, "workspace", "viewer")
	if !ok || output.ViewerRole != "member" {
		t.Fatal("literal unscoped handoff changed")
	}
}
