package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"karbit/internal/exchange"
	"karbit/internal/risk"
)

// ExecutionResult captures the outcome of an arbitrage execution.
type ExecutionResult struct {
	Opportunity            *ArbitrageOpportunity `json:"opportunity"`
	IsSuccess              bool                  `json:"is_success"`
	ErrorMessage           string                `json:"error_message,omitempty"`
	ExecutionLatencyMs     int64                 `json:"execution_latency_ms"`
	ActualNetProfitUSDT    float64               `json:"actual_net_profit_usdt"`
	WalletBalanceAfterUSDT float64               `json:"wallet_balance_after_usdt"`
	Mode                   string                `json:"mode"`
	ExecutedAt             time.Time             `json:"executed_at"`
}

// Executor coordinates paper trading simulations and live order routing.
type Executor struct {
	mu                     sync.RWMutex
	mode                   string
	virtualWalletBalance   float64
	initialBalance         float64
	totalTrades            uint64
	profitableTrades       uint64
	cumulativePnL          float64
	recentLogs             []ExecutionResult
	maxLogs                int
	riskManager            *risk.RiskManager
	client                 *exchange.BinanceClient
	executionCooldownUntil time.Time
	logFilePath            string
}

// NewExecutor creates an executor instance and loads any persisted logs from disk.
func NewExecutor(mode string, initialCapital float64, rm *risk.RiskManager, client *exchange.BinanceClient) *Executor {
	e := &Executor{
		mode:                 mode,
		virtualWalletBalance: initialCapital,
		initialBalance:       initialCapital,
		maxLogs:              50,
		recentLogs:           make([]ExecutionResult, 0, 50),
		riskManager:          rm,
		client:               client,
		logFilePath:          filepath.Join("logs", "executions.json"),
	}
	e.loadLogs()
	return e
}

// loadLogs reads persisted execution log from disk and restores state.
func (e *Executor) loadLogs() {
	data, err := os.ReadFile(e.logFilePath)
	if err != nil {
		return // file doesn't exist yet — first run
	}
	var persisted persistedState
	if err := json.Unmarshal(data, &persisted); err != nil {
		fmt.Printf("[KArbit] Warning: failed to parse execution log: %v\n", err)
		return
	}
	e.recentLogs = persisted.Logs
	e.cumulativePnL = persisted.CumulativePnL
	e.virtualWalletBalance = persisted.WalletBalance
	atomic.StoreUint64(&e.totalTrades, persisted.TotalTrades)
	atomic.StoreUint64(&e.profitableTrades, persisted.ProfitableTrades)
	if len(e.recentLogs) > 0 {
		fmt.Printf("[KArbit] Restored %d execution log entries from disk.\n", len(e.recentLogs))
	}
}

// persistedState is the JSON schema written to disk.
type persistedState struct {
	Logs             []ExecutionResult `json:"logs"`
	CumulativePnL    float64           `json:"cumulative_pnl"`
	WalletBalance    float64           `json:"wallet_balance"`
	TotalTrades      uint64            `json:"total_trades"`
	ProfitableTrades uint64            `json:"profitable_trades"`
}

// saveLogs writes current execution state to disk (called WITHOUT holding e.mu).
func (e *Executor) saveLogs() {
	state := persistedState{
		Logs:             e.recentLogs,
		CumulativePnL:    e.cumulativePnL,
		WalletBalance:    e.virtualWalletBalance,
		TotalTrades:      atomic.LoadUint64(&e.totalTrades),
		ProfitableTrades: atomic.LoadUint64(&e.profitableTrades),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(e.logFilePath), 0755)
	_ = os.WriteFile(e.logFilePath, data, 0644)
}

// SetMode updates the execution mode dynamically (paper or live).
func (e *Executor) SetMode(mode string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mode = mode
}

// SetCapital updates the base capital per trade.
func (e *Executor) SetCapital(capital float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if capital > 0 {
		e.initialBalance = capital
	}
}

