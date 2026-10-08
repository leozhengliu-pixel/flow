package main

import (
	"encoding/json"
	"testing"
)

func TestInsightsConfigAcceptsCustomerDimension(t *testing.T) {
	raw := json.RawMessage(`{"measure":"issueCount","slice":"customer","segment":"customer","hideEmptySlice":true,"hideUnknownCustomer":true}`)
	normalized, ok := normalizeInsightsConfig(raw)
	if !ok {
		t.Fatal("customer slice/segment rejected")
	}
	var stored map[string]any
	if err := json.Unmarshal(normalized, &stored); err != nil {
		t.Fatal(err)
	}
	if stored["slice"] != "customer" || stored["segment"] != "customer" || stored["hideUnknownCustomer"] != true || stored["hideEmptySlice"] != true {
		t.Fatalf("customer insight flags lost: %s", normalized)
	}
	if _, ok := normalizeInsightsConfig(json.RawMessage(`{"slice":"customers"}`)); ok {
		t.Fatal("unknown dimension accepted")
	}
}
