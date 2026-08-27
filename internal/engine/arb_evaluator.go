package engine

import (
	"math"
	"sync"
	"time"

	"karbit/internal/graph"
	"karbit/internal/risk"
)

// ArbitrageOpportunity contains the complete evaluation metrics of an identified triangular or quadrilateral arbitrage.
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
	Leg4Price            float64         `json:"leg4_price,omitempty"`
	Leg4Qty              float64         `json:"leg4_qty,omitempty"`
	Leg4DepthRatio       float64         `json:"leg4_depth_ratio,omitempty"`
	LatencyMs            int64           `json:"latency_ms"`
	EvaluationDurationNs int64           `json:"evaluation_duration_ns"`
}

// ArbEvaluator handles the high-frequency evaluation of triangular and quadrilateral paths.
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
	steps := math.Floor(qty / stepSize)
	return steps * stepSize
}

// Evaluate evaluates a single path (3-hop or 4-hop) given a fresh snapshot of quotes.
func (e *ArbEvaluator) Evaluate(tri *graph.Triangle, quotes []TickerState, startUSDT float64, nowMs int64) (*ArbitrageOpportunity, bool) {
	evalStart := time.Now()

	e.mu.RLock()
	feeModel := e.feeModel
	minProfitThreshold := e.minProfitPercent
	e.mu.RUnlock()

	expectedHops := 3
	if tri.HopCount == 4 {
		expectedHops = 4
	}
	if len(quotes) < expectedHops {
		return nil, false
	}

	// 1. Latency & Freshness Guard
	isFresh, maxAge := e.latencyGuard.AreTriangleQuotesFresh(quotes[:expectedHops], nowMs)
	if !isFresh {
		return nil, false
	}

	q1, q2, q3 := quotes[0], quotes[1], quotes[2]

	// 2. Leg 1: Base -> Asset A
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

	var (
		leg3Price         float64
		leg3Qty           float64
		leg3DepthRatio    float64
		leg4Price         float64
		leg4Qty           float64
		leg4DepthRatio    float64
		finalUSDT         float64
		grossFinalUSDT    float64
		maxDepthRatio     float64
	)

	if expectedHops == 3 {
		// 4-A. Leg 3: Asset B -> Base (3-Hop Loop Closure)
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

		// Calculate Gross Profit 3-Hop
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
		maxDepthRatio = math.Max(leg1DepthRatio, math.Max(leg2DepthRatio, leg3DepthRatio))

	} else {
		// 4-B. Leg 3: Asset B -> Asset C (4-Hop Intermediate)
		var netQtyC float64
		if tri.Leg3.Action == graph.ActionBuy {
			leg3Price = q3.BestAskPrice
			if leg3Price <= 0 {
				return nil, false
			}
			rawQtyC := netQtyB / leg3Price
			leg3Qty = adjustToStepSize(rawQtyC, tri.Leg3.StepSize)
			notionalB := leg3Qty * leg3Price
			if leg3Qty <= 0 || leg3Qty < tri.Leg3.MinQty || notionalB < tri.Leg3.MinNotional {
				return nil, false
			}
			if q3.BestAskQty > 0 {
				leg3DepthRatio = leg3Qty / q3.BestAskQty
			}
			netQtyC = feeModel.CalculateNetAfterFee(leg3Qty)
		} else {
			leg3Price = q3.BestBidPrice
			if leg3Price <= 0 {
				return nil, false
			}
			leg3Qty = adjustToStepSize(netQtyB, tri.Leg3.StepSize)
			grossC := leg3Qty * leg3Price
			if leg3Qty <= 0 || leg3Qty < tri.Leg3.MinQty || grossC < tri.Leg3.MinNotional {
				return nil, false
			}
			if q3.BestBidQty > 0 {
				leg3DepthRatio = leg3Qty / q3.BestBidQty
			}
			netQtyC = feeModel.CalculateNetAfterFee(grossC)
		}

		// 4-C. Leg 4: Asset C -> Base (4-Hop Loop Closure)
		q4 := quotes[3]
		leg4 := tri.Leg4
		if leg4 == nil {
			return nil, false
		}
		if leg4.Action == graph.ActionSell {
			leg4Price = q4.BestBidPrice
			if leg4Price <= 0 {
				return nil, false
			}
			leg4Qty = adjustToStepSize(netQtyC, leg4.StepSize)
			grossUSDT := leg4Qty * leg4Price
			if leg4Qty <= 0 || leg4Qty < leg4.MinQty || grossUSDT < leg4.MinNotional {
				return nil, false
			}
			if q4.BestBidQty > 0 {
				leg4DepthRatio = leg4Qty / q4.BestBidQty
			}
			finalUSDT = feeModel.CalculateNetAfterFee(grossUSDT)
		} else {
			leg4Price = q4.BestAskPrice
			if leg4Price <= 0 {
				return nil, false
			}
			rawFinalUSDT := netQtyC / leg4Price
			leg4Qty = adjustToStepSize(rawFinalUSDT, leg4.StepSize)
			notionalC := leg4Qty * leg4Price
			if leg4Qty <= 0 || leg4Qty < leg4.MinQty || notionalC < leg4.MinNotional {
				return nil, false
			}
			if q4.BestAskQty > 0 {
				leg4DepthRatio = leg4Qty / q4.BestAskQty
			}
			finalUSDT = feeModel.CalculateNetAfterFee(leg4Qty)
		}

		// Calculate Gross Profit 4-Hop
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
		if tri.Leg3.Action == graph.ActionBuy {
			grossFinalUSDT = grossFinalUSDT / leg3Price
		} else {
			grossFinalUSDT = grossFinalUSDT * leg3Price
		}
		if leg4.Action == graph.ActionSell {
			grossFinalUSDT = grossFinalUSDT * leg4Price
		} else {
			grossFinalUSDT = grossFinalUSDT / leg4Price
		}
		maxDepthRatio = math.Max(math.Max(leg1DepthRatio, leg2DepthRatio), math.Max(leg3DepthRatio, leg4DepthRatio))
	}

	netProfitUSDT := finalUSDT - startUSDT
	netProfitPercent := (netProfitUSDT / startUSDT) * 100.0

	if netProfitPercent < minProfitThreshold {
		return nil, false
	}

	grossProfitUSDT := grossFinalUSDT - startUSDT
	grossProfitPercent := (grossProfitUSDT / startUSDT) * 100.0
	totalFeesUSDT := grossFinalUSDT - finalUSDT

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
		Leg4Price:            leg4Price,
		Leg4Qty:              leg4Qty,
		Leg4DepthRatio:       leg4DepthRatio,
		LatencyMs:            maxAge,
		EvaluationDurationNs: evalDuration,
	}

	return opp, true
}

