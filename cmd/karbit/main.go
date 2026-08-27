package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"karbit/config"
	"karbit/internal/engine"
	"karbit/internal/exchange"
	"karbit/internal/graph"
	"karbit/internal/risk"
	"karbit/internal/ui"
)

func main() {
	startTime := time.Now()

	// 1. Load Configuration
	cfg, err := config.LoadConfig("config.json")
	if err != nil {
		fmt.Printf("Warning: failed to load config.json (%v), using defaults\n", err)
		cfg = config.DefaultConfig()
	}
	cfg.ParseFlags()

	fmt.Println("\033[36m[KArbit]\033[0m Initializing High-Frequency Binance Triangular Arbitrage Engine...")
	fmt.Printf("[KArbit] Base Asset: %s | Mode: %s | Capital: $%.2f | Workers: %d\n",
		cfg.BaseCurrency, cfg.TradingMode, cfg.TradeAmountUSDT, cfg.WorkerCount)

	// 2. Initialize Binance REST Client & Test Connectivity
	binanceClient := exchange.NewBinanceClient(cfg.BinanceBaseURL, cfg.BinanceAPIKey, cfg.BinanceAPISecret)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fmt.Print("[KArbit] Connecting to Binance REST API to fetch symbol filters... ")
	pingLat, err := binanceClient.Ping(ctx)
	if err != nil {
		fmt.Printf("\n\033[31m[Error]\033[0m Binance REST ping failed: %v\n", err)
	} else {
		fmt.Printf("OK (Ping: %v)\n", pingLat.Round(time.Millisecond))
	}

	// 3. Fetch Exchange Info and Parse Filters
	symbols, err := binanceClient.FetchExchangeInfo(ctx)
	if err != nil {
		fmt.Printf("\033[31m[Error]\033[0m Failed to fetch exchange metadata: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[KArbit] Successfully parsed %d active spot trading pairs from Binance.\n", len(symbols))

	// 4. Build Triangular Cycle Graph Index
	baseCurrencies := cfg.GetBaseCurrencies()
	fmt.Printf("[KArbit] Discovering 3-leg triangular paths for multi-base assets %v... ", baseCurrencies)
	graphIndex := graph.BuildMultiGraphIndex(symbols, baseCurrencies)
	fmt.Printf("Found %d valid triangles across %d symbols.\n",
		len(graphIndex.Triangles), graphIndex.UniqueSymbolsCount)

	if len(graphIndex.Triangles) == 0 {
		fmt.Printf("\033[31m[Error]\033[0m No triangular cycles found for base currencies %v\n", baseCurrencies)
		os.Exit(1)
	}

	if cfg.RadarDisplayLimit <= 0 {
		cfg.RadarDisplayLimit = 50
	}

	// Activate 100% of all discovered triangular paths (no artificial capping unless explicitly configured lower)
	activeGraph := graphIndex
	if cfg.MaxTrackedTriangles > 0 && cfg.MaxTrackedTriangles < len(graphIndex.Triangles) {
		activeGraph = graphIndex.GetActiveSubset(cfg.MaxTrackedTriangles)
	}
	fmt.Printf("[KArbit] 100%% Active Triangular Scanner Activated: %d Triangles across %d Symbols!\n",
		len(activeGraph.Triangles), activeGraph.UniqueSymbolsCount)

	// 5. Initialize Engine Components
	priceBook := engine.NewPriceBook()
	latencyGuard := engine.NewLatencyGuard(cfg.MaxLatencyMs)
	feeModel := risk.NewFeeModel(cfg.FeeRate, cfg.UseBNBDiscount)
	riskLimits := risk.RiskLimits{
		MaxDailyLossUSDT:     cfg.MaxDailyLossUSDT,
		MaxTradeAmountUSDT:   cfg.TradeAmountUSDT * 10,
		MaxSlippageTolerance: cfg.MaxSlippageTolerance,
	}
	riskManager := risk.NewRiskManager(riskLimits)
	evaluator := engine.NewArbEvaluator(feeModel, latencyGuard, cfg.MinProfitPercent)
	executor := engine.NewExecutor(cfg.TradingMode, cfg.TradeAmountUSDT, riskManager, binanceClient)

	requiredSymbols := make([]string, 0, len(activeGraph.AllRequiredSymbols))
	for sym := range activeGraph.AllRequiredSymbols {
		requiredSymbols = append(requiredSymbols, sym)
	}

	// 6. Setup High-Frequency Channels & Ingestion
	tickerChan := make(chan exchange.BookTickerEvent, 20000)
	wsClient := exchange.NewBinanceWSClient(cfg.BinanceWSURL, tickerChan)
	wsClient.SetSubscriptions(requiredSymbols)
	wsClient.Start(ctx)
	defer wsClient.Stop()

	// Metrics telemetry
	var (
		totalEvaluations   uint64
		evalsInSec         uint64
		evalsPerSecOutput  uint64
		topOpportunities   []*engine.ArbitrageOpportunity
		topOppMu           sync.RWMutex
		liveSpreadsMap     = make(map[string]*engine.ArbitrageOpportunity)
		liveSpreadsMu      sync.RWMutex
		liveAccountBalance float64
		liveUSDCBalance    float64
		liveBNBBalance     float64
		liveBalanceMu      sync.RWMutex
	)

	// Fetch initial Binance account balance on startup
	if binanceClient.HasCredentials() {
		acc, err := binanceClient.GetAccountInfo(ctx)
		if err == nil && acc != nil {
			liveBalanceMu.Lock()
			liveAccountBalance = acc.USDTBalance
			liveUSDCBalance = acc.USDCBalance
			liveBNBBalance = acc.BNBBalance
			liveBalanceMu.Unlock()
			fmt.Printf("[KArbit] Initial Live Spot USDT: $%.2f | USDC: $%.2f | BNB: %.4f\n", acc.USDTBalance, acc.USDCBalance, acc.BNBBalance)
		}
	}

	// Periodic Binance balance refresh routine (every 10 seconds)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if binanceClient.HasCredentials() {
					acc, err := binanceClient.GetAccountInfo(ctx)
					if err == nil && acc != nil {
						liveBalanceMu.Lock()
						liveAccountBalance = acc.USDTBalance
						liveUSDCBalance = acc.USDCBalance
						liveBNBBalance = acc.BNBBalance
						liveBalanceMu.Unlock()
					}
				}
			}
		}
	}()

	// Throughput calculation routine
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rate := atomic.SwapUint64(&evalsInSec, 0)
				atomic.StoreUint64(&evalsPerSecOutput, rate)
			}
		}
	}()

	// 7. Multi-Worker Arb Evaluation Pool
	evalTaskChan := make(chan *graph.Triangle, 200000)
	var workerWg sync.WaitGroup

	for i := 0; i < cfg.WorkerCount; i++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case tri, ok := <-evalTaskChan:
					if !ok {
						return
					}

					atomic.AddUint64(&totalEvaluations, 1)
					atomic.AddUint64(&evalsInSec, 1)

					// Atomic multi-leg snapshot
					quotes, valid := priceBook.GetPathSnapshot(tri.Symbols)
					if !valid {
						continue
					}

					nowMs := time.Now().UnixMilli()

					// Track real-time spread for radar display
					if rawOpp, ok := evaluator.EvaluateRaw(tri, quotes, cfg.TradeAmountUSDT, nowMs); ok {
						liveSpreadsMu.Lock()
						liveSpreadsMap[tri.ID] = rawOpp
						liveSpreadsMu.Unlock()
					}

					// Evaluate against execution threshold
					opp, found := evaluator.Evaluate(tri, quotes, cfg.TradeAmountUSDT, nowMs)
					if found {
						// Record to top opportunities list with deduplication
						topOppMu.Lock()
						foundIdx := -1
						for i, existing := range topOpportunities {
							if existing.Triangle != nil && existing.Triangle.ID == opp.Triangle.ID {
								foundIdx = i
								break
							}
						}
						if foundIdx >= 0 {
							topOpportunities[foundIdx] = opp
						} else {
							if len(topOpportunities) >= 20 {
								topOpportunities = topOpportunities[1:]
							}
							topOpportunities = append(topOpportunities, opp)
						}
						topOppMu.Unlock()

						// Execute in paper or live mode
						executor.Execute(ctx, opp)
					}
				}
			}
		}()
	}

	// 8. Ticker Dispatch Loop
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-tickerChan:
				if !ok {
					return
				}

				priceBook.Update(ev)

				// Find affected triangles
				affectedTriangles := activeGraph.SymbolToTriangles[ev.Symbol]
				for _, tri := range affectedTriangles {
					select {
					case evalTaskChan <- tri:
					default:
						// Non-blocking drop to prioritize ultra-fresh quotes over backpressure
					}
				}
			}
		}
	}()

	// 8b. Periodic Triangle Sweep (Populate & Refresh Radar Spreads every 200ms)
	go func() {
		sweepTicker := time.NewTicker(200 * time.Millisecond)
		defer sweepTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-sweepTicker.C:
				nowMs := time.Now().UnixMilli()
				for _, tri := range activeGraph.Triangles {
					quotes, valid := priceBook.GetPathSnapshot(tri.Symbols)
					if !valid {
						continue
					}
					if rawOpp, ok := evaluator.EvaluateRaw(tri, quotes, cfg.TradeAmountUSDT, nowMs); ok {
						liveSpreadsMu.Lock()
						liveSpreadsMap[tri.ID] = rawOpp
						liveSpreadsMu.Unlock()
					}
				}
			}
		}
	}()

	// 9. Web Server Setup & Dynamic Config Update Handler
	webServer := ui.NewWebServer(cfg.WebPort, "web", 
		func(u ui.WebConfigUpdate) error {
			if u.TradingMode != nil {
				cfg.TradingMode = *u.TradingMode
				executor.SetMode(*u.TradingMode)
			}
			if u.TradeAmountUSDT != nil {
				cfg.TradeAmountUSDT = *u.TradeAmountUSDT
				executor.SetCapital(*u.TradeAmountUSDT)
			}
			if u.MinProfitPercent != nil {
				cfg.MinProfitPercent = *u.MinProfitPercent
				evaluator.SetMinProfitPercent(*u.MinProfitPercent)
			}
			if u.MaxLatencyMs != nil {
				cfg.MaxLatencyMs = *u.MaxLatencyMs
				evaluator.SetMaxLatency(*u.MaxLatencyMs)
			}
			if u.MaxTrackedTriangles != nil && *u.MaxTrackedTriangles > 0 {
				cfg.MaxTrackedTriangles = *u.MaxTrackedTriangles
				activeGraph = graphIndex.GetActiveSubset(cfg.MaxTrackedTriangles)
			}
			if u.RadarDisplayLimit != nil && *u.RadarDisplayLimit > 0 {
				cfg.RadarDisplayLimit = *u.RadarDisplayLimit
			}
			if u.MaxSlippageTolerance != nil || u.MaxDailyLossUSDT != nil {
				if u.MaxSlippageTolerance != nil {
					cfg.MaxSlippageTolerance = *u.MaxSlippageTolerance
				}
				if u.MaxDailyLossUSDT != nil {
					cfg.MaxDailyLossUSDT = *u.MaxDailyLossUSDT
				}
				riskManager.UpdateLimits(cfg.MaxDailyLossUSDT, cfg.MaxSlippageTolerance, cfg.TradeAmountUSDT*10)
			}
			if u.FeeRate != nil || u.UseBNBDiscount != nil {
				if u.FeeRate != nil {
					cfg.FeeRate = *u.FeeRate
				}
				if u.UseBNBDiscount != nil {
					cfg.UseBNBDiscount = *u.UseBNBDiscount
				}
				newFeeModel := risk.NewFeeModel(cfg.FeeRate, cfg.UseBNBDiscount)
				evaluator.SetFeeModel(newFeeModel)
			}
			if u.BinanceAPIKey != nil && u.BinanceAPISecret != nil {
				cfg.BinanceAPIKey = *u.BinanceAPIKey
				cfg.BinanceAPISecret = *u.BinanceAPISecret
				binanceClient.SetCredentials(*u.BinanceAPIKey, *u.BinanceAPISecret)
			}

			// Persist all updated parameters into config.json
			_ = cfg.SaveToFile("config.json")
			return nil
		},
		func(apiKey, apiSecret string) (interface{}, error) {
			binanceClient.SetCredentials(apiKey, apiSecret)
			accInfo, err := binanceClient.GetAccountInfo(ctx)
			if err != nil {
				return nil, err
			}
			cfg.BinanceAPIKey = apiKey
			cfg.BinanceAPISecret = apiSecret
			_ = cfg.SaveToFile("config.json")
			liveBalanceMu.Lock()
			liveAccountBalance = accInfo.USDTBalance
			liveBalanceMu.Unlock()
			return accInfo, nil
		},
	)

	webServer.SetClearLogHandler(func() error {
		executor.ClearLogs()
		return nil
	})

	go func() {
		if err := webServer.Start(ctx); err != nil && err != http.ErrServerClosed {
			fmt.Printf("\033[31m[Web GUI Error]\033[0m %v\n", err)
		}
	}()

	// 10. Interactive Dashboard Render Loop
	refreshInterval := time.Duration(cfg.DashboardRefreshMs) * time.Millisecond
	if refreshInterval < 50*time.Millisecond {
		refreshInterval = 100 * time.Millisecond
	}

	go func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				walletBal, totalTr, profitTr, cumPnL, logs := executor.GetSummary()
				_, _, circuit := riskManager.GetStatus()

				now := time.Now()
				topOppMu.Lock()
				validOpps := make([]*engine.ArbitrageOpportunity, 0, len(topOpportunities))
				for _, o := range topOpportunities {
					if o != nil && now.Sub(o.Timestamp) < 15*time.Second {
						validOpps = append(validOpps, o)
					}
				}
				topOpportunities = validOpps
				copiedOpps := make([]*engine.ArbitrageOpportunity, len(topOpportunities))
				copy(copiedOpps, topOpportunities)
				topOppMu.Unlock()

				// Extract top live spreads
				liveSpreadsMu.RLock()
				allSpreads := make([]*engine.ArbitrageOpportunity, 0, len(liveSpreadsMap))
				for _, sp := range liveSpreadsMap {
					allSpreads = append(allSpreads, sp)
				}
				liveSpreadsMu.RUnlock()

				// Sort ALL active live spreads purely by NetProfitPercent descending (Real-Time Dynamic Profit Ranking)
				sort.Slice(allSpreads, func(i, j int) bool {
					return allSpreads[i].NetProfitPercent > allSpreads[j].NetProfitPercent
				})

				displayLimit := cfg.RadarDisplayLimit
				if displayLimit <= 0 {
					displayLimit = 100
				}
				if len(allSpreads) > displayLimit {
					allSpreads = allSpreads[:displayLimit]
				}

				liveBalanceMu.RLock()
				curLiveBal := liveAccountBalance
				curLiveUSDCBal := liveUSDCBalance
				curLiveBNBBal := liveBNBBalance
				liveBalanceMu.RUnlock()

				baseLabel := strings.Join(cfg.GetBaseCurrencies(), "+")

				dashData := ui.DashboardData{
					StartTime:            startTime,
					TradingMode:          cfg.TradingMode,
					BaseCurrency:         baseLabel,
					TradeAmountUSDT:      cfg.TradeAmountUSDT,
					MinProfitPercent:     cfg.MinProfitPercent,
					FeeRate:              cfg.FeeRate,
					UseBNBDiscount:       cfg.UseBNBDiscount,
					MaxLatencyMs:         cfg.MaxLatencyMs,
					MaxTrackedTriangles:  cfg.MaxTrackedTriangles,
					RadarDisplayLimit:    cfg.RadarDisplayLimit,
					MaxSlippageTolerance: cfg.MaxSlippageTolerance,
					MaxDailyLossUSDT:     cfg.MaxDailyLossUSDT,
					WSStats:              wsClient.GetStats(),
					TotalTriangles:       len(activeGraph.Triangles),
					TotalSymbols:         activeGraph.UniqueSymbolsCount,
					TotalEvaluations:     atomic.LoadUint64(&totalEvaluations),
					EvaluationsPerSec:    atomic.LoadUint64(&evalsPerSecOutput),
					WalletBalance:        walletBal,
					LiveAccountBalance:   curLiveBal,
					LiveUSDCBalance:      curLiveUSDCBal,
					LiveBNBBalance:       curLiveBNBBal,
					HasLiveAPIKeys:       binanceClient.HasCredentials(),
					TotalTrades:          totalTr,
					ProfitableTrades:     profitTr,
					CumulativePnL:        cumPnL,
					CircuitBreaker:       circuit,
					TopOpportunities:     copiedOpps,
					LiveSpreads:          allSpreads,
					RecentExecutionLog:   logs,
				}

				webServer.UpdateDashboardData(dashData)
				ui.RenderDashboard(dashData)
			}
		}
	}()

	// 11. Graceful Shutdown Handler
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan
	fmt.Println("\n\n\033[33m[KArbit]\033[0m Initiating graceful shutdown...")
	cancel()
	close(evalTaskChan)
	workerWg.Wait()

	walletBal, totalTr, profitTr, cumPnL, _ := executor.GetSummary()
	fmt.Println("\033[36m===================================================\033[0m")
	fmt.Println("             KArbit Final Session Summary          ")
	fmt.Println("\033[36m===================================================\033[0m")
	fmt.Printf(" Runtime Duration     : %s\n", time.Since(startTime).Round(time.Second))
	fmt.Printf(" Total Ingested Ticks : %d\n", wsClient.GetStats().TotalMessagesReceived)
	fmt.Printf(" Total Evaluations    : %d\n", atomic.LoadUint64(&totalEvaluations))
	fmt.Printf(" Total Executed Trades: %d (Win: %d)\n", totalTr, profitTr)
	fmt.Printf(" Final Wallet Balance : $%.4f USDT\n", walletBal)
	fmt.Printf(" Cumulative Session PnL: $%.4f USDT\n", cumPnL)
	fmt.Println("\033[32m[KArbit] Shutdown complete. Goodbye!\033[0m")
}
