-- One tenant's data: label history, alert hits and read markers. The file
-- belongs to a single tenant, so tenant_id is recorded but never filtered on.

CREATE TABLE labels (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    article_id   TEXT NOT NULL,
    verdict      TEXT NOT NULL CHECK (verdict IN ('keep', 'drop')),
    at_ms        INTEGER NOT NULL,
    lens_version TEXT NOT NULL DEFAULT '',
    sampled      INTEGER NOT NULL DEFAULT 0,
    tenant_id    TEXT NOT NULL
);

CREATE INDEX labels_article ON labels (article_id, at_ms, seq);
CREATE INDEX labels_at ON labels (at_ms DESC, seq DESC);

CREATE TABLE hits (
    watch_id   TEXT NOT NULL,
    article_id TEXT NOT NULL,
    at_ms      INTEGER NOT NULL,
    alerts     TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (watch_id, article_id)
) WITHOUT ROWID;

CREATE INDEX hits_watch_at ON hits (watch_id, at_ms DESC);

CREATE TABLE seen (
    watch_id TEXT PRIMARY KEY,
    at_ms    INTEGER NOT NULL
) WITHOUT ROWID;
