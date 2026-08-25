package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	req1 := exchange.OrderRequest{
		Symbol:      opp.Triangle.Leg1.Symbol,
		Side:        exchange.SideBuy,
		Type:        exchange.TypeLimit,
		TimeInForce: exchange.TimeInForceIOC,
		Quantity:    opp.Leg1Qty,
		Price:       opp.Leg1Price,
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

	// Leg 2
	side2 := exchange.SideBuy
	if opp.Triangle.Leg2.Action == "SELL" {
		side2 = exchange.SideSell
	}
	req2 := exchange.OrderRequest{
		Symbol:      opp.Triangle.Leg2.Symbol,
		Side:        side2,
		Type:        exchange.TypeLimit,
		TimeInForce: exchange.TimeInForceIOC,
		Quantity:    opp.Leg2Qty,
		Price:       opp.Leg2Price,
	}
	resp2, err2 := e.client.CreateOrder(ctx, req2)
	if err2 != nil {
		res := ExecutionResult{
			Opportunity:  opp,
			IsSuccess:    false,
			ErrorMessage: fmt.Sprintf("Leg 2 failed (Leg 1 filled orderId=%d): %v", resp1.OrderID, err2),
			Mode:         "LIVE",
			ExecutedAt:   time.Now(),
		}
		e.appendLog(res)
		return res
	}

	// Leg 3
	req3 := exchange.OrderRequest{
		Symbol:      opp.Triangle.Leg3.Symbol,
		Side:        exchange.SideSell,
		Type:        exchange.TypeLimit,
		TimeInForce: exchange.TimeInForceIOC,
		Quantity:    opp.Leg3Qty,
		Price:       opp.Leg3Price,
	}
	resp3, err3 := e.client.CreateOrder(ctx, req3)
	if err3 != nil {
		res := ExecutionResult{
			Opportunity:  opp,
			IsSuccess:    false,
			ErrorMessage: fmt.Sprintf("Leg 3 failed (Leg 1 id=%d, Leg 2 id=%d): %v", resp1.OrderID, resp2.OrderID, err3),
			Mode:         "LIVE",
			ExecutedAt:   time.Now(),
		}
		e.appendLog(res)
		return res
	}

	latency := time.Since(startExec).Milliseconds()
	actualProfitUSDT := opp.NetProfitUSDT
	e.cumulativePnL += actualProfitUSDT
	atomic.AddUint64(&e.totalTrades, 1)
	if actualProfitUSDT > 0 {
		atomic.AddUint64(&e.profitableTrades, 1)
	}

	res := ExecutionResult{
		Opportunity:            opp,
		IsSuccess:              true,
		ExecutionLatencyMs:     latency,
		ActualNetProfitUSDT:    actualProfitUSDT,
		WalletBalanceAfterUSDT: e.virtualWalletBalance,
		Mode:                   fmt.Sprintf("LIVE (L1=%d, L2=%d, L3=%d)", resp1.OrderID, resp2.OrderID, resp3.OrderID),
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
