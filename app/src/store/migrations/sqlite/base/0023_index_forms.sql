-- Which forms a day of the EDGAR index was read for. Only the wanted forms of a
-- day are stored, so a day read for 8-K says nothing about 10-Q: when the
-- selection of forms changes, the days that were read for fewer forms are read
-- again. Rows made before this column read as '' (the selection was not
-- recorded) and are read again the next time their day is asked for.
ALTER TABLE filing_index_day ADD COLUMN forms TEXT NOT NULL DEFAULT '';
