package exchange

import (
	"strconv"
)

// RawFilter represents a single exchange filter from Binance API.
type RawFilter struct {
	FilterType  string `json:"filterType"`
	MinPrice    string `json:"minPrice,omitempty"`
	MaxPrice    string `json:"maxPrice,omitempty"`
	TickSize    string `json:"tickSize,omitempty"`
	MinQty      string `json:"minQty,omitempty"`
	MaxQty      string `json:"maxQty,omitempty"`
	StepSize    string `json:"stepSize,omitempty"`
	MinNotional string `json:"minNotional,omitempty"`
	Notional    string `json:"notional,omitempty"`
}

// RawSymbolInfo represents symbol metadata from Binance /api/v3/exchangeInfo.
type RawSymbolInfo struct {
	Symbol                 string      `json:"symbol"`
	Status                 string      `json:"status"`
	BaseAsset              string      `json:"baseAsset"`
	QuoteAsset             string      `json:"quoteAsset"`
	BaseAssetPrecision     int         `json:"baseAssetPrecision"`
	QuotePrecision         int         `json:"quotePrecision"`
	IsSpotTradingAllowed   bool        `json:"isSpotTradingAllowed"`
	Filters                []RawFilter `json:"filters"`
	Permissions            []string    `json:"permissions"`
	IsMarginTradingAllowed bool        `json:"isMarginTradingAllowed"`
}

// ExchangeInfoResponse is the root object returned by /api/v3/exchangeInfo.
type ExchangeInfoResponse struct {
	Timezone   string          `json:"timezone"`
	ServerTime int64           `json:"serverTime"`
	Symbols    []RawSymbolInfo `json:"symbols"`
}

// ParsedSymbol contains pre-processed numeric filters for ultra-fast HFT math.
type ParsedSymbol struct {
	Symbol             string
	BaseAsset          string
	QuoteAsset         string
	Status             string
	BaseAssetPrecision int
	QuotePrecision     int
	MinPrice           float64
	MaxPrice           float64
	TickSize           float64
	MinQty             float64
	MaxQty             float64
	StepSize           float64
	MinNotional        float64
}

// Parse converts a RawSymbolInfo into a ParsedSymbol with precomputed floats.
func (r *RawSymbolInfo) Parse() ParsedSymbol {
	ps := ParsedSymbol{
		Symbol:             r.Symbol,
		BaseAsset:          r.BaseAsset,
		QuoteAsset:         r.QuoteAsset,
		Status:             r.Status,
		BaseAssetPrecision: r.BaseAssetPrecision,
		QuotePrecision:     r.QuotePrecision,
		TickSize:           0.00000001,
		StepSize:           0.00000001,
		MinNotional:        5.0, // standard default fallback
	}

	for _, f := range r.Filters {
		switch f.FilterType {
		case "PRICE_FILTER":
			if val, err := strconv.ParseFloat(f.MinPrice, 64); err == nil {
				ps.MinPrice = val
			}
			if val, err := strconv.ParseFloat(f.MaxPrice, 64); err == nil {
				ps.MaxPrice = val
			}
			if val, err := strconv.ParseFloat(f.TickSize, 64); err == nil && val > 0 {
				ps.TickSize = val
			}
		case "LOT_SIZE":
			if val, err := strconv.ParseFloat(f.MinQty, 64); err == nil {
				ps.MinQty = val
			}
			if val, err := strconv.ParseFloat(f.MaxQty, 64); err == nil {
				ps.MaxQty = val
			}
			if val, err := strconv.ParseFloat(f.StepSize, 64); err == nil && val > 0 {
				ps.StepSize = val
			}
		case "MIN_NOTIONAL":
			if val, err := strconv.ParseFloat(f.MinNotional, 64); err == nil && val > 0 {
				ps.MinNotional = val
			}
		case "NOTIONAL":
			if val, err := strconv.ParseFloat(f.MinNotional, 64); err == nil && val > 0 {
				ps.MinNotional = val
			} else if val, err := strconv.ParseFloat(f.Notional, 64); err == nil && val > 0 {
				ps.MinNotional = val
			}
		}
	}

	return ps
}

// BookTickerEvent represents real-time Binance !bookTicker WebSocket payload.
type BookTickerEvent struct {
	UpdateID        int64   `json:"u"`
	Symbol          string  `json:"s"`
	BestBidPriceStr string  `json:"b"`
	BestBidQtyStr   string  `json:"B"`
	BestAskPriceStr string  `json:"a"`
	BestAskQtyStr   string  `json:"A"`
	BestBidPrice    float64 `json:"-"`
	BestBidQty      float64 `json:"-"`
	BestAskPrice    float64 `json:"-"`
	BestAskQty      float64 `json:"-"`
	EventTimeMs     int64   `json:"E"` // Event time in ms from Binance
	LocalRecvTimeMs int64   `json:"-"` // Local receipt timestamp
}

// FastParse converts string prices and quantities in BookTickerEvent to float64.
func (e *BookTickerEvent) FastParse() bool {
	var err error
	e.BestBidPrice, err = strconv.ParseFloat(e.BestBidPriceStr, 64)
	if err != nil || e.BestBidPrice <= 0 {
		return false
	}
	e.BestBidQty, err = strconv.ParseFloat(e.BestBidQtyStr, 64)
	if err != nil {
		return false
	}
	e.BestAskPrice, err = strconv.ParseFloat(e.BestAskPriceStr, 64)
	if err != nil || e.BestAskPrice <= 0 {
		return false
	}
	e.BestAskQty, err = strconv.ParseFloat(e.BestAskQtyStr, 64)
	if err != nil {
		return false
	}
	return true
}

// OrderSide represents BUY or SELL.
type OrderSide string

const (
	SideBuy  OrderSide = "BUY"
	SideSell OrderSide = "SELL"
)

// OrderType represents order execution type.
type OrderType string

const (
	TypeLimit  OrderType = "LIMIT"
	TypeMarket OrderType = "MARKET"
)

// TimeInForce represents order lifetime.
type TimeInForce string

const (
	TimeInForceIOC TimeInForce = "IOC" // Immediate-Or-Cancel (Best for HFT arbitrage)
	TimeInForceFOK TimeInForce = "FOK" // Fill-Or-Kill
	TimeInForceGTC TimeInForce = "GTC" // Good-Til-Cancelled
)

// OrderRequest represents a spot order payload for Binance.
type OrderRequest struct {
	Symbol      string      `json:"symbol"`
	Side        OrderSide   `json:"side"`
	Type        OrderType   `json:"type"`
	TimeInForce TimeInForce `json:"timeInForce,omitempty"`
	Quantity    float64     `json:"quantity"`
	Price       float64     `json:"price,omitempty"`
	Timestamp   int64       `json:"timestamp"`
}

// OrderResponse represents response returned after order execution.
type OrderResponse struct {
	Symbol              string `json:"symbol"`
	OrderID             int64  `json:"orderId"`
	ClientOrderID       string `json:"clientOrderId"`
	TransactTime        int64  `json:"transactTime"`
	Price               string `json:"price"`
	OrigQty             string `json:"origQty"`
	ExecutedQty         string `json:"executedQty"`
	CummulativeQuoteQty string `json:"cummulativeQuoteQty"`
	Status              string `json:"status"`
	TimeInForce         string `json:"timeInForce"`
	Type                string `json:"type"`
	Side                string `json:"side"`
}
