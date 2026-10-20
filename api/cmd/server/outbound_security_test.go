package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"flow/api/internal/outbound"
)

func TestSecureOutboundClientRejectsProxyAndReservedAddresses(t *testing.T) {
	for _, access := range []outboundAccess{outboundPublicOnly, outboundTrustedPrivate} {
		client := secureOutboundClient(time.Second, access)
		transport := client.Transport.(*http.Transport)
		if transport.Proxy != nil {
			t.Fatal("environment proxies bypass destination validation")
		}
		for _, address := range []string{"127.0.0.1:80", "169.254.169.254:80", "100.64.0.1:80", "192.0.2.1:80", "224.0.0.1:80", "[64:ff9b::7f00:1]:80", "10.218.161.13:443"} {
			connection, err := transport.DialContext(context.Background(), "tcp", address)
			if connection != nil {
				connection.Close()
			}
			if err == nil || !outbound.IsRefusal(err) {
				t.Errorf("%s: expected blocked address, got %v", address, err)
			}
		}
	}
}
