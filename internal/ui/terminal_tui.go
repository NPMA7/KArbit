package ui

import (
	"fmt"
	"strings"
	"time"

	"karbit/internal/engine"
	"karbit/internal/exchange"
	"karbit/internal/graph"
)

const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorPurple = "\033[35m"
	ColorCyan   = "\033[36m"
	ColorWhite  = "\033[37m"
	ColorBold   = "\033[1m"
	ClearScreen = "\033[H\033[2J"
)

// DashboardData encapsulates all telemetry required to render the TUI and Web GUI.
type DashboardData struct {
	StartTime          time.Time                       `json:"start_time"`
	TradingMode        string                          `json:"trading_mode"`
	BaseCurrency       string                          `json:"base_currency"`
	TradeAmountUSDT    float64                         `json:"trade_amount_usdt"`
	MinProfitPercent   float64                         `json:"min_profit_percent"`
	FeeRate              float64                         `json:"fee_rate"`
	UseBNBDiscount       bool                            `json:"use_bnb_discount"`
	MaxLatencyMs         int64                           `json:"max_latency_ms"`
	MaxTrackedTriangles  int                             `json:"max_tracked_triangles"`
	RadarDisplayLimit    int                             `json:"radar_display_limit"`
	MaxSlippageTolerance float64                         `json:"max_slippage_tolerance"`
	MaxDailyLossUSDT     float64                         `json:"max_daily_loss_usdt"`
	WSStats              exchange.WebSocketStats         `json:"ws_stats"`
	GraphIndex         *graph.GraphIndex               `json:"-"` // Omit large graph from 100ms WebSocket stream
	TotalTriangles     int                             `json:"total_triangles"`
	TotalSymbols       int                             `json:"total_symbols"`
	TotalEvaluations   uint64                          `json:"total_evaluations"`
	EvaluationsPerSec  uint64                          `json:"evaluations_per_sec"`
	WalletBalance      float64                         `json:"wallet_balance"`
	LiveAccountBalance float64                         `json:"live_account_balance"`
	HasLiveAPIKeys     bool                            `json:"has_live_api_keys"`
	TotalTrades        uint64                          `json:"total_trades"`
	ProfitableTrades   uint64                          `json:"profitable_trades"`
	CumulativePnL      float64                         `json:"cumulative_pnl"`
	CircuitBreaker     bool                            `json:"circuit_breaker"`
	TopOpportunities   []*engine.ArbitrageOpportunity  `json:"top_opportunities"`
	LiveSpreads        []*engine.ArbitrageOpportunity  `json:"live_spreads"`
	RecentExecutionLog []engine.ExecutionResult        `json:"recent_execution_log"`
}

