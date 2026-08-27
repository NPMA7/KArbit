package engine

import (
	"time"
)

// LatencyGuard monitors quote staleness and protects against executing on outdated data.
type LatencyGuard struct {
	maxLatencyMs int64
}

// NewLatencyGuard creates a new latency monitor.
func NewLatencyGuard(maxLatencyMs int64) *LatencyGuard {
	if maxLatencyMs <= 0 {
		maxLatencyMs = 50
	}
	return &LatencyGuard{
		maxLatencyMs: maxLatencyMs,
	}
}

// SetMaxLatency dynamically updates the latency threshold.
func (lg *LatencyGuard) SetMaxLatency(maxLatencyMs int64) {
	if maxLatencyMs <= 0 {
		maxLatencyMs = 50
	}
	lg.maxLatencyMs = maxLatencyMs
}

// AreTriangleQuotesFresh checks if all quotes in a triangle or quad are within the maximum latency threshold.
func (lg *LatencyGuard) AreTriangleQuotesFresh(quotes []TickerState, nowMs int64) (bool, int64) {
	if nowMs == 0 {
		nowMs = time.Now().UnixMilli()
	}

	var maxAge int64 = 0

	for _, q := range quotes {
		// If quote has never been populated
		if q.LocalRecvTimeMs == 0 {
			return false, 999999
		}

		age := nowMs - q.LocalRecvTimeMs
		if age < 0 {
			age = 0
		}
		if age > maxAge {
			maxAge = age
		}

		// Also check exchange event time if available
		if q.EventTimeMs > 0 {
			eventAge := nowMs - q.EventTimeMs
			if eventAge > maxAge {
				maxAge = eventAge
			}
		}

		if maxAge > lg.maxLatencyMs {
			return false, maxAge
		}
	}

	return true, maxAge
}
