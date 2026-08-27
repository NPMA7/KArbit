package config

import (
	"encoding/json"
	"flag"
	"os"
	"runtime"
	"strings"
)

// Config represents all operational parameters for the KArbit HFT engine.
type Config struct {
	// Base Currency for triangular cycles (default "USDT")
	BaseCurrency string `json:"base_currency"`

	// Trading Mode: "paper" (simulation) or "live" (real Binance orders)
	TradingMode string `json:"trading_mode"`

	// Trade capital per triangular arbitrage cycle in USDT (default 100.0)
	TradeAmountUSDT float64 `json:"trade_amount_usdt"`

	// Minimum net profit threshold in percent after all 3-leg fees (e.g., 0.05 = +0.05%)
	MinProfitPercent float64 `json:"min_profit_percent"`

	// Taker fee rate per leg (e.g. 0.00075 = 0.075% with BNB discount, 0.001 = 0.1% standard)
	FeeRate float64 `json:"fee_rate"`

	// Whether BNB fee discount is applied
	UseBNBDiscount bool `json:"use_bnb_discount"`

	// Maximum allowable quote age / network delay in milliseconds before discarding (default 200ms)
	MaxLatencyMs int64 `json:"max_latency_ms"`

	// Maximum allowable slippage tolerance (e.g., 0.001 = 0.1%)
	MaxSlippageTolerance float64 `json:"max_slippage_tolerance"`

	// Maximum daily loss limit in USDT for circuit breaker
	MaxDailyLossUSDT float64 `json:"max_daily_loss_usdt"`

	// Number of triangular paths to track and evaluate concurrently (e.g. 350)
	MaxTrackedTriangles int `json:"max_tracked_triangles"`

	// Maximum number of active routes to display in the Radar table (e.g. 50)
	RadarDisplayLimit int `json:"radar_display_limit"`

	// Number of concurrent evaluator workers
	WorkerCount int `json:"worker_count"`

	// Binance API Credentials (required only for live trading)
	BinanceAPIKey    string `json:"binance_api_key"`
	BinanceAPISecret string `json:"binance_api_secret"`

	// Binance Endpoints
	BinanceBaseURL string `json:"binance_base_url"`
	BinanceWSURL   string `json:"binance_ws_url"`

	// Dashboard and Web GUI
	WebPort            int  `json:"web_port"`
	DashboardRefreshMs int  `json:"dashboard_refresh_ms"`
	LogOpportunities   bool `json:"log_opportunities"`
}

// DefaultConfig returns safe and optimized default settings.
func DefaultConfig() *Config {
	workers := runtime.NumCPU()
	if workers < 2 {
		workers = 2
	}

	return &Config{
		BaseCurrency:         "USDT",
		TradingMode:          "paper",
		TradeAmountUSDT:      100.0,
		MinProfitPercent:     0.05,
		FeeRate:              0.00075, // 0.075% taker fee with BNB discount
		UseBNBDiscount:       true,
		MaxLatencyMs:         200,
		MaxSlippageTolerance: 0.001,
		MaxDailyLossUSDT:     50.0,
		MaxTrackedTriangles:  350,
		RadarDisplayLimit:    50,
		WorkerCount:          workers,
		BinanceAPIKey:        "",
		BinanceAPISecret:     "",
		BinanceBaseURL:       "https://data-api.binance.vision",
		BinanceWSURL:         "wss://data-stream.binance.vision/ws",
		WebPort:              8080,
		DashboardRefreshMs:   100,
		LogOpportunities:     true,
	}
}

// LoadConfig loads configuration from a JSON file and .env file, and overrides with command-line flags if provided.
func LoadConfig(configPath string) (*Config, error) {
	cfg := DefaultConfig()

	if configPath != "" {
		if fileData, err := os.ReadFile(configPath); err == nil {
			if err := json.Unmarshal(fileData, cfg); err != nil {
				return nil, err
			}
		}
	}

	// Read from .env file if present
	if envData, err := os.ReadFile(".env"); err == nil {
		lines := strings.Split(string(envData), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				k := strings.TrimSpace(parts[0])
				v := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
				switch k {
				case "BINANCE_API_KEY":
					if v != "" {
						cfg.BinanceAPIKey = v
					}
				case "BINANCE_API_SECRET":
					if v != "" {
						cfg.BinanceAPISecret = v
					}
				case "BINANCE_BASE_URL":
					if v != "" {
						cfg.BinanceBaseURL = v
					}
				case "BINANCE_WS_URL":
					if v != "" {
						cfg.BinanceWSURL = v
					}
				case "KARBIT_MODE":
					if v != "" {
						cfg.TradingMode = v
					}
				}
			}
		}
	}

	// Environment variable overrides
	if envKey := os.Getenv("BINANCE_API_KEY"); envKey != "" {
		cfg.BinanceAPIKey = envKey
	}
	if envSec := os.Getenv("BINANCE_API_SECRET"); envSec != "" {
		cfg.BinanceAPISecret = envSec
	}
	if envMode := os.Getenv("KARBIT_MODE"); envMode != "" {
		cfg.TradingMode = envMode
	}

	return cfg, nil
}

// SaveToFile writes current configuration parameters to a JSON file on disk.
func (c *Config) SaveToFile(configPath string) error {
	if configPath == "" {
		configPath = "config.json"
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0644)
}

// ParseFlags allows overriding configuration through CLI arguments.
func (c *Config) ParseFlags() {
	var (
		mode         = flag.String("mode", c.TradingMode, "Trading mode: 'paper' or 'live'")
		capital      = flag.Float64("capital", c.TradeAmountUSDT, "Trade amount in USDT per cycle")
		minProfit    = flag.Float64("min-profit", c.MinProfitPercent, "Minimum net profit percentage threshold (e.g. 0.05)")
		feeRate      = flag.Float64("fee", c.FeeRate, "Taker fee rate per leg (e.g. 0.00075)")
		maxLatency   = flag.Int64("max-latency", c.MaxLatencyMs, "Max allowable quote latency in ms")
		baseCurrency = flag.String("base", c.BaseCurrency, "Base currency asset (e.g. USDT, FDUSD, USDC)")
		webPort      = flag.Int("web-port", c.WebPort, "HTTP Web GUI dashboard port (default 8080)")
	)

	flag.Parse()

	c.TradingMode = *mode
	c.TradeAmountUSDT = *capital
	c.MinProfitPercent = *minProfit
	c.FeeRate = *feeRate
	c.MaxLatencyMs = *maxLatency
	c.BaseCurrency = *baseCurrency
	c.WebPort = *webPort
}
