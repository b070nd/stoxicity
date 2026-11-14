-- Where the reporting came from, and which events are one episode.
--
-- article_origin: the reporting origin of an article, apart from its publisher
-- (see package origin). Rows are appended, never changed: a later rule version,
-- or a later copy that shows an article to be a copy, adds a row, and the latest
-- row with detected_ms at or before a time is what was understood then. Nothing
-- found later is backdated.
CREATE TABLE article_origin (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    article_id   TEXT NOT NULL,
    version      TEXT NOT NULL,
    publisher    TEXT NOT NULL,
    kind         TEXT NOT NULL,   -- primary | issuer | wire | reported | derived | unknown
    origin_key   TEXT NOT NULL,   -- articles with one key are copies of one report
    derived_from TEXT NOT NULL DEFAULT '',
    copy_of      TEXT NOT NULL DEFAULT '',
    method       TEXT NOT NULL,
    confidence   REAL NOT NULL,
    detail       TEXT NOT NULL DEFAULT '',
    detected_ms  INTEGER NOT NULL
);
CREATE INDEX article_origin_article ON article_origin (article_id, seq DESC);
CREATE INDEX article_origin_key ON article_origin (origin_key);

-- episode_decision: whether two events of one security are one episode (the
-- same thing happening, seen through several documents) or distinct. A decision
-- states its basis (the identifier, role or fact that decided it) and is
-- appended; a later decision on the same pair supersedes the earlier one for
-- readers after decided_ms, and a reading as of an earlier time ignores it.
-- The events themselves, and the observations under them, are never touched.
CREATE TABLE episode_decision (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    security_id TEXT NOT NULL,
    event_a     TEXT NOT NULL,    -- event_a < event_b
    event_b     TEXT NOT NULL,
    decision    TEXT NOT NULL,    -- same | distinct | undecided
    basis       TEXT NOT NULL,
    method      TEXT NOT NULL,
    confidence  REAL NOT NULL,
    version     TEXT NOT NULL,
    decided_ms  INTEGER NOT NULL
);
CREATE INDEX episode_decision_pair ON episode_decision (event_a, event_b, seq DESC);
CREATE INDEX episode_decision_security ON episode_decision (security_id, decided_ms);
