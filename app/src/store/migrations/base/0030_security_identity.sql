-- Which symbols were one security's, and when (package marketdata, SymbolSpan).
-- One row is one revision of one span: the period from valid_from to valid_to
-- (inclusive, NULL while it still holds) in which a symbol was the security's.
-- kind says how the span ended (rename, merger, delisting, relisting) or, for a
-- spinoff, records that the successor was cut out of the security; it is empty
-- for a span that has not ended. A merger's or spinoff's successor is another
-- security. A correction is a new revision of the same security and valid_from
-- with a later recorded time, never an update, so the history known at any past
-- time can be read back. A span joins price series only when verified is 1,
-- which says a person checked it against the document in source_ref.
-- The table is append-only, like the other revision tables.
CREATE TABLE security_symbol (
    security    TEXT NOT NULL,
    valid_from  TEXT NOT NULL,
    revision    INTEGER NOT NULL,
    symbol      TEXT NOT NULL,
    valid_to    TEXT,
    kind        TEXT NOT NULL DEFAULT '' CHECK (kind IN ('', 'rename', 'merger', 'spinoff', 'delisting', 'relisting')),
    successor   TEXT,
    source      TEXT NOT NULL,
    source_ref  TEXT NOT NULL DEFAULT '',
    verified    INTEGER NOT NULL DEFAULT 0 CHECK (verified IN (0, 1)),
    recorded_ms INTEGER NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (security, valid_from, revision)
) WITHOUT ROWID;
CREATE INDEX security_symbol_symbol ON security_symbol (symbol, valid_from);

CREATE TRIGGER security_symbol_no_update BEFORE UPDATE ON security_symbol
BEGIN SELECT RAISE(ABORT, 'security_symbol is append-only'); END;
CREATE TRIGGER security_symbol_no_delete BEFORE DELETE ON security_symbol
BEGIN SELECT RAISE(ABORT, 'security_symbol is append-only'); END;
