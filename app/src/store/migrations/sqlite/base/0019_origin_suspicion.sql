-- Suspected derivation and the observations behind a copy finding.
--
-- article_origin.suspected_from: the article this one is suspected to derive
-- from (a substantial passage equals one of that article), not proven. Rows are
-- still only appended; old rows read as '' (no suspicion). The corroboration
-- verdict does not count a suspected derivative as an independent report.
ALTER TABLE article_origin ADD COLUMN suspected_from TEXT NOT NULL DEFAULT '';

-- origin_match: what was seen when two articles were compared, kept so that a
-- finding can be explained and checked later. Appended once per article, other
-- article, rule version and method; the same comparison made again adds nothing.
CREATE TABLE origin_match (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    article_id   TEXT NOT NULL,
    other_id     TEXT NOT NULL,
    version      TEXT NOT NULL,
    method       TEXT NOT NULL,   -- exact-copy | near-copy | passage-overlap
    score        REAL NOT NULL,
    shared_runs  INTEGER NOT NULL,
    longest_run  INTEGER NOT NULL,
    passage      TEXT NOT NULL DEFAULT '',
    detected_ms  INTEGER NOT NULL
);
CREATE UNIQUE INDEX origin_match_unique ON origin_match (article_id, other_id, version, method);
