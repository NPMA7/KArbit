package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WebConfigUpdate represents the payload to update runtime configuration.
type WebConfigUpdate struct {
	TradingMode          *string  `json:"trading_mode,omitempty"`
	TradeAmountUSDT      *float64 `json:"trade_amount_usdt,omitempty"`
	MinProfitPercent     *float64 `json:"min_profit_percent,omitempty"`
	FeeRate              *float64 `json:"fee_rate,omitempty"`
	UseBNBDiscount       *bool    `json:"use_bnb_discount,omitempty"`
	MaxLatencyMs         *int64   `json:"max_latency_ms,omitempty"`
	MaxTrackedTriangles  *int     `json:"max_tracked_triangles,omitempty"`
	RadarDisplayLimit    *int     `json:"radar_display_limit,omitempty"`
	MaxSlippageTolerance *float64 `json:"max_slippage_tolerance,omitempty"`
	MaxDailyLossUSDT     *float64 `json:"max_daily_loss_usdt,omitempty"`
	BinanceAPIKey        *string  `json:"binance_api_key,omitempty"`
	BinanceAPISecret     *string  `json:"binance_api_secret,omitempty"`
	SecurityPIN          *string  `json:"security_pin,omitempty"`
}

// WebServer manages the HTTP and WebSocket endpoints for the browser dashboard.
type WebServer struct {
	port       int
	webDir     string
	clients    map[*websocket.Conn]bool
	clientsMu  sync.RWMutex
	upgrader   websocket.Upgrader
	latestData DashboardData
	dataMu          sync.RWMutex
	onUpdate        func(update WebConfigUpdate) error
	onTestAuth      func(apiKey, apiSecret string) (interface{}, error)
	onClearLog      func() error
	server          *http.Server
}

// NewWebServer initializes the Web GUI server.
func NewWebServer(port int, webDir string, onUpdate func(update WebConfigUpdate) error, onTestAuth func(apiKey, apiSecret string) (interface{}, error)) *WebServer {
	if port <= 0 {
		port = 8080
	}
	if webDir == "" {
		webDir = "web"
	}

	wsUpgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow local browser connections
		},
		ReadBufferSize:  2048,
		WriteBufferSize: 128 * 1024,
	}

	return &WebServer{
		port:       port,
		webDir:     webDir,
		clients:    make(map[*websocket.Conn]bool),
		upgrader:   wsUpgrader,
		onUpdate:   onUpdate,
		onTestAuth: onTestAuth,
	}
}

// SetClearLogHandler registers a callback for clearing trade history logs.
func (ws *WebServer) SetClearLogHandler(fn func() error) {
	ws.onClearLog = fn
}

// UpdateDashboardData updates the current snapshot to be broadcast to web clients.
func (ws *WebServer) UpdateDashboardData(d DashboardData) {
	ws.dataMu.Lock()
	ws.latestData = d
	ws.dataMu.Unlock()
}

// Start launches the HTTP server and client broadcast loop.
func (ws *WebServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Static Assets
	mux.HandleFunc("/", ws.handleIndex)
	mux.HandleFunc("/style.css", ws.handleStaticFile("style.css", "text/css"))
	mux.HandleFunc("/app.js", ws.handleStaticFile("app.js", "application/javascript"))
	mux.HandleFunc("/favicon.svg", ws.handleStaticFile("favicon.svg", "image/svg+xml"))
	mux.HandleFunc("/favicon.png", ws.handleStaticFile("favicon.png", "image/png"))
	mux.HandleFunc("/favicon-64.png", ws.handleStaticFile("favicon-64.png", "image/png"))
	mux.HandleFunc("/favicon.ico", ws.handleStaticFile("favicon.ico", "image/x-icon"))
	mux.HandleFunc("/logo.jpg", ws.handleStaticFile("logo.jpg", "image/jpeg"))

	// Real-Time WebSocket Streaming
	mux.HandleFunc("/ws/stream", ws.handleWSStream)

	// REST Control Endpoints
	mux.HandleFunc("/api/status", ws.handleAPIStatus)
	mux.HandleFunc("/api/config", ws.handleAPIConfig)
	mux.HandleFunc("/api/binance-auth", ws.handleBinanceAuth)
	mux.HandleFunc("/api/security/verify-pin", ws.handleVerifyPIN)
	mux.HandleFunc("/api/security/change-pin", ws.handleChangePIN)
	mux.HandleFunc("/api/clear-log", ws.handleClearLog)

	addr := fmt.Sprintf("0.0.0.0:%d", ws.port)
	ws.server = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	go ws.runBroadcastLoop(ctx)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		ws.server.Shutdown(shutdownCtx)
	}()

	fmt.Printf("[KArbit Web GUI] Dashboard available at: \033[32mhttp://localhost:%d\033[0m\n", ws.port)
	return ws.server.ListenAndServe()
}

