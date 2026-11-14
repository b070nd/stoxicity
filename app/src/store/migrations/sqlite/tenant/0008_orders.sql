-- Orders: the local record of every order intent and what became of it. An order
-- is a log of events; its state is derived by folding the log, never stored. The
-- log is append-only. An event that carries a key (a broker acknowledgement, an
-- execution ID) can be written once per order and kind, so telling the same fact
-- twice changes nothing. Intents and the cash or shares they hold are written in
-- one transaction together with the ledger reservation, in code, by the store.
CREATE TABLE order_event (
    seq      INTEGER PRIMARY KEY AUTOINCREMENT,
    order_id TEXT NOT NULL,
    book_id  TEXT NOT NULL REFERENCES pf_books (id),
    account  TEXT NOT NULL,
    n        INTEGER NOT NULL CHECK (n >= 1),
    kind     TEXT NOT NULL,
    key      TEXT NOT NULL,
    at_ms    INTEGER NOT NULL,
    body     TEXT NOT NULL,
    UNIQUE (order_id, n)
);
CREATE UNIQUE INDEX order_event_key ON order_event (order_id, kind, key) WHERE key <> '';
CREATE INDEX order_event_account ON order_event (account, seq);

CREATE TRIGGER order_event_no_update BEFORE UPDATE ON order_event
BEGIN SELECT RAISE(ABORT, 'order_event is append-only'); END;
CREATE TRIGGER order_event_no_delete BEFORE DELETE ON order_event
BEGIN SELECT RAISE(ABORT, 'order_event is append-only'); END;

-- The durable switch for new orders of an environment. The newest row of an
-- environment is in force; with no row an environment has its default.
CREATE TABLE order_control (
    seq      INTEGER PRIMARY KEY AUTOINCREMENT,
    env      TEXT NOT NULL CHECK (env IN ('real', 'paper_local', 'paper_broker')),
    disabled INTEGER NOT NULL CHECK (disabled IN (0, 1)),
    reason   TEXT NOT NULL,
    by       TEXT NOT NULL,
    at_ms    INTEGER NOT NULL
);

CREATE TRIGGER order_control_no_update BEFORE UPDATE ON order_control
BEGIN SELECT RAISE(ABORT, 'order_control is append-only'); END;
CREATE TRIGGER order_control_no_delete BEFORE DELETE ON order_control
BEGIN SELECT RAISE(ABORT, 'order_control is append-only'); END;
