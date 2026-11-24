package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type blockedProjectionWriter struct {
	*httptest.ResponseRecorder
	started chan struct{}
	resume  chan struct{}
}

func (w *blockedProjectionWriter) Write(p []byte) (int, error) {
	select {
	case <-w.started:
	default:
		close(w.started)
		<-w.resume
	}
	return w.ResponseRecorder.Write(p)
}

func TestLegacyBootstrapAndResourceShareProjectionBudget(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "off")
	t.Setenv("FLOW_RECORD_ACCESS", "off")
	repo := pulseTestRepo(t)
	workspace := repo.Bootstrap().Workspace.URLKey
	_, token, err := repo.Login(t.Context(), "admin@example.test", "test-password")
	if err != nil {
		t.Fatal("could not create fixture session")
	}
	resource := newMetadataReadGate(6, 6, 200*time.Millisecond, 0)
	bootstrap := newMetadataReadGate(4, 4, 200*time.Millisecond, 0)
	budget := newLegacyProjectionBudget(func(*http.Request) int64 { return 8 }, 10)
	resource.projectionBudget = budget
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), projectionGate: resource, metadataGate: bootstrap})
	request := func(ctx context.Context, path string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
		r.Header.Set("X-Workspace-Key", workspace)
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		return r
	}

	const bootstrapPath, resourcePath = "/api/issue-records/bootstrap", "/api/initiatives"
	for _, pair := range []struct {
		name, first, second string
		holder, waiter      *metadataReadGate
	}{
		{"resource-blocks-bootstrap", resourcePath, bootstrapPath, resource, bootstrap},
		{"bootstrap-blocks-resource", bootstrapPath, resourcePath, bootstrap, resource},
	} {
		t.Run(pair.name, func(t *testing.T) {
			w := &blockedProjectionWriter{httptest.NewRecorder(), make(chan struct{}), make(chan struct{})}
			done := make(chan struct{})
			go func() {
				defer close(done)
				handler.ServeHTTP(w, request(t.Context(), pair.first))
			}()
			defer func() {
				close(w.resume)
				<-done
			}()
			select {
			case <-w.started:
			case <-done:
				t.Fatalf("first request completed before encoding a projection, HTTP %d", w.Code)
			case <-time.After(5 * time.Second):
				t.Fatal("first request did not reach the response")
			}
			if len(pair.holder.slots) != 1 || len(pair.waiter.slots) != 0 || pair.holder.active.Load() != 1 {
				t.Fatal("the count gates should have capacity while the shared budget is occupied")
			}
			before := pair.waiter.served.Load()
			refused := httptest.NewRecorder()
			handler.ServeHTTP(refused, request(t.Context(), pair.second))
			if refused.Code != http.StatusServiceUnavailable || refused.Header().Get("Retry-After") == "" {
				t.Errorf("work exceeding the shared budget got HTTP %d", refused.Code)
			}
			if pair.waiter.served.Load() != before || len(pair.waiter.slots) != 0 || len(pair.holder.slots) != 1 {
				t.Fatal("the refused request gained a projection or retained a slot")
			}
			cancellations := pair.waiter.cancelled.Load()
			cancelled, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			handler.ServeHTTP(httptest.NewRecorder(), request(cancelled, pair.second))
			if pair.waiter.cancelled.Load() != cancellations+1 || len(pair.waiter.slots) != 0 {
				t.Fatal("the request did not cancel its wait and release its slot")
			}
		})
		if len(resource.slots) != 0 || len(bootstrap.slots) != 0 || !budget.used.TryAcquire(10) {
			t.Fatal("the completed request held a slot or byte reservation")
		}
		budget.used.Release(10)
	}
}
