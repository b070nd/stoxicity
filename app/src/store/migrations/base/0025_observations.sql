-- Exchange-rate and price observations, exact and append-only.
--
-- A value is stored as the decimal text the source gave, never as a float. Each
-- row says what it is a value of (day), who gave it (source, source_ref), and when
-- this system received it (received_ms), so the values known at a past time can be
-- read back and a later correction is a new row, not an overwrite.
--
-- fx_obs.basis separates a reference rate (an ECB-style daily rate, for reporting)
-- from a fill (a rate a broker actually gave) and a user-typed assumption.
-- price_obs.unit_basis says whether the unit was stated by the source or taken
-- from the instrument record; delay and adjustment say what the price is.
CREATE TABLE fx_obs (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    base       TEXT NOT NULL,
    quote      TEXT NOT NULL,
    value      TEXT NOT NULL,
    basis      TEXT NOT NULL,
    day        TEXT NOT NULL,
    source     TEXT NOT NULL,
    source_ref TEXT NOT NULL DEFAULT '',
    received_ms INTEGER NOT NULL
);
CREATE INDEX fx_obs_pair ON fx_obs (base, quote, day);

CREATE TABLE price_obs (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    listing_id  TEXT NOT NULL,
    day         TEXT NOT NULL,
    kind        TEXT NOT NULL,
    value       TEXT NOT NULL,
    unit        TEXT NOT NULL,
    unit_basis  TEXT NOT NULL,
    adjustment  TEXT NOT NULL,
    delay       TEXT NOT NULL,
    quote_ms    INTEGER NOT NULL DEFAULT 0,
    source      TEXT NOT NULL,
    source_ref  TEXT NOT NULL DEFAULT '',
    received_ms INTEGER NOT NULL
);
CREATE INDEX price_obs_listing ON price_obs (listing_id, day);

CREATE TRIGGER fx_obs_no_update BEFORE UPDATE ON fx_obs
BEGIN SELECT RAISE(ABORT, 'fx_obs is append-only'); END;
CREATE TRIGGER fx_obs_no_delete BEFORE DELETE ON fx_obs
BEGIN SELECT RAISE(ABORT, 'fx_obs is append-only'); END;
CREATE TRIGGER price_obs_no_update BEFORE UPDATE ON price_obs
BEGIN SELECT RAISE(ABORT, 'price_obs is append-only'); END;
CREATE TRIGGER price_obs_no_delete BEFORE DELETE ON price_obs
BEGIN SELECT RAISE(ABORT, 'price_obs is append-only'); END;
