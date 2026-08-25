package exchange

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// BinanceClient manages REST interactions with Binance API.
type BinanceClient struct {
	baseURL    string
	apiKey     string
	apiSecret  string
	httpClient *http.Client
}

// NewBinanceClient creates a client configured with optimized HTTP/2 connection pooling.
func NewBinanceClient(baseURL, apiKey, apiSecret string) *BinanceClient {
	if baseURL == "" {
		baseURL = "https://api.binance.com"
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // Handle environments with local clock skew
		},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 50,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
		DisableCompression:  false,
		ForceAttemptHTTP2:   true,
	}

	return &BinanceClient{
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiKey:    apiKey,
		apiSecret: apiSecret,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
		},
	}
}

// Ping measures round-trip REST latency to Binance server.
func (c *BinanceClient) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v3/ping", nil)
	if err != nil {
		return 0, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("ping returned status %d", resp.StatusCode)
	}

	return time.Since(start), nil
}

// FetchExchangeInfo retrieves and parses all spot symbol metadata and filters.
func (c *BinanceClient) FetchExchangeInfo(ctx context.Context) ([]ParsedSymbol, error) {
	reqURL := c.baseURL + "/api/v3/exchangeInfo"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch exchangeInfo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("exchangeInfo returned status %d: %s", resp.StatusCode, string(body))
	}

	var rawInfo ExchangeInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&rawInfo); err != nil {
		return nil, fmt.Errorf("failed to decode exchangeInfo: %w", err)
	}

	parsedSymbols := make([]ParsedSymbol, 0, len(rawInfo.Symbols))
	for _, rawSym := range rawInfo.Symbols {
		// Only consider trading spot symbols
		if rawSym.Status != "TRADING" || !rawSym.IsSpotTradingAllowed {
			continue
		}
		ps := rawSym.Parse()
		parsedSymbols = append(parsedSymbols, ps)
	}

	return parsedSymbols, nil
}

// sign creates an HMAC-SHA256 signature for Binance authenticated endpoints.
func (c *BinanceClient) sign(query string) string {
	mac := hmac.New(sha256.New, []byte(c.apiSecret))
	mac.Write([]byte(query))
	return hex.EncodeToString(mac.Sum(nil))
}

// CreateOrder places a live order on Binance (IOC/FOK/LIMIT).
func (c *BinanceClient) CreateOrder(ctx context.Context, ord OrderRequest) (*OrderResponse, error) {
	if c.apiKey == "" || c.apiSecret == "" {
		return nil, fmt.Errorf("API key and secret required for live orders")
	}

	endpoint := c.baseURL + "/api/v3/order"

	params := url.Values{}
	params.Set("symbol", ord.Symbol)
	params.Set("side", string(ord.Side))
	params.Set("type", string(ord.Type))
	params.Set("quantity", strconv.FormatFloat(ord.Quantity, 'f', -1, 64))
	if ord.Price > 0 {
		params.Set("price", strconv.FormatFloat(ord.Price, 'f', -1, 64))
	}
	if ord.TimeInForce != "" {
		params.Set("timeInForce", string(ord.TimeInForce))
	}
	params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))

	queryStr := params.Encode()
	signature := c.sign(queryStr)
	fullURL := fmt.Sprintf("%s?%s&signature=%s", endpoint, queryStr, signature)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-MBX-APIKEY", c.apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("order execution failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance order rejected (status %d): %s", resp.StatusCode, string(body))
	}

	var ordResp OrderResponse
	if err := json.Unmarshal(body, &ordResp); err != nil {
		return nil, fmt.Errorf("failed to parse order response: %w", err)
	}

	return &ordResp, nil
}

// SetCredentials updates the API key and secret dynamically.
func (c *BinanceClient) SetCredentials(apiKey, apiSecret string) {
	c.apiKey = strings.TrimSpace(apiKey)
	c.apiSecret = strings.TrimSpace(apiSecret)
}

// HasCredentials returns true if API key and secret are configured.
func (c *BinanceClient) HasCredentials() bool {
	return c.apiKey != "" && c.apiSecret != ""
}

// AccountBalance represents asset balance from Binance.
type AccountBalance struct {
	Asset  string `json:"asset"`
	Free   string `json:"free"`
	Locked string `json:"locked"`
}

// AccountInfo represents user account details from /api/v3/account.
type AccountInfo struct {
	CanTrade     bool             `json:"canTrade"`
	CanWithdraw  bool             `json:"canWithdraw"`
	CanDeposit   bool             `json:"canDeposit"`
	AccountType  string           `json:"accountType"`
	Balances     []AccountBalance `json:"balances"`
	USDTBalance  float64          `json:"-"`
	BNBBalance   float64          `json:"-"`
}

// GetAccountInfo verifies credentials and fetches account balance and trading permissions.
func (c *BinanceClient) GetAccountInfo(ctx context.Context) (*AccountInfo, error) {
	if !c.HasCredentials() {
		return nil, fmt.Errorf("Binance API Key and Secret are not configured")
	}

	endpoint := c.baseURL + "/api/v3/account"
	params := url.Values{}
	params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))

	queryStr := params.Encode()
	signature := c.sign(queryStr)
	fullURL := fmt.Sprintf("%s?%s&signature=%s", endpoint, queryStr, signature)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-MBX-APIKEY", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach Binance: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Binance API rejected authentication (status %d): %s", resp.StatusCode, string(body))
	}

	var info AccountInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("failed to decode account info: %w", err)
	}

	for _, b := range info.Balances {
		if b.Asset == "USDT" {
			info.USDTBalance, _ = strconv.ParseFloat(b.Free, 64)
		} else if b.Asset == "BNB" {
			info.BNBBalance, _ = strconv.ParseFloat(b.Free, 64)
		}
	}

	return &info, nil
}
