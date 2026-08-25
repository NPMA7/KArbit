package graph

import (
	"testing"

	"karbit/internal/exchange"
)

func TestBuildGraphIndex(t *testing.T) {
	mockSymbols := []exchange.ParsedSymbol{
		{
			Symbol:      "BTCUSDT",
			BaseAsset:   "BTC",
			QuoteAsset:  "USDT",
			StepSize:    0.00001,
			TickSize:    0.01,
			MinNotional: 5.0,
			MinQty:      0.00001,
		},
		{
			Symbol:      "ETHBTC",
			BaseAsset:   "ETH",
			QuoteAsset:  "BTC",
			StepSize:    0.001,
			TickSize:    0.00001,
			MinNotional: 0.0001,
			MinQty:      0.001,
		},
		{
			Symbol:      "ETHUSDT",
			BaseAsset:   "ETH",
			QuoteAsset:  "USDT",
			StepSize:    0.001,
			TickSize:    0.01,
			MinNotional: 5.0,
			MinQty:      0.001,
		},
		{
			Symbol:      "SOLUSDT",
			BaseAsset:   "SOL",
			QuoteAsset:  "USDT",
			StepSize:    0.01,
			TickSize:    0.01,
			MinNotional: 5.0,
			MinQty:      0.01,
		},
		{
			Symbol:      "SOLBTC",
			BaseAsset:   "SOL",
			QuoteAsset:  "BTC",
			StepSize:    0.01,
			TickSize:    0.000001,
			MinNotional: 0.0001,
			MinQty:      0.01,
		},
	}

	index := BuildGraphIndex(mockSymbols, "USDT")

	if len(index.Triangles) == 0 {
		t.Fatalf("expected triangles to be discovered, got 0")
	}

	t.Logf("Discovered %d triangles across %d symbols", len(index.Triangles), index.UniqueSymbolsCount)

	// Check if BTCUSDT is mapped
	trianglesForBTCUSDT := index.SymbolToTriangles["BTCUSDT"]
	if len(trianglesForBTCUSDT) == 0 {
		t.Errorf("expected BTCUSDT to be indexed in triangles")
	}

	for _, tri := range index.Triangles {
		t.Logf("Found Triangle: %s (Symbols: %v)", tri.ID, tri.Symbols)
		if tri.BaseAsset != "USDT" {
			t.Errorf("expected base asset USDT, got %s", tri.BaseAsset)
		}
	}
}