// Execute processes an arbitrage opportunity in either Paper or Live mode.
func (e *Executor) Execute(ctx context.Context, opp *ArbitrageOpportunity) ExecutionResult {
	startExec := time.Now()

	e.mu.Lock()
	defer e.mu.Unlock()

	// Enforce 100ms cooldown between executions to prevent double-firing on same tick
	if time.Now().Before(e.executionCooldownUntil) {
		return ExecutionResult{
			Opportunity:  opp,
			IsSuccess:    false,
			ErrorMessage: "cooldown active",
			Mode:         e.mode,
			ExecutedAt:   time.Now(),
		}
	}
	e.executionCooldownUntil = time.Now().Add(100 * time.Millisecond)

	// 1. Risk Manager Limit & Circuit Breaker Check
	if err := e.riskManager.CheckSafety(opp.StartAmountUSDT, opp.EstimatedSlippage); err != nil {
		res := ExecutionResult{
			Opportunity:  opp,
			IsSuccess:    false,
			ErrorMessage: err.Error(),
			Mode:         e.mode,
			ExecutedAt:   time.Now(),
		}
		e.appendLog(res)
		return res
	}

	if e.mode == "live" {
		return e.executeLive(ctx, opp, startExec)
	}

	// 2. Paper Trading Simulation
	return e.executePaper(opp, startExec)
}

func (e *Executor) executePaper(opp *ArbitrageOpportunity, startExec time.Time) ExecutionResult {
	// Account for slippage penalty if order size was larger than top-of-book depth
	actualProfitUSDT := opp.NetProfitUSDT - (opp.StartAmountUSDT * opp.EstimatedSlippage)
	e.virtualWalletBalance += actualProfitUSDT
	e.cumulativePnL += actualProfitUSDT
	atomic.AddUint64(&e.totalTrades, 1)

	if actualProfitUSDT > 0 {
		atomic.AddUint64(&e.profitableTrades, 1)
	}

	e.riskManager.RecordTrade(actualProfitUSDT)

	latency := time.Since(startExec).Milliseconds()

	res := ExecutionResult{
		Opportunity:            opp,
		IsSuccess:              true,
		ExecutionLatencyMs:     latency,
		ActualNetProfitUSDT:    actualProfitUSDT,
		WalletBalanceAfterUSDT: e.virtualWalletBalance,
		Mode:                   "PAPER",
		ExecutedAt:             time.Now(),
	}

	e.appendLog(res)
	return res
}

