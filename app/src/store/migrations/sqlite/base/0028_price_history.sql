-- Periodic, split-adjusted public history is not a raw daily candle.
CREATE TABLE price_history_state (
    ticker TEXT PRIMARY KEY,
    provider TEXT NOT NULL DEFAULT 'yahoo-chart',
    basis TEXT NOT NULL DEFAULT 'split-adjusted-price',
    symbol TEXT NOT NULL DEFAULT '',
    currency TEXT NOT NULL DEFAULT '',
    latest_day TEXT NOT NULL DEFAULT '',
    checked_ms INTEGER NOT NULL DEFAULT 0,
    fetched_ms INTEGER NOT NULL DEFAULT 0,
    lease_until_ms INTEGER NOT NULL DEFAULT 0,
    problem TEXT NOT NULL DEFAULT ''
);
CREATE TABLE price_history_month (
    ticker TEXT NOT NULL REFERENCES price_history_state(ticker),
    month TEXT NOT NULL,
    close REAL NOT NULL CHECK(close > 0),
    PRIMARY KEY(ticker,month)
);
