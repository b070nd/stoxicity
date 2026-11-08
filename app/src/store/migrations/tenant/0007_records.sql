-- An append-only store of typed records: scorecard figures and notes, valuation
-- and plan snapshots, order intents and their events, decision-journal entries.
-- A record is never changed or deleted: a revision is a new row for the same key
-- with other content, and "current" is the newest row of a key. Writing the same
-- content under the same key again changes nothing.
CREATE TABLE records (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT NOT NULL,
    key        TEXT NOT NULL,
    hash       TEXT NOT NULL,
    body       TEXT NOT NULL,
    created_ms INTEGER NOT NULL,
    UNIQUE (kind, key, hash)
);
CREATE INDEX records_kind_key ON records (kind, key, seq);

CREATE TRIGGER records_no_update BEFORE UPDATE ON records
BEGIN SELECT RAISE(ABORT, 'records is append-only'); END;
CREATE TRIGGER records_no_delete BEFORE DELETE ON records
BEGIN SELECT RAISE(ABORT, 'records is append-only'); END;
