-- The identity of what can be bought: issuers, securities (one per share class
-- and per fund share class) and listings (a security on a venue, with its
-- currency and the unit its price is quoted in). Every table is a list of
-- revisions: a correction is a new revision with a later recorded time, never an
-- update, so the instruments known at any past time can be read back.
--
-- The existing security table stays the register of stable internal IDs; a
-- security_revision row adds what is known about it. A listing is identified by
-- venue, ticker and first day, so a ticker that is reused later is another
-- listing.
CREATE TABLE issuer_revision (
    issuer_id   TEXT NOT NULL,
    revision    INTEGER NOT NULL,
    name        TEXT NOT NULL,
    country     TEXT NOT NULL DEFAULT '',
    cik         TEXT NOT NULL DEFAULT '',
    lei         TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL,
    recorded_ms INTEGER NOT NULL,
    PRIMARY KEY (issuer_id, revision)
) WITHOUT ROWID;

CREATE TABLE security_revision (
    security_id  TEXT NOT NULL REFERENCES security (id),
    revision     INTEGER NOT NULL,
    issuer_id    TEXT NOT NULL,
    name         TEXT NOT NULL,
    kind         TEXT NOT NULL,
    share_class  TEXT NOT NULL DEFAULT '',
    isin         TEXT NOT NULL DEFAULT '',
    distribution TEXT NOT NULL DEFAULT '',
    domicile     TEXT NOT NULL DEFAULT '',
    ucits        TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL,
    recorded_ms  INTEGER NOT NULL,
    PRIMARY KEY (security_id, revision)
) WITHOUT ROWID;
CREATE INDEX security_revision_isin ON security_revision (isin) WHERE isin <> '';

CREATE TABLE listing_revision (
    listing_id  TEXT NOT NULL,
    revision    INTEGER NOT NULL,
    security_id TEXT NOT NULL,
    mic         TEXT NOT NULL,
    ticker      TEXT NOT NULL,
    currency    TEXT NOT NULL,
    quote_unit  TEXT NOT NULL,
    from_day    TEXT NOT NULL,
    to_day      TEXT NOT NULL DEFAULT '',
    primary_flag TEXT NOT NULL DEFAULT '',
    contracts   TEXT NOT NULL DEFAULT '[]',
    source      TEXT NOT NULL,
    recorded_ms INTEGER NOT NULL,
    PRIMARY KEY (listing_id, revision)
) WITHOUT ROWID;
CREATE INDEX listing_revision_symbol ON listing_revision (mic, ticker);

CREATE TRIGGER issuer_revision_no_update BEFORE UPDATE ON issuer_revision
BEGIN SELECT RAISE(ABORT, 'issuer_revision is append-only'); END;
CREATE TRIGGER issuer_revision_no_delete BEFORE DELETE ON issuer_revision
BEGIN SELECT RAISE(ABORT, 'issuer_revision is append-only'); END;
CREATE TRIGGER security_revision_no_update BEFORE UPDATE ON security_revision
BEGIN SELECT RAISE(ABORT, 'security_revision is append-only'); END;
CREATE TRIGGER security_revision_no_delete BEFORE DELETE ON security_revision
BEGIN SELECT RAISE(ABORT, 'security_revision is append-only'); END;
CREATE TRIGGER listing_revision_no_update BEFORE UPDATE ON listing_revision
BEGIN SELECT RAISE(ABORT, 'listing_revision is append-only'); END;
CREATE TRIGGER listing_revision_no_delete BEFORE DELETE ON listing_revision
BEGIN SELECT RAISE(ABORT, 'listing_revision is append-only'); END;
