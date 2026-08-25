package exchange

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestWS100Symbols(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clientAPI := NewBinanceClient("https://data-api.binance.vision", "", "")
	symbols, err := clientAPI.FetchExchangeInfo(ctx)
	if err != nil {
		t.Fatalf("Failed to fetch symbols: %v", err)
	}

	subList := make([]string, 0, 100)
	for _, s := range symbols {
		if s.QuoteAsset == "USDT" || s.QuoteAsset == "BTC" || s.QuoteAsset == "BNB" || s.QuoteAsset == "ETH" {
			subList = append(subList, s.Symbol)
			if len(subList) >= 100 {
				break
			}
		}
	}

	tickerChan := make(chan BookTickerEvent, 10000)
	client := NewBinanceWSClient("wss://data-stream.binance.vision/ws", tickerChan)
	client.SetSubscriptions(subList)

	client.Start(ctx)
	defer client.Stop()

	received := 0
	start := time.Now()
	timer := time.After(3 * time.Second)

	for {
		select {
		case <-timer:
			dur := time.Since(start).Seconds()
			fmt.Printf("Received %d ticks across %d symbols in %.2f seconds (%.1f ticks/sec)!\n", received, len(subList), dur, float64(received)/dur)
			if received < 10 {
				t.Fatalf("Too few ticks received: %d", received)
			}
			return
		case <-tickerChan:
			received++
		}
	}
}