func (ws *WebServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	indexPath := filepath.Join(ws.webDir, "index.html")
	http.ServeFile(w, r, indexPath)
}

func (ws *WebServer) handleStaticFile(filename, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filePath := filepath.Join(ws.webDir, filename)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		http.ServeFile(w, r, filePath)
	}
}

func (ws *WebServer) handleWSStream(w http.ResponseWriter, r *http.Request) {
	conn, err := ws.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	ws.clientsMu.Lock()
	ws.clients[conn] = true
	ws.clientsMu.Unlock()

	defer func() {
		ws.clientsMu.Lock()
		delete(ws.clients, conn)
		ws.clientsMu.Unlock()
		conn.Close()
	}()

	// Keep alive read loop (waits for client close/disconnect without artificial read timeouts)
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}

func (ws *WebServer) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	ws.dataMu.RLock()
	data := ws.latestData
	ws.dataMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// BinanceAuthRequest holds user credentials for live Binance trading.
type BinanceAuthRequest struct {
	APIKey      string `json:"api_key"`
	APISecret   string `json:"api_secret"`
	SecurityPIN string `json:"security_pin"`
}

// RateLimiter protects sensitive endpoints from PIN brute-force attacks.
type RateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	lockouts map[string]time.Time
}

var globalLimiter = &RateLimiter{
	attempts: make(map[string][]time.Time),
	lockouts: make(map[string]time.Time),
}

func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("CF-Connecting-IP"); xff != "" {
		return strings.TrimSpace(xff)
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func checkAndVerifyPIN(r *http.Request, providedPIN string) error {
	ip := getClientIP(r)
	now := time.Now()

	globalLimiter.mu.Lock()
	defer globalLimiter.mu.Unlock()

	// Check if IP is currently locked out
	if unlockTime, locked := globalLimiter.lockouts[ip]; locked {
		if now.Before(unlockTime) {
			remaining := unlockTime.Sub(now).Round(time.Second)
			return fmt.Errorf("Terlalu banyak percobaan PIN gagal. Akses IP diblokir sementara selama %v", remaining)
		}
		delete(globalLimiter.lockouts, ip)
		delete(globalLimiter.attempts, ip)
	}

	expectedPIN := getSecurityPIN()
	if expectedPIN == "" {
		return nil
	}

	if strings.TrimSpace(providedPIN) == expectedPIN {
		delete(globalLimiter.attempts, ip)
		return nil
	}

	// Record failed attempt
	cutoff := now.Add(-3 * time.Minute)
	var recent []time.Time
	for _, t := range globalLimiter.attempts[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	recent = append(recent, now)
	globalLimiter.attempts[ip] = recent

	if len(recent) >= 5 {
		globalLimiter.lockouts[ip] = now.Add(5 * time.Minute)
		return fmt.Errorf("PIN salah 5 kali berturut-turut. Akses IP Anda dikunci selama 5 menit")
	}

	return fmt.Errorf("Security PIN salah atau belum dimasukkan! (Sisa percobaan: %d)", 5-len(recent))
}

func (ws *WebServer) handleBinanceAuth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req BinanceAuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Invalid JSON: %v"}`, err), http.StatusBadRequest)
		return
	}

	if err := checkAndVerifyPIN(r, req.SecurityPIN); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	key := strings.TrimSpace(req.APIKey)
	secret := strings.TrimSpace(req.APISecret)

	if key == "" || secret == "" {
		envKey, envSecret := getEnvCredentials()
		if key == "" {
			key = envKey
		}
		if secret == "" {
			secret = envSecret
		}
	}

	if key == "" || secret == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Binance API Key dan Secret belum diatur di file .env server!",
		})
		return
	}

	if ws.onTestAuth != nil {
		info, err := ws.onTestAuth(key, secret)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Binance API Key verified successfully from .env!",
			"account": info,
		})
		return
	}

	http.Error(w, "Auth handler unconfigured", http.StatusInternalServerError)
}

func (ws *WebServer) handleAPIConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var update WebConfigUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Invalid JSON: %v"}`, err), http.StatusBadRequest)
		return
	}

	providedPIN := ""
	if update.SecurityPIN != nil {
		providedPIN = *update.SecurityPIN
	}
	if err := checkAndVerifyPIN(r, providedPIN); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	if ws.onUpdate != nil {
		if err := ws.onUpdate(update); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Configuration updated successfully",
	})
}

