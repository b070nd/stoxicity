-- Intraday bars of stocks (minute aggregates and multiples of them), as a market
-- data source gave them. A row is one stock, one bar length and one bar start,
-- so asking again for a bar replaces it and the table does not grow with the
-- number of requests. ts_utc_ms is the start of the bar, in UTC milliseconds;
-- interval_s is its length in seconds. raw says the prices are as traded, not
-- adjusted for any split; currency is empty when the source does not say.
CREATE TABLE intraday_bar (
    ticker      TEXT NOT NULL,
    interval_s  INTEGER NOT NULL CHECK (interval_s > 0),
    ts_utc_ms   INTEGER NOT NULL,
    open        REAL NOT NULL,
    high        REAL NOT NULL,
    low         REAL NOT NULL,
    close       REAL NOT NULL,
    volume      INTEGER NOT NULL DEFAULT 0,
    raw         INTEGER NOT NULL DEFAULT 0,
    currency    TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT '',
    fetched_ms  INTEGER NOT NULL,
    PRIMARY KEY (ticker, interval_s, ts_utc_ms)
) WITHOUT ROWID;

-- intraday_bar_revision: an earlier version of a bar that a later fetch replaced
-- with different numbers, kept with when it was fetched and when it was replaced,
-- so that what the installation held at a past time can be read back. Only
-- changes are kept; asking again for an unchanged bar writes nothing.
CREATE TABLE intraday_bar_revision (
    ticker        TEXT NOT NULL,
    interval_s    INTEGER NOT NULL,
    ts_utc_ms     INTEGER NOT NULL,
    open          REAL NOT NULL,
    high          REAL NOT NULL,
    low           REAL NOT NULL,
    close         REAL NOT NULL,
    volume        INTEGER NOT NULL DEFAULT 0,
    raw           INTEGER NOT NULL DEFAULT 0,
    currency      TEXT NOT NULL DEFAULT '',
    source        TEXT NOT NULL DEFAULT '',
    fetched_ms    INTEGER NOT NULL,
    superseded_ms INTEGER NOT NULL,
    PRIMARY KEY (ticker, interval_s, ts_utc_ms, fetched_ms)
) WITHOUT ROWID;

-- intraday_check: the days (the exchange's calendar, YYYY-MM-DD) for which the
-- source has been asked for a stock's bars of one length, and how many bars it
-- gave, so that none is asked for twice. A day with no bars (a holiday, a
-- weekend, a stock that did not trade) is recorded too: asking is what is
-- remembered, not finding.
CREATE TABLE intraday_check (
    ticker      TEXT NOT NULL,
    interval_s  INTEGER NOT NULL,
    day         TEXT NOT NULL,
    bars        INTEGER NOT NULL DEFAULT 0,
    checked_ms  INTEGER NOT NULL,
    PRIMARY KEY (ticker, interval_s, day)
) WITHOUT ROWID;
