-- More of what the SEC says about a company (the exchanges its stock trades
-- on, its business address, phone and identifiers), kept as one JSON object so
-- that a field added later is not a migration, and the official website of each
-- ticker as found in Wikidata (the SEC leaves its own website field empty).

ALTER TABLE ref_company ADD COLUMN extra TEXT NOT NULL DEFAULT '{}';

CREATE TABLE ref_website (
    ticker     TEXT PRIMARY KEY,
    url        TEXT NOT NULL DEFAULT '',   -- empty: looked for, not found
    label      TEXT NOT NULL DEFAULT '',   -- the name Wikidata knows the company by
    fetched_ms INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;
