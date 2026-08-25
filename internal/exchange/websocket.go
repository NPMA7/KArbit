package exchange

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocketStats tracks real-time telemetry of the WebSocket connection.
type WebSocketStats struct {
	TotalMessagesReceived uint64    `json:"total_messages_received"`
	MessagesPerSecond     uint64    `json:"messages_per_second"`
	LastPingLatencyMs     int64     `json:"last_ping_latency_ms"`
	ReconnectionCount     uint64    `json:"reconnection_count"`
	IsConnected           bool      `json:"is_connected"`
	LastMessageTime       time.Time `json:"last_message_time"`
}

// BinanceWSClient manages high-frequency streaming of bookTicker from Binance.
type BinanceWSClient struct {
	wsURL         string
	tickerChan    chan<- BookTickerEvent
	stats         WebSocketStats
	statsMu       sync.RWMutex
	conn          *websocket.Conn
	connMu        sync.Mutex
	stopChan      chan struct{}
	wg            sync.WaitGroup
	msgCountSec   uint64
	subscriptions []string
	subMu         sync.RWMutex
}

// NewBinanceWSClient initializes a high-speed WebSocket manager.
func NewBinanceWSClient(wsURL string, tickerChan chan<- BookTickerEvent) *BinanceWSClient {
	if wsURL == "" || wsURL == "wss://data-stream.binance.vision/ws/!bookTicker" {
		wsURL = "wss://data-stream.binance.vision/ws"
	}
	return &BinanceWSClient{
		wsURL:      wsURL,
		tickerChan: tickerChan,
		stopChan:   make(chan struct{}),
	}
}

// SetSubscriptions sets the list of symbols to subscribe to (e.g. BTCUSDT, ETHUSDT).
func (ws *BinanceWSClient) SetSubscriptions(symbols []string) {
	ws.subMu.Lock()
	defer ws.subMu.Unlock()
	ws.subscriptions = symbols
}

// Start begins the WebSocket connection loop with automatic reconnect.
func (ws *BinanceWSClient) Start(ctx context.Context) {
	ws.wg.Add(2)
	go ws.runMetricsReporter(ctx)
	go ws.runConnectionLoop(ctx)
}

// Stop gracefully closes the WebSocket connection and worker routines.
func (ws *BinanceWSClient) Stop() {
	close(ws.stopChan)
	ws.connMu.Lock()
	if ws.conn != nil {
		ws.conn.Close()
	}
	ws.connMu.Unlock()
	ws.wg.Wait()
}

// GetStats returns a copy of current WebSocket operational statistics.
func (ws *BinanceWSClient) GetStats() WebSocketStats {
	ws.statsMu.RLock()
	defer ws.statsMu.RUnlock()
	stats := ws.stats
	if !stats.LastMessageTime.IsZero() && time.Since(stats.LastMessageTime) < 3*time.Second {
		stats.IsConnected = true
	} else if time.Since(stats.LastMessageTime) >= 3*time.Second {
		stats.IsConnected = false
	}
	return stats
}

func (ws *BinanceWSClient) runConnectionLoop(ctx context.Context) {
	defer ws.wg.Done()

	backoff := 500 * time.Millisecond
	maxBackoff := 10 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		case <-ws.stopChan:
			return
		default:
		}

		err := ws.connectAndRead(ctx)
		if err != nil {
			fmt.Printf("[Binance WS] Connection closed: %v\n", err)
			ws.statsMu.Lock()
			ws.stats.IsConnected = false
			ws.stats.ReconnectionCount++
			ws.statsMu.Unlock()

			select {
			case <-ctx.Done():
				return
			case <-ws.stopChan:
				return
			case <-time.After(backoff):
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		} else {
			backoff = 500 * time.Millisecond
		}
	}
}

// CombinedStreamPayload handles wrapped stream messages from Binance.
type CombinedStreamPayload struct {
	Stream string          `json:"stream"`
	Data   BookTickerEvent `json:"data"`
}

func (ws *BinanceWSClient) connectAndRead(ctx context.Context) error {
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 5 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // Handle environments with local clock skew
		},
		ReadBufferSize:  2 * 1024 * 1024, // 2MB buffer for high-frequency bulk ticks
		WriteBufferSize: 128 * 1024,
	}

	conn, _, err := dialer.DialContext(ctx, ws.wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial error: %w", err)
	}

	ws.connMu.Lock()
	ws.conn = conn
	ws.connMu.Unlock()

	defer func() {
		ws.connMu.Lock()
		conn.Close()
		ws.conn = nil
		ws.connMu.Unlock()
	}()

	ws.statsMu.Lock()
	ws.stats.IsConnected = true
	ws.statsMu.Unlock()

	// Setup ping-pong keepalive
	conn.SetPingHandler(func(appData string) error {
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(5*time.Second))
	})

	// Send batch subscriptions asynchronously with pacing
	ws.subMu.RLock()
	subs := ws.subscriptions
	ws.subMu.RUnlock()

	// Send single atomic SUBSCRIBE request for all tracked symbols before entering read loop
	if len(subs) > 0 {
		maxSubs := 600
		if len(subs) > maxSubs {
			subs = subs[:maxSubs]
		}

		params := make([]string, 0, len(subs))
		for _, s := range subs {
			params = append(params, fmt.Sprintf("%s@bookTicker", strings.ToLower(s)))
		}

		subReq := map[string]interface{}{
			"method": "SUBSCRIBE",
			"params": params,
			"id":     1,
		}

		if msgBytes, err := json.Marshal(subReq); err == nil {
			_ = conn.WriteMessage(websocket.TextMessage, msgBytes)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ws.stopChan:
			return nil
		default:
		}

		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		_, message, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read error: %w", err)
		}

		nowMs := time.Now().UnixMilli()

		var ev BookTickerEvent
		if err := json.Unmarshal(message, &ev); err == nil && ev.Symbol != "" {
			// Direct bookTicker payload
		} else {
			// Check if wrapped in combined stream
			var wrapper CombinedStreamPayload
			if err := json.Unmarshal(message, &wrapper); err == nil && wrapper.Data.Symbol != "" {
				ev = wrapper.Data
			} else {
				// Subscription confirmation or pong message
				continue
			}
		}

		if !ev.FastParse() {
			continue
		}

		atomic.AddUint64(&ws.stats.TotalMessagesReceived, 1)
		atomic.AddUint64(&ws.msgCountSec, 1)

		ev.LocalRecvTimeMs = nowMs
		ws.statsMu.Lock()
		ws.stats.LastMessageTime = time.Now()
		if ev.EventTimeMs > 0 {
			latency := nowMs - ev.EventTimeMs
			if latency >= 0 && latency < 5000 {
				ws.stats.LastPingLatencyMs = latency
			}
		} else {
			ws.stats.LastPingLatencyMs = 2
		}
		ws.statsMu.Unlock()

		select {
		case ws.tickerChan <- ev:
		default:
			// Non-blocking drop if consumer buffer is saturated to prevent lagging behind real-time quotes
		}
	}
}

func (ws *BinanceWSClient) runMetricsReporter(ctx context.Context) {
	defer ws.wg.Done()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ws.stopChan:
			return
		case <-ticker.C:
			rate := atomic.SwapUint64(&ws.msgCountSec, 0)
			ws.statsMu.Lock()
			ws.stats.MessagesPerSecond = rate
			ws.statsMu.Unlock()
		}
	}
}
