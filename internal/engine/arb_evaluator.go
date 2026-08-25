package engine

import (
	"math"
	"sync"
	"time"

	"karbit/internal/graph"
	"karbit/internal/risk"
)

// ArbitrageOpportunity contains the complete evaluation metrics of an identified triangular arbitrage.
type ArbitrageOpportunity struct {
	Triangle             *graph.Triangle `json:"triangle"`
	Timestamp            time.Time       `json:"timestamp"`
	StartAmountUSDT      float64         `json:"start_amount_usdt"`
	FinalAmountUSDT      float64         `json:"final_amount_usdt"`
	GrossProfitUSDT      float64         `json:"gross_profit_usdt"`
	GrossProfitPercent   float64         `json:"gross_profit_percent"`
	NetProfitUSDT        float64         `json:"net_profit_usdt"`
	NetProfitPercent     float64         `json:"net_profit_percent"`
	TotalFeesUSDT        float64         `json:"total_fees_usdt"`
	EstimatedSlippage    float64         `json:"estimated_slippage"`
	Leg1Price            float64         `json:"leg1_price"`
	Leg1Qty              float64         `json:"leg1_qty"`
	Leg1DepthRatio       float64         `json:"leg1_depth_ratio"`
	Leg2Price            float64         `json:"leg2_price"`
	Leg2Qty              float64         `json:"leg2_qty"`
	Leg2DepthRatio       float64         `json:"leg2_depth_ratio"`
	Leg3Price            float64         `json:"leg3_price"`
	Leg3Qty              float64         `json:"leg3_qty"`
	Leg3DepthRatio       float64         `json:"leg3_depth_ratio"`
	LatencyMs            int64           `json:"latency_ms"`
	EvaluationDurationNs int64           `json:"evaluation_duration_ns"`
}

// ArbEvaluator handles the high-frequency evaluation of triangular paths.
type ArbEvaluator struct {
	mu               sync.RWMutex
	feeModel         *risk.FeeModel
	latencyGuard     *LatencyGuard
	minProfitPercent float64
}

// NewArbEvaluator constructs an evaluator instance.
func NewArbEvaluator(feeModel *risk.FeeModel, latencyGuard *LatencyGuard, minProfitPercent float64) *ArbEvaluator {
	return &ArbEvaluator{
		feeModel:         feeModel,
		latencyGuard:     latencyGuard,
		minProfitPercent: minProfitPercent,
	}
}

// SetMinProfitPercent updates the minimum profit filter on the fly.
func (e *ArbEvaluator) SetMinProfitPercent(pct float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.minProfitPercent = pct
}

// SetFeeModel updates the fee model on the fly.
func (e *ArbEvaluator) SetFeeModel(feeModel *risk.FeeModel) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.feeModel = feeModel
}

// SetMaxLatency updates the latency guard threshold dynamically.
func (e *ArbEvaluator) SetMaxLatency(maxLatencyMs int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.latencyGuard.SetMaxLatency(maxLatencyMs)
}

// adjustToStepSize rounds down quantity to the nearest stepSize precision.
func adjustToStepSize(qty, stepSize float64) float64 {
	if stepSize <= 0 || math.IsNaN(stepSize) {
		return qty
	}
	// Avoid floating point division glitches by scaling
	steps := math.Floor(qty / stepSize)
	return steps * stepSize
}