func (e *Executor) executeLive(ctx context.Context, opp *ArbitrageOpportunity, startExec time.Time) ExecutionResult {
	if e.client == nil {
		res := ExecutionResult{
			Opportunity:  opp,
			IsSuccess:    false,
			ErrorMessage: "Binance client unconfigured for live trading",
			Mode:         "LIVE",
			ExecutedAt:   time.Now(),
		}
		e.appendLog(res)
		return res
	}

	// Live 3-leg sequential execution with IOC (Immediate-or-Cancel)
	// Leg 1
	side1 := exchange.SideBuy
	if opp.Triangle.Leg1.Action == "SELL" {
		side1 = exchange.SideSell
	}
	req1 := exchange.OrderRequest{
		Symbol:      opp.Triangle.Leg1.Symbol,
		Side:        side1,
		Type:        exchange.TypeLimit,
		TimeInForce: exchange.TimeInForceIOC,
		Quantity:    opp.Leg1Qty,
		Price:       opp.Leg1Price,
		StepSize:    opp.Triangle.Leg1.StepSize,
		TickSize:    opp.Triangle.Leg1.TickSize,
	}
	resp1, err1 := e.client.CreateOrder(ctx, req1)
	if err1 != nil {
		res := ExecutionResult{
			Opportunity:  opp,
			IsSuccess:    false,
			ErrorMessage: fmt.Sprintf("Leg 1 failed: %v", err1),
			Mode:         "LIVE",
			ExecutedAt:   time.Now(),
		}
		e.appendLog(res)
		return res
	}

	execQty1, _ := strconv.ParseFloat(resp1.ExecutedQty, 64)
	if execQty1 <= 0 {
		execQty1 = opp.Leg1Qty
	}

	// Leg 2
	side2 := exchange.SideBuy
	if opp.Triangle.Leg2.Action == "SELL" {
		side2 = exchange.SideSell
	}
	qty2 := opp.Leg2Qty
	if opp.Triangle.Leg2.Action == "SELL" && execQty1 > 0 {
		qty2 = execQty1 * 0.999 // Fee safety margin
	}
	req2 := exchange.OrderRequest{
		Symbol:      opp.Triangle.Leg2.Symbol,
		Side:        side2,
		Type:        exchange.TypeLimit,
		TimeInForce: exchange.TimeInForceIOC,
		Quantity:    qty2,
		Price:       opp.Leg2Price,
		StepSize:    opp.Triangle.Leg2.StepSize,
		TickSize:    opp.Triangle.Leg2.TickSize,
	}
	resp2, err2 := e.client.CreateOrder(ctx, req2)
	if err2 != nil {
		// EMERGENCY AUTO-ROLLBACK: Leg 1 was filled (resp1.OrderID).
		// Immediately liquidate Leg 1 asset back to USDT so funds never get stuck.
		unwindSide := exchange.SideSell
		if opp.Triangle.Leg1.Action == "SELL" {
			unwindSide = exchange.SideBuy
		}
		unwindReq := exchange.OrderRequest{
			Symbol:      opp.Triangle.Leg1.Symbol,
			Side:        unwindSide,
			Type:        exchange.TypeMarket,
			Quantity:    execQty1 * 0.999,
			StepSize:    opp.Triangle.Leg1.StepSize,
			TickSize:    opp.Triangle.Leg1.TickSize,
		}
		unwindResp, unwindErr := e.client.CreateOrder(ctx, unwindReq)
		unwindMsg := ""
		if unwindErr != nil {
			unwindMsg = fmt.Sprintf(" | ⚠️ Auto-rollback error: %v", unwindErr)
		} else {
			unwindMsg = fmt.Sprintf(" | 🛡️ Auto-rollback OK (sold back to USDT orderId=%d)", unwindResp.OrderID)
		}

		res := ExecutionResult{
			Opportunity:  opp,
			IsSuccess:    false,
			ErrorMessage: fmt.Sprintf("Leg 2 failed (Leg 1 filled orderId=%d): %v%s", resp1.OrderID, err2, unwindMsg),
			Mode:         "LIVE",
			ExecutedAt:   time.Now(),
		}
		e.appendLog(res)
		return res
	}

	execQty2, _ := strconv.ParseFloat(resp2.ExecutedQty, 64)
	if execQty2 <= 0 {
		execQty2 = opp.Leg2Qty
	}

	// Leg 3
	side3 := exchange.SideSell
	if opp.Triangle.Leg3.Action == "BUY" {
		side3 = exchange.SideBuy
	}
	qty3 := opp.Leg3Qty
	if opp.Triangle.Leg3.Action == "SELL" && execQty2 > 0 {
		qty3 = execQty2 * 0.999 // Fee safety margin to strictly prevent insufficient balance
	}
	req3 := exchange.OrderRequest{
		Symbol:      opp.Triangle.Leg3.Symbol,
		Side:        side3,
		Type:        exchange.TypeLimit,
		TimeInForce: exchange.TimeInForceIOC,
		Quantity:    qty3,
		Price:       opp.Leg3Price,
		StepSize:    opp.Triangle.Leg3.StepSize,
		TickSize:    opp.Triangle.Leg3.TickSize,
	}
	resp3, err3 := e.client.CreateOrder(ctx, req3)
	if err3 != nil {
		// EMERGENCY AUTO-ROLLBACK: Leg 3 failed.
		// Liquidate AssetB back to Base.
		unwindSide := exchange.SideSell
		if opp.Triangle.Leg2.Action == "BUY" {
			unwindSide = exchange.SideSell
		}
		unwindReq := exchange.OrderRequest{
			Symbol:   opp.Triangle.Leg2.Symbol,
			Side:     unwindSide,
			Type:     exchange.TypeMarket,
			Quantity: execQty2 * 0.999,
			StepSize: opp.Triangle.Leg2.StepSize,
			TickSize: opp.Triangle.Leg2.TickSize,
		}
		unwindResp, unwindErr := e.client.CreateOrder(ctx, unwindReq)
		unwindMsg := ""
		if unwindErr != nil {
			unwindMsg = fmt.Sprintf(" | ⚠️ Auto-rollback error: %v", unwindErr)
		} else {
			unwindMsg = fmt.Sprintf(" | 🛡️ Auto-rollback OK (sold back to Base orderId=%d)", unwindResp.OrderID)
		}

		res := ExecutionResult{
			Opportunity:  opp,
			IsSuccess:    false,
			ErrorMessage: fmt.Sprintf("Leg 3 failed (Leg 1 id=%d, Leg 2 id=%d): %v%s", resp1.OrderID, resp2.OrderID, err3, unwindMsg),
			Mode:         "LIVE",
			ExecutedAt:   time.Now(),
		}
		e.appendLog(res)
		return res
	}

	execQty3, _ := strconv.ParseFloat(resp3.ExecutedQty, 64)
	if execQty3 <= 0 {
		execQty3 = opp.Leg3Qty
	}

	var resp4 *exchange.OrderResponse
	if opp.Triangle.HopCount == 4 && opp.Triangle.Leg4 != nil {
		leg4 := opp.Triangle.Leg4
		side4 := exchange.SideSell
		if leg4.Action == "BUY" {
			side4 = exchange.SideBuy
		}
		qty4 := opp.Leg4Qty
		if leg4.Action == "SELL" && execQty3 > 0 {
			qty4 = execQty3 * 0.999 // Fee safety margin
		}
		req4 := exchange.OrderRequest{
			Symbol:      leg4.Symbol,
			Side:        side4,
			Type:        exchange.TypeLimit,
			TimeInForce: exchange.TimeInForceIOC,
			Quantity:    qty4,
			Price:       opp.Leg4Price,
			StepSize:    leg4.StepSize,
			TickSize:    leg4.TickSize,
		}
		var err4 error
		resp4, err4 = e.client.CreateOrder(ctx, req4)
		if err4 != nil {
			// EMERGENCY AUTO-ROLLBACK: Leg 4 failed. Liquidate AssetC back to Base.
			unwindSide := exchange.SideSell
			if leg4.Action == "BUY" {
				unwindSide = exchange.SideBuy
			}
			unwindReq := exchange.OrderRequest{
				Symbol:   leg4.Symbol,
				Side:     unwindSide,
				Type:     exchange.TypeMarket,
				Quantity: execQty3 * 0.999,
				StepSize: leg4.StepSize,
				TickSize: leg4.TickSize,
			}
			unwindResp, unwindErr := e.client.CreateOrder(ctx, unwindReq)
			unwindMsg := ""
			if unwindErr != nil {
				unwindMsg = fmt.Sprintf(" | ⚠️ Auto-rollback error: %v", unwindErr)
			} else {
				unwindMsg = fmt.Sprintf(" | 🛡️ Auto-rollback OK (sold back to Base orderId=%d)", unwindResp.OrderID)
			}

			res := ExecutionResult{
				Opportunity:  opp,
				IsSuccess:    false,
				ErrorMessage: fmt.Sprintf("Leg 4 failed (L1=%d, L2=%d, L3=%d): %v%s", resp1.OrderID, resp2.OrderID, resp3.OrderID, err4, unwindMsg),
				Mode:         "LIVE",
				ExecutedAt:   time.Now(),
			}
			e.appendLog(res)
			return res
		}
	}

	latency := time.Since(startExec).Milliseconds()
	actualProfitUSDT := opp.NetProfitUSDT
	e.cumulativePnL += actualProfitUSDT
	atomic.AddUint64(&e.totalTrades, 1)
	if actualProfitUSDT > 0 {
		atomic.AddUint64(&e.profitableTrades, 1)
	}

	modeStr := fmt.Sprintf("LIVE (L1=%d, L2=%d, L3=%d)", resp1.OrderID, resp2.OrderID, resp3.OrderID)
	if resp4 != nil {
		modeStr = fmt.Sprintf("LIVE 4-HOP (L1=%d, L2=%d, L3=%d, L4=%d)", resp1.OrderID, resp2.OrderID, resp3.OrderID, resp4.OrderID)
	}

	res := ExecutionResult{
		Opportunity:            opp,
		IsSuccess:              true,
		ExecutionLatencyMs:     latency,
		ActualNetProfitUSDT:    actualProfitUSDT,
		WalletBalanceAfterUSDT: e.virtualWalletBalance,
		Mode:                   modeStr,
		ExecutedAt:             time.Now(),
	}

	e.appendLog(res)
	return res
}

