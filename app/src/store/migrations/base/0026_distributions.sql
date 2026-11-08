-- Dividend and fund-distribution observations, exact and append-only.
--
-- One row is one source's statement about one payout of one share class: its type
-- (ordinary, special, ...), the period it is for, whether it is declared,
-- estimated or cancelled, the per-share amount as decimal text, the currency it
-- is paid in, and its dates. A revision (a cut, a cancellation, a corrected
-- ex-date) is a new row from the same source, never an update, so the payouts
-- known at a past time can be read back. Nothing here is cash.
CREATE TABLE distribution_obs (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    security_id  TEXT NOT NULL,
    type         TEXT NOT NULL,
    period       TEXT NOT NULL,
    status       TEXT NOT NULL,
    amount       TEXT NOT NULL,
    currency     TEXT NOT NULL,
    ex_day       TEXT NOT NULL,
    record_day   TEXT NOT NULL DEFAULT '',
    pay_day      TEXT NOT NULL DEFAULT '',
    declared_day TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL,
    source_ref   TEXT NOT NULL DEFAULT '',
    received_ms  INTEGER NOT NULL
);
CREATE INDEX distribution_obs_security ON distribution_obs (security_id, ex_day);

CREATE TRIGGER distribution_obs_no_update BEFORE UPDATE ON distribution_obs
BEGIN SELECT RAISE(ABORT, 'distribution_obs is append-only'); END;
CREATE TRIGGER distribution_obs_no_delete BEFORE DELETE ON distribution_obs
BEGIN SELECT RAISE(ABORT, 'distribution_obs is append-only'); END;