// RenderDashboard prints an ANSI terminal dashboard.
func RenderDashboard(d DashboardData) {
	uptime := time.Since(d.StartTime).Round(time.Second)

	var sb strings.Builder
	sb.WriteString(ClearScreen)

	// Header Banner
	sb.WriteString(ColorCyan + ColorBold)
	sb.WriteString("====================================================================================================\n")
	sb.WriteString("          KArbit: High-Frequency Binance Triangular Arbitrage Engine (HFT Spot v1.0)               \n")
	sb.WriteString("====================================================================================================\n" + ColorReset)

	// Mode and Connection Bar
	modeColor := ColorGreen
	if strings.ToUpper(d.TradingMode) == "LIVE" {
		modeColor = ColorRed + ColorBold
	}

	wsStatus := ColorGreen + "● CONNECTED" + ColorReset
	if !d.WSStats.IsConnected && (d.WSStats.LastMessageTime.IsZero() || time.Since(d.WSStats.LastMessageTime) > 3*time.Second) {
		wsStatus = ColorRed + "○ DISCONNECTED" + ColorReset
	}

	pingColor := ColorGreen
	if d.WSStats.LastPingLatencyMs > 50 {
		pingColor = ColorYellow
	}
	if d.WSStats.LastPingLatencyMs > 100 {
		pingColor = ColorRed
	}

	sb.WriteString(fmt.Sprintf(" Mode: %s[%s]%s | WS Stream: %s | Latency: %s%d ms%s | Uptime: %s\n",
		modeColor, strings.ToUpper(d.TradingMode), ColorReset,
		wsStatus,
		pingColor, d.WSStats.LastPingLatencyMs, ColorReset,
		uptime.String(),
	))
	sb.WriteString(ColorCyan + "----------------------------------------------------------------------------------------------------\n" + ColorReset)

	// Engine & Market Stats
	feeText := fmt.Sprintf("%.3f%%", d.FeeRate*100)
	if d.UseBNBDiscount {
		feeText += " (BNB Discount ON)"
	}

	uniquePairs := d.TotalSymbols
	totalTriangles := d.TotalTriangles
	if d.GraphIndex != nil {
		uniquePairs = d.GraphIndex.UniqueSymbolsCount
		totalTriangles = len(d.GraphIndex.Triangles)
	}

	sb.WriteString(fmt.Sprintf(" Base Asset: %s%-6s%s | Triangles Tracked: %s%d%s (%d active spot pairs)\n",
		ColorBold, d.BaseCurrency, ColorReset,
		ColorBold, totalTriangles, ColorReset,
		uniquePairs,
	))
	sb.WriteString(fmt.Sprintf(" WS Ingestion: %s%d msgs/sec%s | Total Ingested: %d ticks\n",
		ColorYellow, d.WSStats.MessagesPerSecond, ColorReset,
		d.WSStats.TotalMessagesReceived,
	))
	sb.WriteString(fmt.Sprintf(" Arb Throughput: %s%d evals/sec%s | Total Evaluated: %d\n",
		ColorCyan, d.EvaluationsPerSec, ColorReset,
		d.TotalEvaluations,
	))
	sb.WriteString(fmt.Sprintf(" Risk & Fee Config: Min Profit: %s>= +%.2f%%%s | Fee/Leg: %s | Max Latency: %d ms\n",
		ColorGreen, d.MinProfitPercent, ColorReset,
		feeText, d.MaxLatencyMs,
	))
	sb.WriteString(ColorCyan + "----------------------------------------------------------------------------------------------------\n" + ColorReset)

	// Financial Performance Box
	pnlColor := ColorGreen
	pnlSign := "+"
	if d.CumulativePnL < 0 {
		pnlColor = ColorRed
		pnlSign = ""
	}

	winRate := 0.0
	if d.TotalTrades > 0 {
		winRate = (float64(d.ProfitableTrades) / float64(d.TotalTrades)) * 100.0
	}

	circuitText := ColorGreen + "SAFE" + ColorReset
	if d.CircuitBreaker {
		circuitText = ColorRed + ColorBold + "TRIPPED (Loss Limit Hit)" + ColorReset
	}

	sb.WriteString(fmt.Sprintf(" Capital/Trade: %s$%.2f%s | Wallet: %s$%.2f%s | Cumulative PnL: %s%s$%.4f%s\n",
		ColorBold, d.TradeAmountUSDT, ColorReset,
		ColorBold, d.WalletBalance, ColorReset,
		pnlColor+ColorBold, pnlSign, d.CumulativePnL, ColorReset,
	))
	sb.WriteString(fmt.Sprintf(" Total Trades: %s%d%s | Profitable: %s%d%s | Win Rate: %s%.1f%%%s | Circuit Breaker: %s\n",
		ColorBold, d.TotalTrades, ColorReset,
		ColorGreen, d.ProfitableTrades, ColorReset,
		ColorYellow, winRate, ColorReset,
		circuitText,
	))
	sb.WriteString(ColorCyan + "====================================================================================================\n" + ColorReset)

	// Live Opportunities Table
	sb.WriteString(ColorBold + ColorYellow + "▶ LIVE ARBITRAGE OPPORTUNITIES DETECTED\n" + ColorReset)
	sb.WriteString(fmt.Sprintf(" %-36s | %-9s | %-9s | %-10s | %-8s | %-6s\n", "TRIANGULAR PATH", "GROSS %", "NET %", "NET PNL ($)", "SLIPPAGE", "AGE"))
	sb.WriteString("-------------------------------------+-----------+-----------+------------+----------+-------\n")

	if len(d.TopOpportunities) == 0 {
		sb.WriteString("  Scanning real-time order books... No active mispricing above profit threshold.\n")
	} else {
		for i, opp := range d.TopOpportunities {
			if i >= 6 {
				break
			}
			pathStr := opp.Triangle.ID
			if len(pathStr) > 36 {
				pathStr = pathStr[:33] + "..."
			}

			netColor := ColorGreen
			if opp.NetProfitPercent < 0 {
				netColor = ColorRed
			}

			sb.WriteString(fmt.Sprintf(" %-36s | %7.3f%%  | %s%7.3f%%%s | %s%s$%-7.4f%s | %6.4f%%  | %3dms\n",
				pathStr,
				opp.GrossProfitPercent,
				netColor+ColorBold, opp.NetProfitPercent, ColorReset,
				netColor, pnlSign, opp.NetProfitUSDT, ColorReset,
				opp.EstimatedSlippage*100,
				opp.LatencyMs,
			))
		}
	}

	sb.WriteString(ColorCyan + "----------------------------------------------------------------------------------------------------\n" + ColorReset)

	// Recent Execution Log
	sb.WriteString(ColorBold + ColorGreen + "▶ RECENT EXECUTIONS (SIMULATED & LIVE LOG)\n" + ColorReset)
	sb.WriteString(fmt.Sprintf(" %-8s | %-32s | %-8s | %-10s | %-8s | %s\n", "TIME", "PATH", "NET %", "NET PROFIT", "LATENCY", "STATUS / MODE"))
	sb.WriteString("---------+----------------------------------+----------+------------+----------+--------------------\n")

	if len(d.RecentExecutionLog) == 0 {
		sb.WriteString("  No executions yet. Waiting for profitable opportunities exceeding threshold.\n")
	} else {
		// Print in reverse order (newest first)
		count := 0
		for i := len(d.RecentExecutionLog) - 1; i >= 0 && count < 6; i-- {
			log := d.RecentExecutionLog[i]
			timeStr := log.ExecutedAt.Format("15:04:05")
			pathStr := log.Opportunity.Triangle.ID
			if len(pathStr) > 32 {
				pathStr = pathStr[:29] + "..."
			}

			statusStr := ColorGreen + log.Mode + ColorReset
			if !log.IsSuccess {
				statusStr = ColorRed + log.ErrorMessage + ColorReset
			}

			profitColor := ColorGreen
			if log.ActualNetProfitUSDT < 0 {
				profitColor = ColorRed
			}

			sb.WriteString(fmt.Sprintf(" %-8s | %-32s | %s%6.3f%%%s | %s$%-9.4f%s | %5d ms  | %s\n",
				timeStr,
				pathStr,
				profitColor, log.Opportunity.NetProfitPercent, ColorReset,
				profitColor, log.ActualNetProfitUSDT, ColorReset,
				log.ExecutionLatencyMs,
				statusStr,
			))
			count++
		}
	}

	sb.WriteString(ColorCyan + "====================================================================================================\n" + ColorReset)
	sb.WriteString(ColorWhite + " [Ctrl+C] Stop Engine | [Data Source] Binance Public BookTicker WebSocket Stream\n" + ColorReset)

	fmt.Print(sb.String())
}
