package graph

import (
	"fmt"

	"karbit/internal/exchange"
)

// TradeAction defines whether to BUY (spend quote, get base) or SELL (spend base, get quote).
type TradeAction string

const (
	ActionBuy  TradeAction = "BUY"
	ActionSell TradeAction = "SELL"
)

// Leg represents one trade hop in a triangular or quadrilateral cycle.
type Leg struct {
	Symbol      string      `json:"symbol"`
	FromAsset   string      `json:"from_asset"`
	ToAsset     string      `json:"to_asset"`
	Action      TradeAction `json:"action"`
	StepSize    float64     `json:"step_size"`
	TickSize    float64     `json:"tick_size"`
	MinNotional float64     `json:"min_notional"`
	MinQty      float64     `json:"min_qty"`
}

// Triangle represents a pre-compiled 3-hop or 4-hop arbitrage path starting and ending at BaseAsset.
type Triangle struct {
	ID        string   `json:"id"`
	BaseAsset string   `json:"base_asset"`
	AssetA    string   `json:"asset_a"`
	AssetB    string   `json:"asset_b"`
	AssetC    string   `json:"asset_c,omitempty"` // Used for 4-Hop
	HopCount  int      `json:"hop_count"`         // 3 or 4
	Leg1      Leg      `json:"leg1"`
	Leg2      Leg      `json:"leg2"`
	Leg3      Leg      `json:"leg3"`
	Leg4      *Leg     `json:"leg4,omitempty"`    // Used for 4-Hop
	Symbols   []string `json:"symbols"`
}

// NewTriangle constructs a 3-hop Triangle and pre-populates metadata.
func NewTriangle(baseAsset, assetA, assetB string, leg1, leg2, leg3 Leg) *Triangle {
	id := fmt.Sprintf("%s->%s->%s->%s", baseAsset, assetA, assetB, baseAsset)
	return &Triangle{
		ID:        id,
		BaseAsset: baseAsset,
		AssetA:    assetA,
		AssetB:    assetB,
		HopCount:  3,
		Leg1:      leg1,
		Leg2:      leg2,
		Leg3:      leg3,
		Symbols:   []string{leg1.Symbol, leg2.Symbol, leg3.Symbol},
	}
}

// NewQuad constructs a 4-hop Quadrilateral arbitrage path and pre-populates metadata.
func NewQuad(baseAsset, assetA, assetB, assetC string, leg1, leg2, leg3, leg4 Leg) *Triangle {
	id := fmt.Sprintf("%s->%s->%s->%s->%s", baseAsset, assetA, assetB, assetC, baseAsset)
	return &Triangle{
		ID:        id,
		BaseAsset: baseAsset,
		AssetA:    assetA,
		AssetB:    assetB,
		AssetC:    assetC,
		HopCount:  4,
		Leg1:      leg1,
		Leg2:      leg2,
		Leg3:      leg3,
		Leg4:      &leg4,
		Symbols:   []string{leg1.Symbol, leg2.Symbol, leg3.Symbol, leg4.Symbol},
	}
}

// BuildLeg constructs a Leg given fromAsset, toAsset and the corresponding ParsedSymbol.
func BuildLeg(fromAsset, toAsset string, sym exchange.ParsedSymbol) (Leg, bool) {
	if sym.QuoteAsset == fromAsset && sym.BaseAsset == toAsset {
		// e.g. from USDT to BTC with symbol BTCUSDT -> Action BUY
		return Leg{
			Symbol:      sym.Symbol,
			FromAsset:   fromAsset,
			ToAsset:     toAsset,
			Action:      ActionBuy,
			StepSize:    sym.StepSize,
			TickSize:    sym.TickSize,
			MinNotional: sym.MinNotional,
			MinQty:      sym.MinQty,
		}, true
	} else if sym.BaseAsset == fromAsset && sym.QuoteAsset == toAsset {
		// e.g. from BTC to USDT with symbol BTCUSDT -> Action SELL
		return Leg{
			Symbol:      sym.Symbol,
			FromAsset:   fromAsset,
			ToAsset:     toAsset,
			Action:      ActionSell,
			StepSize:    sym.StepSize,
			TickSize:    sym.TickSize,
			MinNotional: sym.MinNotional,
			MinQty:      sym.MinQty,
		}, true
	}
	return Leg{}, false
}