// Evaluate evaluates a single triangle given a fresh snapshot of its 3 quotes.
func (e *ArbEvaluator) Evaluate(tri *graph.Triangle, quotes [3]TickerState, startUSDT float64, nowMs int64) (*ArbitrageOpportunity, bool) {
	evalStart := time.Now()

	e.mu.RLock()
	feeModel := e.feeModel
	minProfitThreshold := e.minProfitPercent
	e.mu.RUnlock()

	// 1. Latency & Freshness Guard
	isFresh, maxAge := e.latencyGuard.AreTriangleQuotesFresh(quotes, nowMs)
	if !isFresh {
		return nil, false
	}

	q1, q2, q3 := quotes[0], quotes[1], quotes[2]

	// 2. Leg 1: BaseCurrency (USDT) -> Asset A
	var (
		leg1Price      float64
		leg1Qty        float64
		leg1DepthRatio float64
		netQtyA        float64
	)

	if tri.Leg1.Action == graph.ActionBuy {
		leg1Price = q1.BestAskPrice
		if leg1Price <= 0 {
			return nil, false
		}
		rawQtyA := startUSDT / leg1Price
		leg1Qty = adjustToStepSize(rawQtyA, tri.Leg1.StepSize)
		if leg1Qty <= 0 || leg1Qty < tri.Leg1.MinQty || (leg1Qty*leg1Price) < tri.Leg1.MinNotional {
			return nil, false
		}
		if q1.BestAskQty > 0 {
			leg1DepthRatio = leg1Qty / q1.BestAskQty
		}
		netQtyA = feeModel.CalculateNetAfterFee(leg1Qty)
	} else {
		leg1Price = q1.BestBidPrice
		if leg1Price <= 0 {
			return nil, false
		}
		leg1Qty = adjustToStepSize(startUSDT, tri.Leg1.StepSize)
		grossA := leg1Qty * leg1Price
		if leg1Qty <= 0 || leg1Qty < tri.Leg1.MinQty || grossA < tri.Leg1.MinNotional {
			return nil, false
		}
		if q1.BestBidQty > 0 {
			leg1DepthRatio = leg1Qty / q1.BestBidQty
		}
		netQtyA = feeModel.CalculateNetAfterFee(grossA)
	}

	// 3. Leg 2: Asset A -> Asset B
	var (
		leg2Price      float64
		leg2Qty        float64
		leg2DepthRatio float64
		netQtyB        float64
	)

	if tri.Leg2.Action == graph.ActionBuy {
		leg2Price = q2.BestAskPrice
		if leg2Price <= 0 {
			return nil, false
		}
		rawQtyB := netQtyA / leg2Price
		leg2Qty = adjustToStepSize(rawQtyB, tri.Leg2.StepSize)
		notionalA := leg2Qty * leg2Price
		if leg2Qty <= 0 || leg2Qty < tri.Leg2.MinQty || notionalA < tri.Leg2.MinNotional {
			return nil, false
		}
		if q2.BestAskQty > 0 {
			leg2DepthRatio = leg2Qty / q2.BestAskQty
		}
		netQtyB = feeModel.CalculateNetAfterFee(leg2Qty)
	} else {
		leg2Price = q2.BestBidPrice
		if leg2Price <= 0 {
			return nil, false
		}
		leg2Qty = adjustToStepSize(netQtyA, tri.Leg2.StepSize)
		grossB := leg2Qty * leg2Price
		if leg2Qty <= 0 || leg2Qty < tri.Leg2.MinQty || grossB < tri.Leg2.MinNotional {
			return nil, false
		}
		if q2.BestBidQty > 0 {
			leg2DepthRatio = leg2Qty / q2.BestBidQty
		}
		netQtyB = feeModel.CalculateNetAfterFee(grossB)
	}

	// 4. Leg 3: Asset B -> BaseCurrency (USDT)
	var (
		leg3Price      float64
		leg3Qty        float64
		leg3DepthRatio float64
		finalUSDT      float64
	)

	if tri.Leg3.Action == graph.ActionSell {
		leg3Price = q3.BestBidPrice
		if leg3Price <= 0 {
			return nil, false
		}
		leg3Qty = adjustToStepSize(netQtyB, tri.Leg3.StepSize)
		grossUSDT := leg3Qty * leg3Price
		if leg3Qty <= 0 || leg3Qty < tri.Leg3.MinQty || grossUSDT < tri.Leg3.MinNotional {
			return nil, false
		}
		if q3.BestBidQty > 0 {
			leg3DepthRatio = leg3Qty / q3.BestBidQty
		}
		finalUSDT = feeModel.CalculateNetAfterFee(grossUSDT)
	} else {
		leg3Price = q3.BestAskPrice
		if leg3Price <= 0 {
			return nil, false
		}
		rawFinalUSDT := netQtyB / leg3Price
		leg3Qty = adjustToStepSize(rawFinalUSDT, tri.Leg3.StepSize)
		notionalB := leg3Qty * leg3Price
		if leg3Qty <= 0 || leg3Qty < tri.Leg3.MinQty || notionalB < tri.Leg3.MinNotional {
			return nil, false
		}
		if q3.BestAskQty > 0 {
			leg3DepthRatio = leg3Qty / q3.BestAskQty
		}
		finalUSDT = feeModel.CalculateNetAfterFee(leg3Qty)
	}

	// 5. Profit & Metrics
	netProfitUSDT := finalUSDT - startUSDT
	netProfitPercent := (netProfitUSDT / startUSDT) * 100.0

	// Filter by minimum profit threshold
	if netProfitPercent < minProfitThreshold {
		return nil, false
	}

	// Calculate Exact Gross Profit (without fees)
	var grossFinalUSDT float64
	if tri.Leg1.Action == graph.ActionBuy {
		grossFinalUSDT = startUSDT / leg1Price
	} else {
		grossFinalUSDT = startUSDT * leg1Price
	}

	if tri.Leg2.Action == graph.ActionBuy {
		grossFinalUSDT = grossFinalUSDT / leg2Price
	} else {
		grossFinalUSDT = grossFinalUSDT * leg2Price
	}

	if tri.Leg3.Action == graph.ActionSell {
		grossFinalUSDT = grossFinalUSDT * leg3Price
	} else {
		grossFinalUSDT = grossFinalUSDT / leg3Price
	}

	grossProfitUSDT := grossFinalUSDT - startUSDT
	grossProfitPercent := (grossProfitUSDT / startUSDT) * 100.0
	totalFeesUSDT := grossFinalUSDT - finalUSDT

	// Estimate slippage risk based on depth saturation
	maxDepthRatio := math.Max(leg1DepthRatio, math.Max(leg2DepthRatio, leg3DepthRatio))
	estimatedSlippage := 0.0
	if maxDepthRatio > 1.0 {
		estimatedSlippage = (maxDepthRatio - 1.0) * 0.0005
	}

	evalDuration := time.Since(evalStart).Nanoseconds()

	opp := &ArbitrageOpportunity{
		Triangle:             tri,
		Timestamp:            time.Now(),
		StartAmountUSDT:      startUSDT,
		FinalAmountUSDT:      finalUSDT,
		GrossProfitUSDT:      grossProfitUSDT,
		GrossProfitPercent:   grossProfitPercent,
		NetProfitUSDT:        netProfitUSDT,
		NetProfitPercent:     netProfitPercent,
		TotalFeesUSDT:        totalFeesUSDT,
		EstimatedSlippage:    estimatedSlippage,
		Leg1Price:            leg1Price,
		Leg1Qty:              leg1Qty,
		Leg1DepthRatio:       leg1DepthRatio,
		Leg2Price:            leg2Price,
		Leg2Qty:              leg2Qty,
		Leg2DepthRatio:       leg2DepthRatio,
		Leg3Price:            leg3Price,
		Leg3Qty:              leg3Qty,
		Leg3DepthRatio:       leg3DepthRatio,
		LatencyMs:            maxAge,
		EvaluationDurationNs: evalDuration,
	}

	return opp, true
}

