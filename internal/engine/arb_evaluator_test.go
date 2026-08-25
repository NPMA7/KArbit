package engine

import (
	"testing"
	"time"

	"karbit/internal/graph"
	"karbit/internal/risk"
)

func createTestTriangle() *graph.Triangle {
	leg1 := graph.Leg{
		Symbol:      "BTCUSDT",
		FromAsset:   "USDT",
		ToAsset:     "BTC",
		Action:      graph.ActionBuy,
		StepSize:    0.00001,
		TickSize:    0.01,
		MinNotional: 5.0,
		MinQty:      0.00001,
	}
	leg2 := graph.Leg{
		Symbol:      "ETHBTC",
		FromAsset:   "BTC",
		ToAsset:     "ETH",
		Action:      graph.ActionBuy, // Market ETHBTC, buying ETH with BTC
		StepSize:    0.0001,
		TickSize:    0.00001,
		MinNotional: 0.0001,
		MinQty:      0.0001,
	}
	leg3 := graph.Leg{
		Symbol:      "ETHUSDT",
		FromAsset:   "ETH",
		ToAsset:     "USDT",
		Action:      graph.ActionSell, // Market ETHUSDT, selling ETH for USDT
		StepSize:    0.0001,
		TickSize:    0.01,
		MinNotional: 5.0,
		MinQty:      0.0001,
	}

	return graph.NewTriangle("USDT", "BTC", "ETH", leg1, leg2, leg3)
}

func TestArbEvaluator_ProfitableOpportunity(t *testing.T) {
	feeModel := risk.NewFeeModel(0.00075, true) // 0.075% BNB fee
	latencyGuard := NewLatencyGuard(50)
	evaluator := NewArbEvaluator(feeModel, latencyGuard, 0.01) // > 0.01% net profit

	tri := createTestTriangle()
	nowMs := time.Now().UnixMilli()

	// Construct prices with deliberate arbitrage mispricing:
	// BTCUSDT: Ask = 50,000 USDT (100 USDT buys 0.002 BTC)
	// ETHBTC: Ask = 0.05 BTC (0.002 BTC buys 0.04 ETH)
	// ETHUSDT: Bid = 2,600 USDT (0.04 ETH sells for 104 USDT -> ~4% profit gross)
	quotes := [3]TickerState{
		{
			Symbol:          "BTCUSDT",
			BestAskPrice:    50000.0,
			BestAskQty:      10.0,
			BestBidPrice:    49990.0,
			BestBidQty:      10.0,
			EventTimeMs:     nowMs - 5,
			LocalRecvTimeMs: nowMs - 5,
		},
		{
			Symbol:          "ETHBTC",
			BestAskPrice:    0.050,
			BestAskQty:      100.0,
			BestBidPrice:    0.049,
			BestBidQty:      100.0,
			EventTimeMs:     nowMs - 5,
			LocalRecvTimeMs: nowMs - 5,
		},
		{
			Symbol:          "ETHUSDT",
			BestBidPrice:    2700.0,
			BestBidQty:      50.0,
			BestAskPrice:    2701.0,
			BestAskQty:      50.0,
			EventTimeMs:     nowMs - 5,
			LocalRecvTimeMs: nowMs - 5,
		},
	}

	opp, found := evaluator.Evaluate(tri, quotes, 100.0, nowMs)
	if !found {
		t.Fatalf("expected profitable arbitrage opportunity to be detected")
	}

	t.Logf("Detected Arbitrage Opportunity: %s", opp.Triangle.ID)
	t.Logf("Start Capital: $%.2f | Final: $%.4f | Net Profit: +$%.4f (+%.3f%%)",
		opp.StartAmountUSDT, opp.FinalAmountUSDT, opp.NetProfitUSDT, opp.NetProfitPercent)
	t.Logf("Leg 1: Qty=%.5f Price=%.2f | Leg 2: Qty=%.3f Price=%.4f | Leg 3: Qty=%.3f Price=%.2f",
		opp.Leg1Qty, opp.Leg1Price, opp.Leg2Qty, opp.Leg2Price, opp.Leg3Qty, opp.Leg3Price)

	if opp.NetProfitUSDT <= 0 {
		t.Errorf("expected positive net profit, got %.4f", opp.NetProfitUSDT)
	}

	if opp.NetProfitPercent < 3.0 {
		t.Errorf("expected ~3-4%% net profit, got %.2f%%", opp.NetProfitPercent)
	}
}

