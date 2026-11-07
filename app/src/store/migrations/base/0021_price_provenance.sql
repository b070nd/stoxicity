-- What a price, a split and a split reading were worth to a valuation at a past
-- time. Additive: nothing is rewritten.
--
-- candle.currency: the currency the source states for the price. '' is unknown
-- (every candle stored before this column, and every source that does not say):
-- a valuation then needs another stated basis (the listing venue's currency) and
-- says which, and never assumes one.
ALTER TABLE candle ADD COLUMN currency TEXT NOT NULL DEFAULT '';

-- candle_revision: an earlier version of a candle that a later fetch replaced
-- with different numbers, kept with when it was fetched and when it was replaced,
-- so that what the installation held at a past time can be read back. Only
-- changes are kept; asking again for an unchanged day writes nothing.
CREATE TABLE candle_revision (
    ticker        TEXT NOT NULL,
    day           TEXT NOT NULL,
    open          REAL NOT NULL,
    high          REAL NOT NULL,
    low           REAL NOT NULL,
    close         REAL NOT NULL,
    volume        INTEGER NOT NULL DEFAULT 0,
    raw           INTEGER NOT NULL DEFAULT 0,
    currency      TEXT NOT NULL DEFAULT '',
    fetched_ms    INTEGER NOT NULL,
    superseded_ms INTEGER NOT NULL,
    PRIMARY KEY (ticker, day, fetched_ms)
) WITHOUT ROWID;

-- split_read: every reading of the split list, so the coverage can be told as of
-- a past time (split_run keeps only the union). The reading kept in split_run
-- before this table is carried over as one row: its start is the union's and its
-- time the newest reading's, which can only overstate what was covered earlier.
CREATE TABLE split_read (
    seq       INTEGER PRIMARY KEY AUTOINCREMENT,
    since_day TEXT NOT NULL,
    read_ms   INTEGER NOT NULL
);
INSERT INTO split_read (since_day, read_ms) SELECT since_day, read_ms FROM split_run;
