-- Accounts and the portfolio journal. A book is one environment (real, local
-- paper or broker paper) of one account of this tenant; every journal row carries
-- the environment of its book and the foreign key below ties the two together,
-- so a paper transaction cannot be filed in a real book or the other way round.
-- The journal, its entries, the lot openings and the import records are
-- append-only; a correction is a reversal, never an edit. A book's sequence has
-- no gaps, and each transaction carries a chain hash over its predecessor so an
-- edit made outside the program is found by verify. The only change a book ever
-- gets is to be archived, and only a paper book can be.
CREATE TABLE pf_books (
    id                  TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL,
    account             TEXT NOT NULL,
    name                TEXT NOT NULL,
    env                 TEXT NOT NULL CHECK (env IN ('real', 'paper_local', 'paper_broker')),
    base                TEXT NOT NULL,
    method              TEXT NOT NULL CHECK (method IN ('fifo', 'lifo')),
    allow_negative_cash INTEGER NOT NULL CHECK (allow_negative_cash IN (0, 1)),
    status              TEXT NOT NULL CHECK (status IN ('active', 'archived')),
    created_ms          INTEGER NOT NULL,
    archived_ms         INTEGER,
    UNIQUE (id, env),
    UNIQUE (tenant_id, account, name)
);

CREATE TABLE pf_tx (
    book_id      TEXT NOT NULL,
    env          TEXT NOT NULL,
    seq          INTEGER NOT NULL CHECK (seq >= 1),
    id           TEXT NOT NULL UNIQUE,
    kind         TEXT NOT NULL,
    event_ms     INTEGER NOT NULL,
    recorded_ms  INTEGER NOT NULL,
    source       TEXT NOT NULL CHECK (source <> ''),
    import_id    TEXT NOT NULL,
    memo         TEXT NOT NULL,
    reverses     TEXT NOT NULL,
    releases     TEXT NOT NULL,
    reservation  TEXT NOT NULL,
    meta         TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    chain        TEXT NOT NULL,
    PRIMARY KEY (book_id, seq),
    FOREIGN KEY (book_id, env) REFERENCES pf_books (id, env)
);
CREATE UNIQUE INDEX pf_tx_import ON pf_tx (book_id, import_id) WHERE import_id <> '';
CREATE INDEX pf_tx_event ON pf_tx (book_id, event_ms, seq);

CREATE TABLE pf_entry (
    book_id  TEXT NOT NULL,
    seq      INTEGER NOT NULL,
    n        INTEGER NOT NULL,
    account  TEXT NOT NULL,
    currency TEXT NOT NULL,
    minor    INTEGER NOT NULL,
    security TEXT NOT NULL,
    qty      TEXT NOT NULL,
    lot      TEXT NOT NULL,
    new_lot  INTEGER NOT NULL CHECK (new_lot IN (0, 1)),
    acquired TEXT NOT NULL,
    PRIMARY KEY (book_id, seq, n),
    FOREIGN KEY (book_id, seq) REFERENCES pf_tx (book_id, seq)
);

CREATE TABLE pf_lot (
    book_id  TEXT NOT NULL,
    env      TEXT NOT NULL,
    id       TEXT NOT NULL,
    security TEXT NOT NULL,
    currency TEXT NOT NULL,
    qty      TEXT NOT NULL,
    cost     INTEGER NOT NULL,
    acquired TEXT NOT NULL,
    open_seq INTEGER NOT NULL,
    PRIMARY KEY (book_id, id),
    FOREIGN KEY (book_id, env) REFERENCES pf_books (id, env),
    FOREIGN KEY (book_id, open_seq) REFERENCES pf_tx (book_id, seq)
);

CREATE TABLE pf_import (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    book_id     TEXT NOT NULL REFERENCES pf_books (id),
    file        TEXT NOT NULL,
    sha256      TEXT NOT NULL,
    rows        INTEGER NOT NULL,
    posted      INTEGER NOT NULL,
    repeated    INTEGER NOT NULL,
    at_ms       INTEGER NOT NULL
);

CREATE TRIGGER pf_tx_next BEFORE INSERT ON pf_tx
WHEN NEW.seq <> COALESCE((SELECT MAX(seq) FROM pf_tx WHERE book_id = NEW.book_id), 0) + 1
BEGIN SELECT RAISE(ABORT, 'pf_tx: the sequence of a book has no gaps'); END;
CREATE TRIGGER pf_tx_active BEFORE INSERT ON pf_tx
WHEN (SELECT status FROM pf_books WHERE id = NEW.book_id) <> 'active'
BEGIN SELECT RAISE(ABORT, 'pf_tx: the book is archived'); END;
CREATE TRIGGER pf_tx_time BEFORE INSERT ON pf_tx
WHEN NEW.recorded_ms < COALESCE((SELECT MAX(recorded_ms) FROM pf_tx WHERE book_id = NEW.book_id), 0)
BEGIN SELECT RAISE(ABORT, 'pf_tx: recorded time cannot go backwards'); END;

CREATE TRIGGER pf_tx_no_update BEFORE UPDATE ON pf_tx
BEGIN SELECT RAISE(ABORT, 'pf_tx is append-only'); END;
CREATE TRIGGER pf_tx_no_delete BEFORE DELETE ON pf_tx
BEGIN SELECT RAISE(ABORT, 'pf_tx is append-only'); END;
CREATE TRIGGER pf_entry_no_update BEFORE UPDATE ON pf_entry
BEGIN SELECT RAISE(ABORT, 'pf_entry is append-only'); END;
CREATE TRIGGER pf_entry_no_delete BEFORE DELETE ON pf_entry
BEGIN SELECT RAISE(ABORT, 'pf_entry is append-only'); END;
CREATE TRIGGER pf_lot_no_update BEFORE UPDATE ON pf_lot
BEGIN SELECT RAISE(ABORT, 'pf_lot is append-only'); END;
CREATE TRIGGER pf_lot_no_delete BEFORE DELETE ON pf_lot
BEGIN SELECT RAISE(ABORT, 'pf_lot is append-only'); END;
CREATE TRIGGER pf_import_no_update BEFORE UPDATE ON pf_import
BEGIN SELECT RAISE(ABORT, 'pf_import is append-only'); END;
CREATE TRIGGER pf_import_no_delete BEFORE DELETE ON pf_import
BEGIN SELECT RAISE(ABORT, 'pf_import is append-only'); END;

CREATE TRIGGER pf_books_no_delete BEFORE DELETE ON pf_books
BEGIN SELECT RAISE(ABORT, 'a book is never deleted'); END;
-- The only change a book gets: a paper book is archived. Nothing about a real
-- book changes, and nothing but the status of a paper book.
CREATE TRIGGER pf_books_archive_only BEFORE UPDATE ON pf_books
WHEN NOT (OLD.status = 'active' AND NEW.status = 'archived' AND OLD.env <> 'real'
          AND NEW.archived_ms IS NOT NULL
          AND NEW.id = OLD.id AND NEW.tenant_id = OLD.tenant_id AND NEW.account = OLD.account
          AND NEW.name = OLD.name AND NEW.env = OLD.env AND NEW.base = OLD.base
          AND NEW.method = OLD.method AND NEW.allow_negative_cash = OLD.allow_negative_cash
          AND NEW.created_ms = OLD.created_ms)
BEGIN SELECT RAISE(ABORT, 'pf_books: only a paper book can be archived, and nothing else changes'); END;
