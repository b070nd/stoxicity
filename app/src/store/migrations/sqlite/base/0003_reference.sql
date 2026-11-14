-- Reference data: every stock symbol, company and its basic data, refreshed on
-- a schedule from the SEC and Nasdaq. The tables hold the latest content of
-- each source; ref_source says where it came from and ref_snapshot and
-- ref_change keep what changed, so the same inputs always give the same rows.

-- One row per source: the state a conditional request needs, and the hash of
-- the content that was stored. The hash covers the parsed rows in key order
-- (not the bytes of the answer), so a reformatted answer changes nothing.
CREATE TABLE ref_source (
    name          TEXT PRIMARY KEY,
    etag          TEXT NOT NULL DEFAULT '',
    last_modified TEXT NOT NULL DEFAULT '',
    hash          TEXT NOT NULL DEFAULT '',
    records       INTEGER NOT NULL DEFAULT 0,
    checked_ms    INTEGER NOT NULL DEFAULT 0, -- last time the source was confirmed current
    changed_ms    INTEGER NOT NULL DEFAULT 0, -- last time its content changed
    error         TEXT NOT NULL DEFAULT ''    -- why the last refresh failed, or empty
) WITHOUT ROWID;

-- The SEC list of US-listed tickers (company_tickers_exchange.json).
CREATE TABLE ref_ticker (
    ticker   TEXT PRIMARY KEY,
    name     TEXT NOT NULL,
    cik      TEXT NOT NULL,
    exchange TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
CREATE INDEX ref_ticker_cik ON ref_ticker (cik, ticker);

-- The Nasdaq Trader symbol directory (nasdaqlisted.txt and otherlisted.txt).
CREATE TABLE ref_listing (
    symbol TEXT PRIMARY KEY,
    name   TEXT NOT NULL,
    venue  TEXT NOT NULL DEFAULT '',
    market TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    etf    INTEGER NOT NULL DEFAULT 0,
    test   INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;

-- The SEC record of one company (submissions/CIK##########.json).
CREATE TABLE ref_company (
    cik         TEXT PRIMARY KEY,
    name        TEXT NOT NULL DEFAULT '',
    sic         TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    entity_type TEXT NOT NULL DEFAULT '',
    category    TEXT NOT NULL DEFAULT '',
    state       TEXT NOT NULL DEFAULT '',
    fiscal_year TEXT NOT NULL DEFAULT '',
    website     TEXT NOT NULL DEFAULT '',
    former      TEXT NOT NULL DEFAULT '[]',
    missing     INTEGER NOT NULL DEFAULT 0,
    fetched_ms  INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;

-- Every time a source's content changed, with its hash.
CREATE TABLE ref_snapshot (
    id      INTEGER PRIMARY KEY,
    source  TEXT NOT NULL,
    hash    TEXT NOT NULL,
    records INTEGER NOT NULL,
    at_ms   INTEGER NOT NULL
);
CREATE INDEX ref_snapshot_source ON ref_snapshot (source, id DESC);

-- What changed: a ticker or company that appeared, went, or changed. The first
-- load of a source is one snapshot and no changes.
CREATE TABLE ref_change (
    id     INTEGER PRIMARY KEY,
    at_ms  INTEGER NOT NULL,
    source TEXT NOT NULL,
    kind   TEXT NOT NULL CHECK (kind IN ('added', 'removed', 'changed')),
    key    TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX ref_change_at ON ref_change (at_ms DESC, id DESC);