func (ws *WebServer) runBroadcastLoop(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ws.dataMu.RLock()
			data := ws.latestData
			ws.dataMu.RUnlock()

			payload, err := json.Marshal(data)
			if err != nil {
				continue
			}

			// Copy active clients slice under RLock
			ws.clientsMu.RLock()
			if len(ws.clients) == 0 {
				ws.clientsMu.RUnlock()
				continue
			}
			clientList := make([]*websocket.Conn, 0, len(ws.clients))
			for conn := range ws.clients {
				clientList = append(clientList, conn)
			}
			ws.clientsMu.RUnlock()

			// Write to clients OUTSIDE the mutex to prevent lock contention
			for _, conn := range clientList {
				conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
				if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
					conn.Close()
					ws.clientsMu.Lock()
					delete(ws.clients, conn)
					ws.clientsMu.Unlock()
				}
			}
		}
	}
}

func (ws *WebServer) handleClearLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SecurityPIN string `json:"security_pin"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if err := checkAndVerifyPIN(r, req.SecurityPIN); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	if ws.onClearLog != nil {
		_ = ws.onClearLog()
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Trade execution log cleared successfully",
	})
}

// Credentials Helper (.env file storage)
func getEnvCredentials() (string, string) {
	var key, secret string
	if envData, err := os.ReadFile(".env"); err == nil {
		lines := strings.Split(string(envData), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "BINANCE_API_KEY=") {
				key = strings.Trim(strings.TrimPrefix(line, "BINANCE_API_KEY="), `"' `)
			}
			if strings.HasPrefix(line, "BINANCE_API_SECRET=") {
				secret = strings.Trim(strings.TrimPrefix(line, "BINANCE_API_SECRET="), `"' `)
			}
		}
	}
	if key == "" {
		key = os.Getenv("BINANCE_API_KEY")
	}
	if secret == "" {
		secret = os.Getenv("BINANCE_API_SECRET")
	}
	return key, secret
}

// Security PIN Helpers (.env file storage)
func getSecurityPIN() string {
	envPath := ".env"
	file, err := os.Open(envPath)
	if err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "SECURITY_PIN=") {
				pin := strings.TrimPrefix(line, "SECURITY_PIN=")
				pin = strings.Trim(pin, `"' `)
				if pin != "" {
					return pin
				}
			}
		}
	}
	if envPin := os.Getenv("SECURITY_PIN"); envPin != "" {
		return envPin
	}
	return "npma_0"
}

func saveSecurityPIN(newPIN string) error {
	envPath := ".env"
	var lines []string
	found := false

	if file, err := os.Open(envPath); err == nil {
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(strings.TrimSpace(line), "SECURITY_PIN=") {
				lines = append(lines, fmt.Sprintf("SECURITY_PIN=%s", newPIN))
				found = true
			} else {
				lines = append(lines, line)
			}
		}
		file.Close()
	}

	if !found {
		lines = append(lines, fmt.Sprintf("SECURITY_PIN=%s", newPIN))
	}

	content := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(envPath, []byte(content), 0600)
}

type VerifyPINRequest struct {
	PIN string `json:"pin"`
}

func (ws *WebServer) handleVerifyPIN(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req VerifyPINRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Format JSON tidak valid"})
		return
	}
	correctPIN := getSecurityPIN()
	if strings.TrimSpace(req.PIN) != correctPIN {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Kode keamanan (PIN) salah! Akses ditolak."})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Kode keamanan (PIN) terverifikasi!"})
}

type ChangePINRequest struct {
	CurrentPIN string `json:"current_pin"`
	NewPIN     string `json:"new_pin"`
}

func (ws *WebServer) handleChangePIN(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req ChangePINRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Format JSON tidak valid"})
		return
	}
	correctPIN := getSecurityPIN()
	if strings.TrimSpace(req.CurrentPIN) != correctPIN {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Kode keamanan lama (Current PIN) salah!"})
		return
	}
	newTrimmed := strings.TrimSpace(req.NewPIN)
	if newTrimmed == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Kode keamanan baru tidak boleh kosong!"})
		return
	}
	if err := saveSecurityPIN(newTrimmed); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": fmt.Sprintf("Gagal menyimpan ke .env: %v", err)})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Kode keamanan (PIN) berhasil diubah dan disimpan di .env!"})
}
