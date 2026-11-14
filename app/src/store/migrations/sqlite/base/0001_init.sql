-- Shared information base: articles, their scores, per-source scheduler state
-- and a full-text index over articles.

-- seq is a stable integer key for the full-text index; a table with only a
-- text primary key has a rowid that VACUUM may renumber.
CREATE TABLE articles (
    seq          INTEGER PRIMARY KEY,
    id           TEXT NOT NULL UNIQUE,
    source_id    TEXT NOT NULL,
    domain       TEXT NOT NULL,
    url          TEXT NOT NULL,
    title        TEXT NOT NULL,
    summary      TEXT NOT NULL,
    published_ms INTEGER NOT NULL,
    fetched_ms   INTEGER NOT NULL,
    form         TEXT NOT NULL DEFAULT '',
    items        TEXT NOT NULL DEFAULT '[]',
    tickers      TEXT NOT NULL DEFAULT '[]',
    topics       TEXT NOT NULL DEFAULT '[]',
    cluster_id   TEXT NOT NULL DEFAULT ''
);

CREATE INDEX articles_published ON articles (published_ms DESC, id);
CREATE INDEX articles_cluster ON articles (cluster_id) WHERE cluster_id <> '';

CREATE TABLE scores (
    article_id   TEXT NOT NULL REFERENCES articles (id) ON DELETE CASCADE,
    lens_version TEXT NOT NULL,
    relevance    REAL NOT NULL,
    materiality  INTEGER NOT NULL,
    tone         REAL NOT NULL,
    kind         TEXT NOT NULL,
    sector       TEXT NOT NULL,
    reasons      TEXT NOT NULL DEFAULT '[]',
    created_ms   INTEGER NOT NULL,
    PRIMARY KEY (article_id, lens_version)
) WITHOUT ROWID;

CREATE TABLE source_state (
    source_id  TEXT PRIMARY KEY,
    state      TEXT NOT NULL,
    health     TEXT NOT NULL,
    updated_ms INTEGER NOT NULL
) WITHOUT ROWID;

CREATE VIRTUAL TABLE articles_fts USING fts5 (
    title, summary, tickers,
    content = 'articles', content_rowid = 'seq'
);

-- The index is kept in sync by Save: it adds new rows in one statement per
-- batch, which is about ten times faster than a per-row insert trigger. Rows
-- that change or disappear are handled here, because the old values must be
-- removed from the index.
CREATE TRIGGER articles_fts_delete AFTER DELETE ON articles BEGIN
    INSERT INTO articles_fts (articles_fts, rowid, title, summary, tickers)
    VALUES ('delete', old.seq, old.title, old.summary, old.tickers);
END;

CREATE TRIGGER articles_fts_update AFTER UPDATE OF title, summary, tickers ON articles
WHEN old.title IS NOT new.title OR old.summary IS NOT new.summary OR old.tickers IS NOT new.tickers BEGIN
    INSERT INTO articles_fts (articles_fts, rowid, title, summary, tickers)
    VALUES ('delete', old.seq, old.title, old.summary, old.tickers);
    INSERT INTO articles_fts (rowid, title, summary, tickers)
    VALUES (new.seq, new.title, new.summary, new.tickers);
END;
