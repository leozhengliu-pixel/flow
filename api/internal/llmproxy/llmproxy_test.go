package llmproxy

import (
	"testing"

	"flow/api/internal/config"
)

func TestCostMicros(t *testing.T) {
	price := config.ModelPrice{Input: 3, Output: 15, CacheRead: 0.3}
	// 1M input at $3 + 100k output at $15/M = $4.50.
	if got := CostMicros(price, 1_000_000, 100_000, 0, 0); got != 4_500_000 {
		t.Fatalf("cost: %d", got)
	}
}
