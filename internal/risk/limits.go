package risk

import (
	"fmt"
	"sync"
	"time"
)

// RiskLimits configures safety circuit breakers.
type RiskLimits struct {
	MaxDailyLossUSDT     float64
	MaxTradeAmountUSDT   float64
	MaxSlippageTolerance float64
}

// RiskManager enforces position limits and maximum loss safety checks.
type RiskManager struct {
	mu                   sync.RWMutex
	limits               RiskLimits
	currentDayLossUSDT   float64
	currentDayProfitUSDT float64
	lastResetDate        string
	circuitBreaker       bool
}

// NewRiskManager initializes a risk manager with limits.
func NewRiskManager(limits RiskLimits) *RiskManager {
	return &RiskManager{
		limits:        limits,
		lastResetDate: time.Now().Format("2006-01-02"),
	}
}

// CheckSafety verifies if an execution is permitted under risk rules.
func (rm *RiskManager) CheckSafety(tradeAmountUSDT float64, estimatedSlippage float64) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.checkDailyReset()

	if rm.circuitBreaker {
		return fmt.Errorf("circuit breaker active: daily loss limit exceeded ($%.2f)", rm.limits.MaxDailyLossUSDT)
	}

	if tradeAmountUSDT > rm.limits.MaxTradeAmountUSDT && rm.limits.MaxTradeAmountUSDT > 0 {
		return fmt.Errorf("trade amount $%.2f exceeds maximum permitted $%.2f", tradeAmountUSDT, rm.limits.MaxTradeAmountUSDT)
	}

	if estimatedSlippage > rm.limits.MaxSlippageTolerance && rm.limits.MaxSlippageTolerance > 0 {
		return fmt.Errorf("estimated slippage %.4f%% exceeds tolerance %.4f%%", estimatedSlippage*100, rm.limits.MaxSlippageTolerance*100)
	}

	return nil
}

// RecordTrade updates daily PnL and triggers circuit breaker if loss exceeds limit.
func (rm *RiskManager) RecordTrade(netProfitUSDT float64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.checkDailyReset()

	if netProfitUSDT >= 0 {
		rm.currentDayProfitUSDT += netProfitUSDT
	} else {
		loss := -netProfitUSDT
		rm.currentDayLossUSDT += loss
		if rm.limits.MaxDailyLossUSDT > 0 && rm.currentDayLossUSDT >= rm.limits.MaxDailyLossUSDT {
			rm.circuitBreaker = true
		}
	}
}

func (rm *RiskManager) checkDailyReset() {
	today := time.Now().Format("2006-01-02")
	if today != rm.lastResetDate {
		rm.lastResetDate = today
		rm.currentDayLossUSDT = 0
		rm.currentDayProfitUSDT = 0
		rm.circuitBreaker = false
	}
}

// GetStatus returns risk and PnL metrics.
func (rm *RiskManager) GetStatus() (dailyProfit float64, dailyLoss float64, circuitBreaker bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.currentDayProfitUSDT, rm.currentDayLossUSDT, rm.circuitBreaker
}

// UpdateLimits dynamically updates risk limits.
func (rm *RiskManager) UpdateLimits(maxDailyLoss float64, maxSlippage float64, maxTradeAmount float64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if maxDailyLoss > 0 {
		rm.limits.MaxDailyLossUSDT = maxDailyLoss
	}
	if maxSlippage > 0 {
		rm.limits.MaxSlippageTolerance = maxSlippage
	}
	if maxTradeAmount > 0 {
		rm.limits.MaxTradeAmountUSDT = maxTradeAmount
	}
}