func TestArbEvaluator_StaleQuoteDiscard(t *testing.T) {
	feeModel := risk.NewFeeModel(0.00075, true)
	latencyGuard := NewLatencyGuard(50) // Max 50ms
	evaluator := NewArbEvaluator(feeModel, latencyGuard, 0.01)

	tri := createTestTriangle()
	nowMs := time.Now().UnixMilli()

	// Quote that is 150ms old (stale!)
	quotes := [3]TickerState{
		{
			Symbol:          "BTCUSDT",
			BestAskPrice:    50000.0,
			BestAskQty:      10.0,
			EventTimeMs:     nowMs - 150,
			LocalRecvTimeMs: nowMs - 150,
		},
		{
			Symbol:          "ETHBTC",
			BestAskPrice:    0.050,
			BestAskQty:      100.0,
			EventTimeMs:     nowMs - 150,
			LocalRecvTimeMs: nowMs - 150,
		},
		{
			Symbol:          "ETHUSDT",
			BestBidPrice:    2600.0,
			BestBidQty:      50.0,
			EventTimeMs:     nowMs - 150,
			LocalRecvTimeMs: nowMs - 150,
		},
	}

	_, found := evaluator.Evaluate(tri, quotes, 100.0, nowMs)
	if found {
		t.Fatalf("expected stale quote (>50ms) to be discarded by LatencyGuard")
	}
}

func TestArbEvaluator_EvaluateRaw(t *testing.T) {
	feeModel := risk.NewFeeModel(0.00075, true)
	latencyGuard := NewLatencyGuard(50)
	evaluator := NewArbEvaluator(feeModel, latencyGuard, 0.01)

	tri := createTestTriangle()
	nowMs := time.Now().UnixMilli()

	quotes := [3]TickerState{
		{
			Symbol:          "BTCUSDT",
			BestAskPrice:    60000.0,
			BestBidPrice:    59990.0,
			LocalRecvTimeMs: nowMs,
		},
		{
			Symbol:          "ETHBTC",
			BestAskPrice:    0.040,
			BestBidPrice:    0.0399,
			LocalRecvTimeMs: nowMs,
		},
		{
			Symbol:          "ETHUSDT",
			BestBidPrice:    2400.0,
			BestAskPrice:    2401.0,
			LocalRecvTimeMs: nowMs,
		},
	}

	opp, ok := evaluator.EvaluateRaw(tri, quotes, 100.0, nowMs)
	if !ok {
		t.Fatalf("expected EvaluateRaw to return true, got false")
	}

	t.Logf("EvaluateRaw result: NetPct=%.4f%%, GrossPct=%.4f%%", opp.NetProfitPercent, opp.GrossProfitPercent)
}

func BenchmarkArbEvaluator_Evaluate(b *testing.B) {
	feeModel := risk.NewFeeModel(0.00075, true)
	latencyGuard := NewLatencyGuard(50)
	evaluator := NewArbEvaluator(feeModel, latencyGuard, 0.01)

	tri := createTestTriangle()
	nowMs := time.Now().UnixMilli()

	quotes := [3]TickerState{
		{
			Symbol:          "BTCUSDT",
			BestAskPrice:    50000.0,
			BestAskQty:      10.0,
			BestBidPrice:    49990.0,
			BestBidQty:      10.0,
			EventTimeMs:     nowMs - 2,
			LocalRecvTimeMs: nowMs - 2,
		},
		{
			Symbol:          "ETHBTC",
			BestAskPrice:    0.050,
			BestAskQty:      100.0,
			BestBidPrice:    0.049,
			BestBidQty:      100.0,
			EventTimeMs:     nowMs - 2,
			LocalRecvTimeMs: nowMs - 2,
		},
		{
			Symbol:          "ETHUSDT",
			BestBidPrice:    2700.0,
			BestBidQty:      50.0,
			BestAskPrice:    2701.0,
			BestAskQty:      50.0,
			EventTimeMs:     nowMs - 2,
			LocalRecvTimeMs: nowMs - 2,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		evaluator.Evaluate(tri, quotes, 100.0, nowMs)
	}
}
