package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestLegacyBootstrapQueueCancelsWithoutEnteringHandler(t *testing.T) {
	entered, release, finished := make(chan struct{}, 2), make(chan struct{}), make(chan struct{})
	handler := serializeLegacyBootstrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
	}))
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/bootstrap", nil))
		close(finished)
	}()
	<-entered
	defer func() { close(release); <-finished }()
	ctx, cancel := context.WithCancel(context.Background())
	secondDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/bootstrap", nil).WithContext(ctx))
		close(secondDone)
	}()
	cancel()
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("cancelled bootstrap remained queued")
	}
	select {
	case <-entered:
		t.Fatal("concurrent bootstrap entered the expensive handler")
	default:
	}
}

func TestStreamBootstrapPreservesJSONContract(t *testing.T) {
	for _, data := range []domain.Bootstrap{
		{},
		{Issues: []domain.Issue{}, Comments: map[string][]domain.Comment{}, Activities: map[string][]domain.ActivityEvent{}, Notifications: []domain.Notification{}, NotificationDeliveries: []domain.NotificationDelivery{}},
		{Issues: []domain.Issue{{ID: "issue", Title: "quoted \"<title>\""}}, Comments: map[string][]domain.Comment{"issue": {{ID: "comment", Body: "body\ntext"}}}, Activities: map[string][]domain.ActivityEvent{"issue": {{ID: "event", Type: "issue.created"}}}, Notifications: []domain.Notification{{ID: "notification"}}, NotificationDeliveries: []domain.NotificationDelivery{{ID: "delivery"}}},
	} {
		w := httptest.NewRecorder()
		writeBootstrapJSON(w, data)
		var got, want any
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("streaming changed the bootstrap JSON contract")
		}
	}
}

type failedBootstrapWriter struct{ writes int }

func (w *failedBootstrapWriter) Write(p []byte) (int, error) {
	w.writes++
	return 0, errors.New("disconnected")
}

func TestBootstrapArrayStopsAfterDisconnectedClient(t *testing.T) {
	w := &failedBootstrapWriter{}
	if err := encodeBootstrapArray(w, json.NewEncoder(w), make([]domain.Issue, 1000)); err == nil || w.writes != 1 {
		t.Fatalf("continued writing after disconnect: writes=%d err=%v", w.writes, err)
	}
}
