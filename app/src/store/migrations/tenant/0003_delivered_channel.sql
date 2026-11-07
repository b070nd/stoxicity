-- delivered_channel: one row for each channel an alert name of a hit (or a
-- digest period, as article_id "digest:<period>") was sent to. An alert is
-- recorded in `delivered` only when every channel it names is here, so a
-- channel that failed or is not configured yet is tried again without sending
-- the others twice.
CREATE TABLE delivered_channel (
    watch_id   TEXT NOT NULL,
    article_id TEXT NOT NULL,
    alert      TEXT NOT NULL,
    channel    TEXT NOT NULL,
    at_ms      INTEGER NOT NULL,
    PRIMARY KEY (watch_id, article_id, alert, channel)
) WITHOUT ROWID;
