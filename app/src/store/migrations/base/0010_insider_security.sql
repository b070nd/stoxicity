-- A Form 4 trade now keeps the title of the security (a purchase of preferred
-- stock is not one of the common stock), the form (4 or its amendment 4/A) and
-- the period it reports, which is how an amendment finds what it replaces.
-- Filings read before are read again: the rows lack these and cannot be told
-- from a common-stock trade.
ALTER TABLE insider_trade ADD COLUMN security TEXT NOT NULL DEFAULT '';
ALTER TABLE insider_trade ADD COLUMN form TEXT NOT NULL DEFAULT '4';
ALTER TABLE insider_trade ADD COLUMN period TEXT NOT NULL DEFAULT '';
DELETE FROM insider_trade;
DELETE FROM insider_filing;
