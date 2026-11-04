// Package marketdata supplies the numbers on a stock page: quotes and
// fundamentals. EDGAR filings are free; prices and fundamentals come from a
// provider whose licence decides what may be stored or shown.
package marketdata

import (
	"context"
	"time"
)

// Snapshot is what a stock page shows next to the news.
type Snapshot struct {
	Ticker    string
	AsOf      time.Time
	Price     float64
	Currency  string
	MarketCap float64
	PE        float64
	// Fundamentals from the latest annual filing.
	Revenue      float64
	NetIncome    float64
	FreeCashFlow float64
	DebtToEquity float64
}

// Provider returns data for one ticker.
type Provider interface {
	Snapshot(ctx context.Context, ticker string) (Snapshot, error)
}
