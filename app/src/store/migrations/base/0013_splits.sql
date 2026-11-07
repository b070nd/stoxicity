-- Corporate actions and the basis of the stored prices.
--
-- Candles fetched from now on are raw (as traded that day, not adjusted for any
-- split), so a later split changes nothing in them and the adjustment is a
-- function of the split table. Rows made before this table are of unknown basis
-- (raw = 0): the source adjusted them for the splits it knew when they were
-- fetched (fetched_ms is 0 for them, unknown).
ALTER TABLE candle ADD COLUMN raw INTEGER NOT NULL DEFAULT 0;
ALTER TABLE candle ADD COLUMN fetched_ms INTEGER NOT NULL DEFAULT 0;

-- split: a stock split as the source reported it. exec_day is the first day the
-- stock trades at the new price. One share became split_to / split_from shares
-- (2-for-1: from 1 to 2; 1-for-10 reverse split: from 10 to 1). Rows are only
-- added; a source that reports a split again changes nothing.
CREATE TABLE split (
    ticker        TEXT NOT NULL,
    exec_day      TEXT NOT NULL,
    split_from    REAL NOT NULL,
    split_to      REAL NOT NULL,
    source        TEXT NOT NULL,
    first_seen_ms INTEGER NOT NULL,
    PRIMARY KEY (ticker, exec_day, split_from, split_to)
) WITHOUT ROWID;

-- split_run: what the splits cover. Splits with an exec_day from since_day on
-- were read at read_ms; a price move that no listed split explains is then a
-- real move, and before that it is not known to be.
CREATE TABLE split_run (
    id        INTEGER PRIMARY KEY CHECK (id = 1),
    since_day TEXT NOT NULL,
    read_ms   INTEGER NOT NULL
);
