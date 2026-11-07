-- One story can be several events, and each event rests on the claims that make
-- it, not on everything an article says.
--
-- assertion.action: which way the claim went (a dividend suspended, cut, raised).
-- Older rows read as ''.
ALTER TABLE assertion ADD COLUMN action TEXT NOT NULL DEFAULT '';

-- event_claim: the claims (assertions) of an article that a revision of an event
-- rests on. names = 1 marks the claim that decides the event's kind and title.
-- Appended with the revision, never changed. Evidence (event_evidence) says which
-- articles; this says which sentence of them.
CREATE TABLE event_claim (
    event_id          TEXT NOT NULL,
    revision          INTEGER NOT NULL,
    article_id        TEXT NOT NULL,
    assertion_seq     INTEGER NOT NULL,
    assertion_version TEXT NOT NULL,
    extractor         TEXT NOT NULL,
    names             INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (event_id, revision, article_id, assertion_seq, assertion_version, extractor),
    FOREIGN KEY (event_id, revision) REFERENCES event_revision (event_id, revision)
) WITHOUT ROWID;

-- event_relation: how events relate when a story is read as more than one thing.
--   split_from  the event is another event of a story whose first event is to_event
--   withdrawn   the event no longer stands under the current rules (its claim is
--               denied, in the past, or was never about a company); to_event is ''
--   superseded  the event's claims now belong to to_event
-- Appended; a relation is never removed, and a reader as of an earlier time
-- ignores the relations recorded later.
CREATE TABLE event_relation (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    from_event  TEXT NOT NULL,
    to_event    TEXT NOT NULL DEFAULT '',
    relation    TEXT NOT NULL,
    basis       TEXT NOT NULL DEFAULT '',
    recorded_ms INTEGER NOT NULL
);
CREATE UNIQUE INDEX event_relation_unique ON event_relation (from_event, to_event, relation);
CREATE INDEX event_relation_to ON event_relation (to_event);
