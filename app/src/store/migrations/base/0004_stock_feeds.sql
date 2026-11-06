-- The feeds of each stock: its SEC filing record and, where the company's own
-- website points to one, its news or investor-relations feed. A feed is a row
-- that carries its own fetch state (validators, newest IDs seen) and its own
-- schedule, because there are thousands of them and the scheduler polls them as
-- two pools, not as sources of their own.

CREATE TABLE stock_feed (
    ticker      TEXT NOT NULL,
    kind        TEXT NOT NULL,             -- 'sec' or 'site'
    url         TEXT NOT NULL,
    title       TEXT NOT NULL DEFAULT '',
    found_ms    INTEGER NOT NULL DEFAULT 0,
    state       TEXT NOT NULL DEFAULT '{}', -- feeds.State as JSON
    next_due_ms INTEGER NOT NULL DEFAULT 0,
    last_ok_ms  INTEGER NOT NULL DEFAULT 0,
    last_new    INTEGER NOT NULL DEFAULT 0,
    failures    INTEGER NOT NULL DEFAULT 0,
    last_error  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (ticker, kind, url)
) WITHOUT ROWID;
CREATE INDEX stock_feed_due ON stock_feed (kind, next_due_ms);

-- Where the search for a company's feed has been, so a company whose website has
-- none is not asked again every time.
CREATE TABLE stock_feed_search (
    ticker     TEXT PRIMARY KEY,
    checked_ms INTEGER NOT NULL,
    found      INTEGER NOT NULL DEFAULT 0,
    error      TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