// EvaluateRaw calculates current spread and prices for a path regardless of profit threshold.
func (e *ArbEvaluator) EvaluateRaw(tri *graph.Triangle, quotes []TickerState, startUSDT float64, nowMs int64) (*ArbitrageOpportunity, bool) {
	evalStart := time.Now()

	e.mu.RLock()
	feeModel := e.feeModel
	e.mu.RUnlock()

	expectedHops := 3
	if tri.HopCount == 4 {
		expectedHops = 4
	}
	if len(quotes) < expectedHops {
		return nil, false
	}

	q1, q2, q3 := quotes[0], quotes[1], quotes[2]
	if (tri.Leg1.Action == graph.ActionBuy && q1.BestAskPrice <= 0) || (tri.Leg1.Action == graph.ActionSell && q1.BestBidPrice <= 0) {
		return nil, false
	}
	if (tri.Leg2.Action == graph.ActionBuy && q2.BestAskPrice <= 0) || (tri.Leg2.Action == graph.ActionSell && q2.BestBidPrice <= 0) {
		return nil, false
	}
	if (tri.Leg3.Action == graph.ActionBuy && q3.BestAskPrice <= 0) || (tri.Leg3.Action == graph.ActionSell && q3.BestBidPrice <= 0) {
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

	var (
		leg3Price      float64
		leg3Qty        float64
		leg4Price      float64
		leg4Qty        float64
		finalUSDT      float64
		grossFinalUSDT float64
	)

	if expectedHops == 3 {
		// 3-Hop Closure
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
	} else {
		// 4-Hop Intermediate Leg 3
		var netQtyC float64
		if tri.Leg3.Action == graph.ActionBuy {
			leg3Price = q3.BestAskPrice
			if leg3Price <= 0 {
				return nil, false
			}
			rawQtyC := netQtyB / leg3Price
			leg3Qty = adjustToStepSize(rawQtyC, tri.Leg3.StepSize)
			netQtyC = feeModel.CalculateNetAfterFee(leg3Qty)
		} else {
			leg3Price = q3.BestBidPrice
			if leg3Price <= 0 {
				return nil, false
			}
			leg3Qty = adjustToStepSize(netQtyB, tri.Leg3.StepSize)
			grossC := leg3Qty * leg3Price
			netQtyC = feeModel.CalculateNetAfterFee(grossC)
		}

		// 4-Hop Closure Leg 4
		q4 := quotes[3]
		if (tri.Leg4.Action == graph.ActionBuy && q4.BestAskPrice <= 0) || (tri.Leg4.Action == graph.ActionSell && q4.BestBidPrice <= 0) {
			return nil, false
		}
		leg4 := tri.Leg4
		if leg4.Action == graph.ActionSell {
			leg4Price = q4.BestBidPrice
			if leg4Price <= 0 {
				return nil, false
			}
			leg4Qty = adjustToStepSize(netQtyC, leg4.StepSize)
			grossUSDT := leg4Qty * leg4Price
			finalUSDT = feeModel.CalculateNetAfterFee(grossUSDT)
		} else {
			leg4Price = q4.BestAskPrice
			if leg4Price <= 0 {
				return nil, false
			}
			rawFinalUSDT := netQtyC / leg4Price
			leg4Qty = adjustToStepSize(rawFinalUSDT, leg4.StepSize)
			finalUSDT = feeModel.CalculateNetAfterFee(leg4Qty)
		}

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
		if tri.Leg3.Action == graph.ActionBuy {
			grossFinalUSDT = grossFinalUSDT / leg3Price
		} else {
			grossFinalUSDT = grossFinalUSDT * leg3Price
		}
		if leg4.Action == graph.ActionSell {
			grossFinalUSDT = grossFinalUSDT * leg4Price
		} else {
			grossFinalUSDT = grossFinalUSDT / leg4Price
		}
	}

	netProfitUSDT := finalUSDT - startUSDT
	netProfitPercent := (netProfitUSDT / startUSDT) * 100.0
	grossProfitUSDT := grossFinalUSDT - startUSDT
	grossProfitPercent := (grossProfitUSDT / startUSDT) * 100.0

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
		TotalFeesUSDT:        grossFinalUSDT - finalUSDT,
		Leg1Price:            leg1Price,
		Leg1Qty:              leg1Qty,
		Leg2Price:            leg2Price,
		Leg2Qty:              leg2Qty,
		Leg3Price:            leg3Price,
		Leg3Qty:              leg3Qty,
		Leg4Price:            leg4Price,
		Leg4Qty:              leg4Qty,
		EvaluationDurationNs: evalDuration,
	}

	return opp, true
}