func (e *Executor) appendLog(res ExecutionResult) {
	if len(e.recentLogs) >= e.maxLogs {
		e.recentLogs = e.recentLogs[1:]
	}
	e.recentLogs = append(e.recentLogs, res)
	e.saveLogs()
}

// ClearLogs clears the trade log history and resets simulation metrics.
func (e *Executor) ClearLogs() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.virtualWalletBalance = e.initialBalance
	e.cumulativePnL = 0.0
	atomic.StoreUint64(&e.totalTrades, 0)
	atomic.StoreUint64(&e.profitableTrades, 0)
	e.recentLogs = make([]ExecutionResult, 0, e.maxLogs)
	e.saveLogs()
}

// ResetSession resets all simulated trading performance metrics and logs.
func (e *Executor) ResetSession() {
	e.ClearLogs()
}

// GetSummary returns execution performance metrics and recent trade logs.
func (e *Executor) GetSummary() (walletBalance float64, totalTrades uint64, profitTrades uint64, cumulativePnL float64, logs []ExecutionResult) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	copiedLogs := make([]ExecutionResult, len(e.recentLogs))
	copy(copiedLogs, e.recentLogs)

	return e.virtualWalletBalance, e.totalTrades, e.profitableTrades, e.cumulativePnL, copiedLogs
}
