-- State of the hit ledger (package ledger): how far each watch has been
-- evaluated, which alerts of which hits were delivered, and which digests were
-- sent.

-- eval_cursor: the newest fetched time a watch has been evaluated up to, and
-- the hash of the watch definition that evaluation used. A different hash means
-- the watch changed and the cursor starts again.
CREATE TABLE eval_cursor (
    watch_id   TEXT PRIMARY KEY,
    fetched_ms INTEGER NOT NULL,
    watch_hash TEXT NOT NULL
) WITHOUT ROWID;

-- delivered: one row for each alert name of a hit that was handled. at_ms is
-- the time of the delivery, which the cooldown of an alert is measured from.
CREATE TABLE delivered (
    watch_id   TEXT NOT NULL,
    article_id TEXT NOT NULL,
    alert      TEXT NOT NULL,
    at_ms      INTEGER NOT NULL,
    PRIMARY KEY (watch_id, article_id, alert)
) WITHOUT ROWID;

CREATE INDEX delivered_alert_at ON delivered (watch_id, alert, at_ms DESC);

-- digest_sent: the periods (a day or an ISO week) whose digest of a watch was
-- sent.
CREATE TABLE digest_sent (
    watch_id   TEXT NOT NULL,
    period_key TEXT NOT NULL,
    at_ms      INTEGER NOT NULL,
    PRIMARY KEY (watch_id, period_key)
) WITHOUT ROWID;

-- The hits that carry alert names, oldest first, for the delivery pass.
CREATE INDEX hits_alerting ON hits (at_ms) WHERE alerts <> '[]';
