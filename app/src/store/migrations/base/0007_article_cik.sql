-- The company a SEC filing belongs to, by CIK (ten digits). A filing names its
-- filer in the record itself, so its tickers come from this and never from
-- guessing at the text. Filings already stored carry the CIK in their summary
-- ("... filed by NAME (CIK 0001234567), accession ..."); it is read from there.
ALTER TABLE articles ADD COLUMN cik TEXT NOT NULL DEFAULT '';

UPDATE articles
   SET cik = substr(summary, instr(summary, '(CIK ') + 5, 10)
 WHERE form <> '' AND instr(summary, '(CIK ') > 0
   AND substr(summary, instr(summary, '(CIK ') + 5, 10) NOT GLOB '*[^0-9]*'
   AND length(substr(summary, instr(summary, '(CIK ') + 5, 10)) = 10;

-- Filings read from a company's own record (the stock feeds) have no CIK in
-- their summary; their first ticker says which company they belong to.
UPDATE articles
   SET cik = COALESCE((SELECT t.cik FROM ref_ticker t WHERE t.ticker = json_extract(articles.tickers, '$[0]')), '')
 WHERE form <> '' AND cik = '' AND json_valid(tickers) AND json_array_length(tickers) > 0;

CREATE INDEX articles_cik ON articles (cik) WHERE cik <> '';
