-- What an article claims, one claim at a time, and what each claim is read to
-- mean. Both are appended and never rewritten.
--
-- assertion: an observation about the text. A claim is a kind of event, the
-- companies it is about with their roles (parties, JSON), whether the sentence
-- negates or denies it, whether it is about the past, how far the event has come
-- (status) and who vouches for it (attribution). span is the sentence it was read
-- from, verbatim, at span_start..span_end of title + newline + summary. extractor
-- says what found it ("rules-1", or the model that proposed it, with its
-- prompt_version); truncated says the text was cut before it was read. rated is
-- the materiality a filing item states for itself and says nothing of the amount.
CREATE TABLE assertion (
    article_id     TEXT NOT NULL,
    version        TEXT NOT NULL,
    extractor      TEXT NOT NULL,
    seq            INTEGER NOT NULL,
    kind           TEXT NOT NULL,
    method         TEXT NOT NULL,
    item           TEXT NOT NULL DEFAULT '',
    span           TEXT NOT NULL,
    span_start     INTEGER NOT NULL,
    span_end       INTEGER NOT NULL,
    match          TEXT NOT NULL DEFAULT '',
    parties        TEXT NOT NULL DEFAULT '[]',
    negated        INTEGER NOT NULL DEFAULT 0,
    denied         INTEGER NOT NULL DEFAULT 0,
    historical     INTEGER NOT NULL DEFAULT 0,
    anticipatory   INTEGER NOT NULL DEFAULT 0,
    status         TEXT NOT NULL DEFAULT 'unknown',
    attribution    TEXT NOT NULL DEFAULT '',
    magnitude      TEXT NOT NULL DEFAULT '',
    routine        TEXT NOT NULL DEFAULT '',
    rated          INTEGER NOT NULL DEFAULT 0,
    model          TEXT NOT NULL DEFAULT '',
    prompt_version TEXT NOT NULL DEFAULT '',
    truncated      INTEGER NOT NULL DEFAULT 0,
    recorded_ms    INTEGER NOT NULL,
    PRIMARY KEY (article_id, version, extractor, seq)
) WITHOUT ROWID;
CREATE INDEX assertion_recorded ON assertion (recorded_ms);

-- assessment: a reading of one dimension (materiality, novelty, urgency,
-- price_impact) for one company in one article, under a version. They are
-- readings, not facts: a later version, or a later reading under the same one,
-- is another row, and the latest by the time asked is the one in force then.
-- abstained says the evidence did not support a reading; basis says why. seqs are
-- the assertions it rests on.
CREATE TABLE assessment (
    article_id  TEXT NOT NULL,
    ticker      TEXT NOT NULL,
    dimension   TEXT NOT NULL,
    version     TEXT NOT NULL,
    value       INTEGER NOT NULL DEFAULT 0,
    label       TEXT NOT NULL DEFAULT '',
    abstained   INTEGER NOT NULL DEFAULT 0,
    basis       TEXT NOT NULL DEFAULT '',
    seqs        TEXT NOT NULL DEFAULT '[]',
    assessed_ms INTEGER NOT NULL,
    PRIMARY KEY (article_id, ticker, dimension, version, assessed_ms)
) WITHOUT ROWID;
CREATE INDEX assessment_assessed ON assessment (assessed_ms);
