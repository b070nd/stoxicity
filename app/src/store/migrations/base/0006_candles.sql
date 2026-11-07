-- Daily candles (open, high, low, close, volume) of stocks, as a market-data
-- source gave them. A row is one stock on one trading day, so asking again for
-- a day replaces it and the table does not grow with the number of requests.
CREATE TABLE candle (
    ticker TEXT NOT NULL,
    day    TEXT NOT NULL,                -- YYYY-MM-DD, the exchange's own calendar
    open   REAL NOT NULL,
    high   REAL NOT NULL,
    low    REAL NOT NULL,
    close  REAL NOT NULL,
    volume INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (ticker, day)
) WITHOUT ROWID;

-- Where the look-up for each stock (or, as "day:YYYY-MM-DD", for each market day
-- of a source that gives whole days) has been, so that none is asked for twice.
CREATE TABLE candle_check (
    ticker     TEXT PRIMARY KEY,
    checked_ms INTEGER NOT NULL,
    found      INTEGER NOT NULL DEFAULT 0,
    error      TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
