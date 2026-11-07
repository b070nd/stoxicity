-- The change number (articles.change_seq) a watch has been evaluated up to. 0
-- means the cursor was made before the numbers existed and is read from
-- fetched_ms once.
ALTER TABLE eval_cursor ADD COLUMN seq INTEGER NOT NULL DEFAULT 0;
