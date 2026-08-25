package risk

import (
	"testing"
)

func TestRiskManager_CircuitBreaker(t *testing.T) {
	limits := RiskLimits{
		MaxDailyLossUSDT:     20.0,
		MaxTradeAmountUSDT:   200.0,
		MaxSlippageTolerance: 0.005, // 0.5%
	}

	rm := NewRiskManager(limits)

	// Trade amount exceeding max limit
	err := rm.CheckSafety(250.0, 0.001)
	if err == nil {
		t.Errorf("expected error for exceeding max trade amount")
	}

	// Slippage exceeding tolerance
	err = rm.CheckSafety(100.0, 0.01) // 1.0% > 0.5%
	if err == nil {
		t.Errorf("expected error for excessive slippage")
	}

	// Normal check
	err = rm.CheckSafety(100.0, 0.001)
	if err != nil {
		t.Fatalf("unexpected error on safe trade: %v", err)
	}

	// Simulate losing trades that trigger circuit breaker
	rm.RecordTrade(-12.0)
	_, dailyLoss, tripped := rm.GetStatus()
	if tripped || dailyLoss != 12.0 {
		t.Errorf("circuit breaker should not trip yet (loss=$12 < limit=$20)")
	}

	rm.RecordTrade(-10.0) // Total loss = $22 >= $20
	_, _, tripped = rm.GetStatus()
	if !tripped {
		t.Errorf("circuit breaker SHOULD have tripped (loss=$22 >= limit=$20)")
	}

	// Now subsequent check should fail with circuit breaker error
	err = rm.CheckSafety(50.0, 0.001)
	if err == nil {
		t.Errorf("expected error due to active circuit breaker")
	}
}
