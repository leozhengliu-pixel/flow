package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScaleP16FamilyRollbackAndHydrationGate(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"project", "initiative"} {
		for _, hydration := range []string{"off", "on"} {
			t.Setenv("FLOW_RECORD_HYDRATION", hydration)
			for _, mode := range []string{"", "off", "on"} {
				t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", mode)
				t.Setenv("FLOW_INITIATIVE_RECORD_MUTATIONS", mode)
				got := projectRecordScope(ctx, kind, "comment", "target", nil)
				enabled := mode == "on" && hydration == "on"
				if (got != ctx) != enabled {
					t.Fatalf("family=%s hydration=%s mode=%q: scope enabled=%t", kind, hydration, mode, got != ctx)
				}
				request := httptest.NewRequest(http.MethodPost, "/", nil)
				request.SetPathValue("id", "target")
				projectRecordHandler(kind, "comment", func(_ http.ResponseWriter, current *http.Request) {
					if !enabled && current != request {
						t.Fatal("disabled family changed the request")
					}
				})(httptest.NewRecorder(), request)
			}
		}
	}
}
