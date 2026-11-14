-- Evidence and the order in which it became visible.
--
-- observation is append-only: one row for each distinct thing a source said
-- about an article ID: a new row whenever a source's text for it differs from
-- that source's previous row (content_hash is empty on rows made from the
-- articles that existed before this table). Nothing in it is updated or deleted, so a second source's copy of an address, and a correction made at the
-- same address, are both kept. `articles` stays the current view.
--   received_ms  when this system got it
--   recorded_ms  when it was committed (seq is the durable order of commits)
CREATE TABLE observation (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    article_id   TEXT NOT NULL,
    source_id    TEXT NOT NULL,
    domain       TEXT NOT NULL,
    url          TEXT NOT NULL,
    title        TEXT NOT NULL,
    summary      TEXT NOT NULL,
    published_ms INTEGER NOT NULL,
    received_ms  INTEGER NOT NULL,
    recorded_ms  INTEGER NOT NULL,
    content_hash TEXT NOT NULL,
    form         TEXT NOT NULL DEFAULT '',
    cik          TEXT NOT NULL DEFAULT '',
    items        TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX observation_source ON observation (article_id, source_id, seq DESC);
CREATE INDEX observation_article ON observation (article_id, seq);

-- change_seq numbers every commit that inserted or changed an article, so a
-- consumer reads "everything after the last number I handled" and cannot miss a
-- slow writer (a wall-clock time can land behind a cursor that moved on).
ALTER TABLE articles ADD COLUMN change_seq INTEGER NOT NULL DEFAULT 0;
UPDATE articles SET change_seq = seq;
CREATE INDEX articles_change_seq ON articles (change_seq);

INSERT INTO observation (article_id, source_id, domain, url, title, summary, published_ms, received_ms, recorded_ms, content_hash, form, cik, items)
SELECT id, source_id, domain, url, title, summary, published_ms, fetched_ms, fetched_ms, '', form, cik, items FROM articles ORDER BY seq;
