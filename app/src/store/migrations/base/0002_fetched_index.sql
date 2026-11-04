-- Articles read by the time they were first fetched, oldest first: the ledger
-- (package ledger) evaluates only what arrived since its last look.
CREATE INDEX articles_fetched ON articles (fetched_ms, id);