// EvaluateRaw calculates current spread and prices for a triangle regardless of profit threshold.
func (e *ArbEvaluator) EvaluateRaw(tri *graph.Triangle, quotes [3]TickerState, startUSDT float64, nowMs int64) (*ArbitrageOpportunity, bool) {
	evalStart := time.Now()

	e.mu.RLock()
	feeModel := e.feeModel
	e.mu.RUnlock()

	q1, q2, q3 := quotes[0], quotes[1], quotes[2]
	if (tri.Leg1.Action == graph.ActionBuy && q1.BestAskPrice <= 0) || (tri.Leg1.Action == graph.ActionSell && q1.BestBidPrice <= 0) {
		return nil, false
	}
	if (tri.Leg2.Action == graph.ActionBuy && q2.BestAskPrice <= 0) || (tri.Leg2.Action == graph.ActionSell && q2.BestBidPrice <= 0) {
		return nil, false
	}
	if (tri.Leg3.Action == graph.ActionSell && q3.BestBidPrice <= 0) || (tri.Leg3.Action == graph.ActionBuy && q3.BestAskPrice <= 0) {
		return nil, false
	}

	// 1. Leg 1
	var (
		leg1Price float64
		leg1Qty   float64
		netQtyA   float64
	)
	if tri.Leg1.Action == graph.ActionBuy {
		leg1Price = q1.BestAskPrice
		if leg1Price <= 0 {
			return nil, false
		}
		rawQtyA := startUSDT / leg1Price
		leg1Qty = adjustToStepSize(rawQtyA, tri.Leg1.StepSize)
		netQtyA = feeModel.CalculateNetAfterFee(leg1Qty)
	} else {
		leg1Price = q1.BestBidPrice
		if leg1Price <= 0 {
			return nil, false
		}
		leg1Qty = adjustToStepSize(startUSDT, tri.Leg1.StepSize)
		grossA := leg1Qty * leg1Price
		netQtyA = feeModel.CalculateNetAfterFee(grossA)
	}

	// 2. Leg 2
	var (
		leg2Price float64
		leg2Qty   float64
		netQtyB   float64
	)
	if tri.Leg2.Action == graph.ActionBuy {
		leg2Price = q2.BestAskPrice
		if leg2Price <= 0 {
			return nil, false
		}
		rawQtyB := netQtyA / leg2Price
		leg2Qty = adjustToStepSize(rawQtyB, tri.Leg2.StepSize)
		netQtyB = feeModel.CalculateNetAfterFee(leg2Qty)
	} else {
		leg2Price = q2.BestBidPrice
		if leg2Price <= 0 {
			return nil, false
		}
		leg2Qty = adjustToStepSize(netQtyA, tri.Leg2.StepSize)
		grossB := leg2Qty * leg2Price
		netQtyB = feeModel.CalculateNetAfterFee(grossB)
	}

	// 3. Leg 3
	var (
		leg3Price float64
		leg3Qty   float64
		finalUSDT float64
	)
	if tri.Leg3.Action == graph.ActionSell {
		leg3Price = q3.BestBidPrice
		if leg3Price <= 0 {
			return nil, false
		}
		leg3Qty = adjustToStepSize(netQtyB, tri.Leg3.StepSize)
		grossUSDT := leg3Qty * leg3Price
		finalUSDT = feeModel.CalculateNetAfterFee(grossUSDT)
	} else {
		leg3Price = q3.BestAskPrice
		if leg3Price <= 0 {
			return nil, false
		}
		rawFinalUSDT := netQtyB / leg3Price
		leg3Qty = adjustToStepSize(rawFinalUSDT, tri.Leg3.StepSize)
		finalUSDT = feeModel.CalculateNetAfterFee(leg3Qty)
	}

	// Net Profit
	netProfitUSDT := finalUSDT - startUSDT
	netProfitPercent := (netProfitUSDT / startUSDT) * 100.0

	// Calculate Exact Gross Profit
	var grossFinalUSDT float64
	if tri.Leg1.Action == graph.ActionBuy {
		grossFinalUSDT = startUSDT / leg1Price
	} else {
		grossFinalUSDT = startUSDT * leg1Price
	}

	if tri.Leg2.Action == graph.ActionBuy {
		grossFinalUSDT = grossFinalUSDT / leg2Price
	} else {
		grossFinalUSDT = grossFinalUSDT * leg2Price
	}

	if tri.Leg3.Action == graph.ActionSell {
		grossFinalUSDT = grossFinalUSDT * leg3Price
	} else {
		grossFinalUSDT = grossFinalUSDT / leg3Price
	}

	grossProfitUSDT := grossFinalUSDT - startUSDT
	grossProfitPercent := (grossProfitUSDT / startUSDT) * 100.0

	evalDuration := time.Since(evalStart).Nanoseconds()
	age := nowMs - q1.LocalRecvTimeMs
	if age < 0 {
		age = 0
	}

	return &ArbitrageOpportunity{
		Triangle:             tri,
		Timestamp:            time.Now(),
		StartAmountUSDT:      startUSDT,
		FinalAmountUSDT:      finalUSDT,
		GrossProfitUSDT:      grossProfitUSDT,
		GrossProfitPercent:   grossProfitPercent,
		NetProfitUSDT:        netProfitUSDT,
		NetProfitPercent:     netProfitPercent,
		TotalFeesUSDT:        grossFinalUSDT - finalUSDT,
		Leg1Price:            leg1Price,
		Leg1Qty:              leg1Qty,
		Leg2Price:            leg2Price,
		Leg2Qty:              leg2Qty,
		Leg3Price:            leg3Price,
		Leg3Qty:              leg3Qty,
		LatencyMs:            age,
		EvaluationDurationNs: evalDuration,
	}, true
}
