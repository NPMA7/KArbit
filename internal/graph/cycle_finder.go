package graph

import (
	"sort"

	"karbit/internal/exchange"
)

// GraphIndex contains pre-compiled triangles/quads and symbol lookup indices.
type GraphIndex struct {
	BaseCurrency       string
	Triangles          []*Triangle
	SymbolToTriangles  map[string][]*Triangle
	UniqueSymbolsCount int
	AllRequiredSymbols map[string]bool
}

var restrictedFiatAssets = map[string]bool{
	"TRY":  true, // Turkish Lira (Restricted)
	"RUB":  true, // Russian Ruble (Restricted)
	"UAH":  true, // Ukrainian Hryvnia (Restricted)
	"NGN":  true, // Nigerian Naira (Restricted)
	"ZAR":  true, // South African Rand (Restricted)
	"COP":  true, // Colombian Peso (Restricted)
	"PLN":  true, // Polish Zloty (Restricted)
	"RON":  true, // Romanian Leu (Restricted)
	"CZK":  true, // Czech Koruna (Restricted)
	"HUF":  true, // Hungarian Forint (Restricted)
	"KZT":  true, // Kazakhstani Tenge (Restricted)
	"BIDR": true, // Indonesian Rupiah token (Restricted)
	"IDRT": true, // Rupiah token (Restricted)
	"GBP":  true, // British Pound (Restricted)
	"AUD":  true, // Australian Dollar (Restricted)
	"JPY":  true, // Japanese Yen (Restricted)
	"IDR":  true, // Rupiah (Restricted)
}

func isRestrictedFiat(asset string) bool {
	return restrictedFiatAssets[asset]
}

// BuildGraphIndex finds all valid 3-leg and 4-leg arbitrage cycles starting and ending at baseCurrency.
func BuildGraphIndex(symbols []exchange.ParsedSymbol, baseCurrency string) *GraphIndex {
	return BuildMultiGraphIndex(symbols, []string{baseCurrency})
}

// BuildMultiGraphIndex finds all valid 3-hop (100%) and 4-hop (top 50k) cycles starting and ending at baseCurrencies.
func BuildMultiGraphIndex(symbols []exchange.ParsedSymbol, baseCurrencies []string) *GraphIndex {
	if len(baseCurrencies) == 0 {
		baseCurrencies = []string{"USDT", "USDC"}
	}

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

	priorityAssets := []string{
		"BTC", "ETH", "BNB", "SOL", "XRP", "DOGE", "ADA", "AVAX", "LINK", "SUI",
		"NEAR", "PEPE", "SHIB", "DOT", "TRX", "LTC", "BCH", "UNI", "APT", "FET",
		"TAO", "ATOM", "FIL", "OP", "ARB", "INJ", "TIA", "RENDER", "ICP", "FDUSD", "USDC", "USDT", "EUR", "BRL", "MXN", "ARS",
	}
	priorityMap := make(map[string]int)
	for i, a := range priorityAssets {
		priorityMap[a] = (len(priorityAssets) - i) * 10
	}

	var triangles []*Triangle
	seenTriangles := make(map[string]bool)
	symbolToTriangles := make(map[string][]*Triangle)
	allRequiredSymbols := make(map[string]bool)

	// 2. Discover 100% of all 3-Hop Triangular Cycles: Base -> A -> B -> Base
	for _, baseCurrency := range baseCurrencies {
		baseNeighbors := assetPairMap[baseCurrency]
		if baseNeighbors == nil {
			continue
		}

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
			}
		}
	}

	// 3. Discover 4-Hop Quadrilateral Cycles: Base -> A -> B -> C -> Base
	type quadRank struct {
		quad  *Triangle
		score int
	}
	var quads []quadRank

	for _, baseCurrency := range baseCurrencies {
		baseNeighbors := assetPairMap[baseCurrency]
		if baseNeighbors == nil {
			continue
		}

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

				bNeighbors := assetPairMap[assetB]
				for assetC, sym3 := range bNeighbors {
					if assetC == baseCurrency || assetC == assetA || assetC == assetB || isRestrictedFiat(assetC) {
						continue
					}
					leg3, ok3 := BuildLeg(assetB, assetC, sym3)
					if !ok3 {
						continue
					}

					sym4, ok4 := assetPairMap[assetC][baseCurrency]
					if !ok4 {
						continue
					}
					leg4, ok5 := BuildLeg(assetC, baseCurrency, sym4)
					if !ok5 {
						continue
					}

					quad := NewQuad(baseCurrency, assetA, assetB, assetC, leg1, leg2, leg3, leg4)
					score := priorityMap[assetA] + priorityMap[assetB] + priorityMap[assetC]
					quads = append(quads, quadRank{quad: quad, score: score})
				}
			}
		}
	}

	// Sort quads descending by liquidity/volume score
	sort.Slice(quads, func(i, j int) bool {
		return quads[i].score > quads[j].score
	})

	// Select 100% Full 4-hop routes (165,388 paths)
	target4Hop := len(quads)

	quadCount := 0
	for i := 0; i < len(quads) && quadCount < target4Hop; i++ {
		q := quads[i].quad
		if seenTriangles[q.ID] {
			continue
		}
		seenTriangles[q.ID] = true
		triangles = append(triangles, q)
		quadCount++
	}

	// Index all symbols across both 3-hop and 4-hop paths
	for _, path := range triangles {
		for _, s := range path.Symbols {
			symbolToTriangles[s] = append(symbolToTriangles[s], path)
			allRequiredSymbols[s] = true
		}
	}

	baseLabel := "USDT+USDC"
	if len(baseCurrencies) == 1 {
		baseLabel = baseCurrencies[0]
	}

	return &GraphIndex{
		BaseCurrency:       baseLabel,
		Triangles:          triangles,
		SymbolToTriangles:  symbolToTriangles,
		UniqueSymbolsCount: len(allRequiredSymbols),
		AllRequiredSymbols: allRequiredSymbols,
	}
}

// GetActiveSubset returns a focused GraphIndex containing the configured limit of paths.
func (gi *GraphIndex) GetActiveSubset(maxTriangles int) *GraphIndex {
	if maxTriangles <= 0 || len(gi.Triangles) <= maxTriangles {
		return gi
	}

	selectedTriangles := gi.Triangles[:maxTriangles]
	selectedSymbolToTri := make(map[string][]*Triangle)
	selectedAllSymbols := make(map[string]bool)

	for _, tri := range selectedTriangles {
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
