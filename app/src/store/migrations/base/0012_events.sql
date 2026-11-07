-- Securities, events and what is known about them, append-only.
--
-- security: a stable internal identity. A ticker is only a label of it for a
-- period (security_ticker): tickers change, are reused, and collide across
-- venues. cik is the issuer's SEC number (several securities, the classes of one
-- company, share one); empty when not known.
CREATE TABLE security (
    id         TEXT PRIMARY KEY,
    cik        TEXT NOT NULL DEFAULT '',
    name       TEXT NOT NULL DEFAULT '',
    class      TEXT NOT NULL DEFAULT '',   -- the share class when known: "A", "B", "common"
    created_ms INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX security_cik ON security (cik) WHERE cik <> '';

CREATE TABLE security_ticker (
    security_id TEXT NOT NULL REFERENCES security (id),
    ticker      TEXT NOT NULL,
    venue       TEXT NOT NULL DEFAULT '',
    from_day    TEXT NOT NULL,             -- YYYY-MM-DD, the first day it was seen with this label
    to_day      TEXT NOT NULL DEFAULT '',  -- empty while it still is
    PRIMARY KEY (security_id, ticker, from_day)
) WITHOUT ROWID;
CREATE INDEX security_ticker_label ON security_ticker (ticker, to_day);

-- event: one thing that happened. An article is evidence about an event, not the
-- event: ten copies of a story are one event, one filing may be several.
CREATE TABLE event (
    id         TEXT PRIMARY KEY,
    created_ms INTEGER NOT NULL
) WITHOUT ROWID;

-- event_revision: what is understood about the event, appended, never changed.
-- A correction is a new revision that supersedes the one before.
--   public_ms    when the earliest evidence was published (when the world could know)
--   recorded_ms  when this system committed the revision (what it knew then)
--   assessed under lens_version; materiality and relevance are the lens's reading,
--   not a fact about the event.
CREATE TABLE event_revision (
    event_id     TEXT NOT NULL REFERENCES event (id),
    revision     INTEGER NOT NULL,
    kind         TEXT NOT NULL,
    status       TEXT NOT NULL,             -- reported | corrected
    title        TEXT NOT NULL,
    effective_day TEXT NOT NULL DEFAULT '', -- the day the action happened or takes effect, when known
    public_ms    INTEGER NOT NULL,
    recorded_ms  INTEGER NOT NULL,
    materiality  INTEGER NOT NULL,
    relevance    REAL NOT NULL,
    lens_version TEXT NOT NULL,
    fingerprint  TEXT NOT NULL,
    supersedes   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (event_id, revision)
) WITHOUT ROWID;
CREATE INDEX event_revision_recorded ON event_revision (recorded_ms);

-- event_security: which securities a revision concerns, and the observed role
-- ("subject", "counterparty", "mentioned"). The role is a relationship seen in
-- the evidence, not a judgement of price effect.
CREATE TABLE event_security (
    event_id    TEXT NOT NULL,
    revision    INTEGER NOT NULL,
    security_id TEXT NOT NULL REFERENCES security (id),
    role        TEXT NOT NULL,
    PRIMARY KEY (event_id, revision, security_id),
    FOREIGN KEY (event_id, revision) REFERENCES event_revision (event_id, revision)
) WITHOUT ROWID;
CREATE INDEX event_security_by_security ON event_security (security_id, event_id, revision);

-- event_evidence: the observations (article ID and source) a revision rests on.
CREATE TABLE event_evidence (
    event_id   TEXT NOT NULL,
    revision   INTEGER NOT NULL,
    article_id TEXT NOT NULL,
    source_id  TEXT NOT NULL,
    relation   TEXT NOT NULL DEFAULT 'supports',   -- supports | contradicts
    PRIMARY KEY (event_id, revision, article_id, source_id),
    FOREIGN KEY (event_id, revision) REFERENCES event_revision (event_id, revision)
) WITHOUT ROWID;

-- event_assessment: an interpretation, apart from the observation. Each has its
-- own horizon, confidence, rationale and the rule or model version that made it.
CREATE TABLE event_assessment (
    event_id    TEXT NOT NULL,
    revision    INTEGER NOT NULL,
    security_id TEXT NOT NULL REFERENCES security (id),
    rule        TEXT NOT NULL,               -- lens version or model name
    effect      TEXT NOT NULL,               -- what is assessed, e.g. "materiality"
    direction   TEXT NOT NULL,               -- up | down | unknown
    horizon     TEXT NOT NULL,               -- e.g. "unspecified", "1d", "1y"
    confidence  REAL NOT NULL,
    rationale   TEXT NOT NULL DEFAULT '',
    assessed_ms INTEGER NOT NULL,
    PRIMARY KEY (event_id, revision, security_id, rule, effect),
    FOREIGN KEY (event_id, revision) REFERENCES event_revision (event_id, revision)
) WITHOUT ROWID;

-- event_topic: topics a revision was filed under.
CREATE TABLE event_topic (
    event_id TEXT NOT NULL,
    revision INTEGER NOT NULL,
    topic    TEXT NOT NULL,
    PRIMARY KEY (event_id, revision, topic),
    FOREIGN KEY (event_id, revision) REFERENCES event_revision (event_id, revision)
) WITHOUT ROWID;
