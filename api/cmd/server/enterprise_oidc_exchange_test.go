package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// The token exchange of a workspace identity provider goes through the
// outbound policy: a token endpoint on a loopback address is refused and
// never receives the code or the client secret.
func TestEnterpriseOIDCTokenExchangeUsesOutboundPolicy(t *testing.T) {
	var hits int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"x","token_type":"bearer"}`))
	}))
	defer internal.Close()
	config := oauth2.Config{ClientID: "client", ClientSecret: "secret", Endpoint: oauth2.Endpoint{TokenURL: internal.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := config.Exchange(enterpriseOIDCContext(ctx), "code"); err == nil {
		t.Fatal("a loopback token endpoint was accepted")
	}
	if hits != 0 {
		t.Fatalf("the token endpoint received %d request(s)", hits)
	}
}
