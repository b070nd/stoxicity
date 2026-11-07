-- What the SEC's XBRL frames say about each filer: revenue and net income for a
-- fiscal year, assets and equity at a date, shares outstanding. A row is one
-- number of one company for one period, so reading the market again replaces
-- the rows instead of adding to them.
CREATE TABLE fundamental (
    cik          TEXT NOT NULL,            -- ten digits
    metric       TEXT NOT NULL,            -- revenue, income, assets, equity, shares, shares_avg
    period_end   TEXT NOT NULL,            -- YYYY-MM-DD
    period_start TEXT NOT NULL DEFAULT '', -- empty for a value at a date
    value        REAL NOT NULL,
    PRIMARY KEY (cik, metric, period_end)
) WITHOUT ROWID;

-- When the market was last read (one row).
CREATE TABLE fundamental_run (
    id       INTEGER PRIMARY KEY CHECK (id = 1),
    read_ms  INTEGER NOT NULL,
    facts    INTEGER NOT NULL DEFAULT 0
);
