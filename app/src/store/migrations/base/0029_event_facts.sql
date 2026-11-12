-- The historical event database: one row is one revision of one economic event
-- record (package eventfact). The record is stored whole as JSON in body; the
-- other columns repeat what a read filters or orders by. A correction, or a
-- change of the extraction rules, is a new revision with a later recorded time,
-- never an update, so the records known at any past time can be read back.
-- The table is append-only, like the other revision tables.
CREATE TABLE event_fact (
    id           TEXT NOT NULL,
    revision     INTEGER NOT NULL,
    hash         TEXT NOT NULL,
    key          TEXT NOT NULL,
    category     TEXT NOT NULL,
    type         TEXT NOT NULL,
    security_id  TEXT NOT NULL DEFAULT '',
    symbol       TEXT NOT NULL DEFAULT '',
    cik          TEXT NOT NULL DEFAULT '',
    event_id     TEXT NOT NULL DEFAULT '',
    published_ms INTEGER NOT NULL,
    available_ms INTEGER NOT NULL,
    precision    TEXT NOT NULL,
    source       TEXT NOT NULL,
    source_ref   TEXT NOT NULL,
    basis        TEXT NOT NULL,
    extractor    TEXT NOT NULL,
    version      TEXT NOT NULL,
    body         TEXT NOT NULL,
    recorded_ms  INTEGER NOT NULL,
    PRIMARY KEY (id, revision)
) WITHOUT ROWID;
CREATE INDEX event_fact_available ON event_fact (available_ms);
CREATE INDEX event_fact_symbol ON event_fact (symbol, available_ms);
CREATE INDEX event_fact_type ON event_fact (type, available_ms);

CREATE TRIGGER event_fact_no_update BEFORE UPDATE ON event_fact
BEGIN SELECT RAISE(ABORT, 'event_fact is append-only'); END;
CREATE TRIGGER event_fact_no_delete BEFORE DELETE ON event_fact
BEGIN SELECT RAISE(ABORT, 'event_fact is append-only'); END;
