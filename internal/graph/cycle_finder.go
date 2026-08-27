package graph

import (
	"sort"

	"karbit/internal/exchange"
)

// GraphIndex contains pre-compiled triangles and symbol lookup indices.
type GraphIndex struct {
	BaseCurrency       string
	Triangles          []*Triangle
	SymbolToTriangles  map[string][]*Triangle
	UniqueSymbolsCount int
	AllRequiredSymbols map[string]bool
}

var restrictedFiatAssets = map[string]bool{
	"TRY":  true, // Turkish Lira (Region restricted on Binance)
	"BRL":  true, // Brazilian Real (Region restricted)
	"RUB":  true, // Russian Ruble (Region restricted)
	"UAH":  true, // Ukrainian Hryvnia (Region restricted)
	"NGN":  true, // Nigerian Naira (Region restricted)
	"ZAR":  true, // South African Rand (Region restricted)
	"ARS":  true, // Argentine Peso (Region restricted)
	"COP":  true, // Colombian Peso (Region restricted)
	"PLN":  true, // Polish Zloty (Region restricted)
	"RON":  true, // Romanian Leu (Region restricted)
	"CZK":  true, // Czech Koruna (Region restricted)
	"HUF":  true, // Hungarian Forint (Region restricted)
	"MXN":  true, // Mexican Peso (Region restricted)
	"KZT":  true, // Kazakhstani Tenge (Region restricted)
	"BIDR": true, // Indonesian Rupiah token (Restricted)
	"IDRT": true, // Rupiah token (Restricted)
	"GBP":  true, // British Pound (Restricted)
	"AUD":  true, // Australian Dollar (Restricted)
	"JPY":  true, // Japanese Yen (Restricted)
}

func isRestrictedFiat(asset string) bool {
	return restrictedFiatAssets[asset]
}

// BuildGraphIndex finds all valid 3-leg triangular arbitrage cycles starting and ending at baseCurrency.
func BuildGraphIndex(symbols []exchange.ParsedSymbol, baseCurrency string) *GraphIndex {
	// 1. Build adjacency map: assetPairMap[fromAsset][toAsset] = ParsedSymbol
	assetPairMap := make(map[string]map[string]exchange.ParsedSymbol)

	for _, sym := range symbols {
		if sym.BaseAsset == "" || sym.QuoteAsset == "" {
			continue
		}
		if isRestrictedFiat(sym.BaseAsset) || isRestrictedFiat(sym.QuoteAsset) {
			continue
		}
		if assetPairMap[sym.BaseAsset] == nil {
			assetPairMap[sym.BaseAsset] = make(map[string]exchange.ParsedSymbol)
		}
		if assetPairMap[sym.QuoteAsset] == nil {
			assetPairMap[sym.QuoteAsset] = make(map[string]exchange.ParsedSymbol)
		}

		assetPairMap[sym.QuoteAsset][sym.BaseAsset] = sym
		assetPairMap[sym.BaseAsset][sym.QuoteAsset] = sym
	}

	var triangles []*Triangle
	seenTriangles := make(map[string]bool)
	symbolToTriangles := make(map[string][]*Triangle)
	allRequiredSymbols := make(map[string]bool)

	// 2. Discover cycles: Base -> A -> B -> Base
	baseNeighbors := assetPairMap[baseCurrency]
	if baseNeighbors == nil {
		return &GraphIndex{
			BaseCurrency:       baseCurrency,
			Triangles:          triangles,
			SymbolToTriangles:  symbolToTriangles,
			AllRequiredSymbols: allRequiredSymbols,
		}
	}

	// Sort neighbor keys for deterministic ordering
	var neighborKeys []string
	for a := range baseNeighbors {
		neighborKeys = append(neighborKeys, a)
	}
	sort.Strings(neighborKeys)

	for _, assetA := range neighborKeys {
		if isRestrictedFiat(assetA) {
			continue
		}
		sym1 := baseNeighbors[assetA]
		leg1, ok1 := BuildLeg(baseCurrency, assetA, sym1)
		if !ok1 {
			continue
		}

		aNeighbors := assetPairMap[assetA]
		for assetB, sym2 := range aNeighbors {
			if assetB == baseCurrency || assetB == assetA || isRestrictedFiat(assetB) {
				continue
			}

			leg2, ok2 := BuildLeg(assetA, assetB, sym2)
			if !ok2 {
				continue
			}

			// Check if B connects back to BaseCurrency
			sym3, ok3 := assetPairMap[assetB][baseCurrency]
			if !ok3 {
				continue
			}

			leg3, ok4 := BuildLeg(assetB, baseCurrency, sym3)
			if !ok4 {
				continue
			}

			tri := NewTriangle(baseCurrency, assetA, assetB, leg1, leg2, leg3)
			if seenTriangles[tri.ID] {
				continue
			}
			seenTriangles[tri.ID] = true
			triangles = append(triangles, tri)

			// Populate symbol-to-triangle lookup
			for _, s := range tri.Symbols {
				symbolToTriangles[s] = append(symbolToTriangles[s], tri)
				allRequiredSymbols[s] = true
			}
		}
	}

	return &GraphIndex{
		BaseCurrency:       baseCurrency,
		Triangles:          triangles,
		SymbolToTriangles:  symbolToTriangles,
		UniqueSymbolsCount: len(allRequiredSymbols),
		AllRequiredSymbols: allRequiredSymbols,
	}
}

// GetActiveSubset returns a focused GraphIndex containing the top highest-liquidity complete triangles.
func (gi *GraphIndex) GetActiveSubset(maxTriangles int) *GraphIndex {
	if len(gi.Triangles) <= maxTriangles {
		return gi
	}

	priorityAssets := []string{
		"BTC", "ETH", "BNB", "SOL", "XRP", "DOGE", "ADA", "AVAX", "LINK", "SUI",
		"NEAR", "PEPE", "SHIB", "DOT", "TRX", "LTC", "BCH", "UNI", "APT", "FET",
		"TAO", "ATOM", "FIL", "OP", "ARB", "INJ", "TIA", "RENDER", "ICP", "FDUSD", "USDC",
	}
	priorityMap := make(map[string]int)
	for i, a := range priorityAssets {
		priorityMap[a] = (len(priorityAssets) - i) * 10
	}

	type triRank struct {
		tri   *Triangle
		score int
	}

	ranks := make([]triRank, 0, len(gi.Triangles))
	for _, tri := range gi.Triangles {
		score := priorityMap[tri.AssetA] + priorityMap[tri.AssetB]
		ranks = append(ranks, triRank{tri: tri, score: score})
	}

	sort.Slice(ranks, func(i, j int) bool {
		return ranks[i].score > ranks[j].score
	})

	selectedTriangles := make([]*Triangle, 0, maxTriangles)
	selectedSymbolToTri := make(map[string][]*Triangle)
	selectedAllSymbols := make(map[string]bool)

	for i := 0; i < len(ranks) && i < maxTriangles; i++ {
		tri := ranks[i].tri
		selectedTriangles = append(selectedTriangles, tri)
		for _, s := range tri.Symbols {
			selectedSymbolToTri[s] = append(selectedSymbolToTri[s], tri)
			selectedAllSymbols[s] = true
		}
	}

	return &GraphIndex{
		BaseCurrency:       gi.BaseCurrency,
		Triangles:          selectedTriangles,
		SymbolToTriangles:  selectedSymbolToTri,
		UniqueSymbolsCount: len(selectedAllSymbols),
		AllRequiredSymbols: selectedAllSymbols,
	}
}
