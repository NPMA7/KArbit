package engine

import (
	"sync"

	"karbit/internal/exchange"
)

// TickerState holds the latest level-1 quote information for a market.
type TickerState struct {
	Symbol          string
	BestBidPrice    float64
	BestBidQty      float64
	BestAskPrice    float64
	BestAskQty      float64
	EventTimeMs     int64
	LocalRecvTimeMs int64
	UpdateID        int64
}

// PriceBook is a high-speed in-memory store for real-time market data.
type PriceBook struct {
	mu     sync.RWMutex
	quotes map[string]TickerState
}

// NewPriceBook initializes a clean PriceBook.
func NewPriceBook() *PriceBook {
	return &PriceBook{
		quotes: make(map[string]TickerState, 2048),
	}
}

// Update saves or updates the latest quote for a symbol.
func (pb *PriceBook) Update(ev exchange.BookTickerEvent) {
	pb.mu.Lock()
	pb.quotes[ev.Symbol] = TickerState{
		Symbol:          ev.Symbol,
		BestBidPrice:    ev.BestBidPrice,
		BestBidQty:      ev.BestBidQty,
		BestAskPrice:    ev.BestAskPrice,
		BestAskQty:      ev.BestAskQty,
		EventTimeMs:     ev.EventTimeMs,
		LocalRecvTimeMs: ev.LocalRecvTimeMs,
		UpdateID:        ev.UpdateID,
	}
	pb.mu.Unlock()
}

// Get retrieves a snapshot quote for a single symbol.
func (pb *PriceBook) Get(symbol string) (TickerState, bool) {
	pb.mu.RLock()
	state, exists := pb.quotes[symbol]
	pb.mu.RUnlock()
	return state, exists
}

// GetTriangleSnapshot atomically reads quotes for all 3 legs of a triangle in a single read-lock.
func (pb *PriceBook) GetTriangleSnapshot(symbols [3]string) ([3]TickerState, bool) {
	pb.mu.RLock()
	defer pb.mu.RUnlock()

	var states [3]TickerState
	for i, sym := range symbols {
		s, ok := pb.quotes[sym]
		if !ok || s.BestBidPrice <= 0 || s.BestAskPrice <= 0 {
			return states, false
		}
		states[i] = s
	}
	return states, true
}

// TotalTrackedSymbols returns the number of active tickers received.
func (pb *PriceBook) TotalTrackedSymbols() int {
	pb.mu.RLock()
	defer pb.mu.RUnlock()
	return len(pb.quotes)
}
