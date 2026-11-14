-- What the Form 4 filings say: who in a company bought or sold its shares, and
-- at what price. A filing is read once (insider_filing remembers it, even if it
-- held no trade in ordinary shares, or could not be read), and its trades are
-- one row each. Only the last year is kept.
CREATE TABLE insider_filing (
    accession TEXT PRIMARY KEY,           -- 0000320193-26-000030
    cik       TEXT NOT NULL,              -- the issuer, ten digits
    filed     TEXT NOT NULL,              -- YYYY-MM-DD
    read_ms   INTEGER NOT NULL,
    trades    INTEGER NOT NULL DEFAULT 0,
    status    TEXT NOT NULL DEFAULT 'ok', -- ok | bad (could not be read, not asked again)
    error     TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;

CREATE TABLE insider_trade (
    accession TEXT NOT NULL,
    seq       INTEGER NOT NULL,           -- 1.. within the filing
    cik       TEXT NOT NULL,              -- the issuer, ten digits
    owner     TEXT NOT NULL,
    owner_cik TEXT NOT NULL DEFAULT '',
    role      TEXT NOT NULL DEFAULT '',
    traded    TEXT NOT NULL,              -- day of the transaction, YYYY-MM-DD
    filed     TEXT NOT NULL,
    code      TEXT NOT NULL,              -- P, S, A, F, M, G ...
    acquired  INTEGER NOT NULL,           -- 1 got the shares, 0 gave them up
    shares    REAL NOT NULL,
    price     REAL NOT NULL DEFAULT 0,
    held      REAL NOT NULL DEFAULT 0,    -- shares owned afterwards
    direct    INTEGER NOT NULL DEFAULT 1,
    plan      INTEGER NOT NULL DEFAULT 0, -- under a Rule 10b5-1 plan
    PRIMARY KEY (accession, seq)
) WITHOUT ROWID;

CREATE INDEX insider_trade_cik ON insider_trade (cik, traded DESC);
CREATE INDEX insider_trade_traded ON insider_trade (traded);
