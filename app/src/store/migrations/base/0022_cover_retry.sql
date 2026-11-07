-- The retry state of each issuer's cover-page reading, kept so that a restart
-- continues where the last cycle stopped and an operator can see why an issuer
-- has no capitalisation without searching.
--   kind      '' the last look worked; 'failed' the request or the parsing failed
--             (problem says how); 'nocover' the issuer's recent reports have no
--             machine-readable cover page (not an error: unsupported by this method)
--   failures  consecutive failed looks, 0 after one that worked
--   next_ms   not before this time is the issuer looked at again; 0 means the
--             ordinary age rule applies
ALTER TABLE cover_check ADD COLUMN kind TEXT NOT NULL DEFAULT '';
ALTER TABLE cover_check ADD COLUMN failures INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cover_check ADD COLUMN next_ms INTEGER NOT NULL DEFAULT 0;
-- Earlier failures were not counted: one failure is assumed for each, and the
-- reading that found no cover page is told from an error by its message.
UPDATE cover_check SET kind = 'nocover' WHERE problem LIKE 'no annual or quarterly report with a machine-readable cover page%';
UPDATE cover_check SET kind = 'failed', failures = 1 WHERE problem <> '' AND kind = '';
