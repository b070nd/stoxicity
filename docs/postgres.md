# Running on Postgres

By default the program keeps its data in SQLite files (one `base.db` and one file per tenant), and for a single user on one machine that is the recommended setup. It can instead keep the same data in a PostgreSQL database. Both backends are supported and tested; they hold the same data and give the same answers.

## Switching it on

Set one variable, in `.env.local` (never the tracked `.env`):

```
STOXICITY_DATABASE_URL=postgresql://USER@localhost:5432/DATABASE
```

or `database_url` in the configuration. With it set, **everything** uses Postgres: the reference data and every tenant's data. Without it, the SQLite files in `STOXICITY_DATA_DIR` are used. The program never falls back on its own: if the URL is set and the server cannot be reached, the command fails with that message, because quietly writing to the SQLite files would split your history across two databases. Leases, the `company` folder, restore markers and logs stay in the data folder either way.

The database must sort text by bytes, as SQLite does, or the order of results would differ. Create it like this (once, as a role that may create databases):

```
CREATE DATABASE stoxicity_app OWNER stoxicity_owner TEMPLATE template0 ENCODING 'UTF8'
  LOCALE_PROVIDER builtin BUILTIN_LOCALE 'C.UTF-8';
```

The program refuses a database with another collation and says what to run (PostgreSQL 17 or newer for the builtin locale). It uses one schema `base` and one schema `tenant_<id>` per tenant inside that one database. Give it a database of its own: do not share it with another application.

## Moving existing data

With the program stopped, and a backup made first (`./backup.sh backup` while still on SQLite):

```
STOXICITY_DATABASE_URL=postgresql://... stoxicity db import-sqlite
```

copies `base.db` and `tenants/*.db` of the data folder (or `-from DIR`) into the empty database, keeping every id and sequence number, then reads every table back from both sides and compares a hash of its rows in primary-key order. A difference is an error. It refuses a database that already holds rows and SQLite files that are at another schema version than this program. The SQLite files are only read; SQLite may create empty `-wal` and `-shm` files beside them. Keep the files until you are happy; unsetting the variable goes back to them (anything written to Postgres since is not in them).

## Backups

`ops backup` makes a `pg_dump` (custom format, one archive of the whole database, taken in one transaction), restores it into a scratch database, counts every table, records those counts in the manifest and drops the scratch database again. A backup whose dump does not restore, or whose restored rows differ from the live database when that did not change meanwhile, is not kept. `ops verify BACKUP` repeats the restore into a scratch database and compares the rows; without a configured server it can only check the checksum and list the archive, and says that the rows were not checked. `ops restore BACKUP -by NAME` restores into a **new** database named after the configured one (`..._restored_<time>`) and leaves the configured one untouched; it then writes the same restore marker as for SQLite and switches off new orders in the restored database. You switch to it by changing the name in `STOXICITY_DATABASE_URL`, which keeps the old database as the way back.

`pg_dump` and `pg_restore` must be installed (of the server's version or newer). The password in the URL is passed to them in the environment, never on a command line. The role that runs `ops backup` and `ops verify` needs `CREATEDB`. `./backup.sh` refuses to back up or restore while the URL is set, so an old set of SQLite files is never mistaken for a backup. `ops drill` does not yet drill a Postgres database.

## What differs

- **Writes** are serialized per database by an advisory lock, which keeps the single-writer behaviour (and the order of sequence numbers) SQLite gave.
- **Full-text search** uses a generated `tsvector` column. It accepts the expressions `filter.Query.FTS()` produces (words, quoted phrases, `*` prefixes, AND, OR, NOT); a column filter such as `tickers:AAPL` in the match expression is refused. The search is a prefilter and the reader checks every row it returns, so results are the same; accents are folded slightly differently from SQLite's tokenizer.
- **Append-only tables** are enforced by triggers in both (including `TRUNCATE` in Postgres).
- **Migrations** are separate files per backend and kept at the same version numbers: `migrations/sqlite/` and `migrations/postgres/` (the latter start from a baseline at the current SQLite version).
- `ops health` and `doctor` read the migration tables of the Postgres database and nothing else; they report an unreachable server as a failure.

## Tests

`go test ./...` runs on SQLite. With `STOXICITY_TEST_DATABASE_URL=postgresql://user@localhost/postgres` (a role that may create databases) the store tests run on Postgres instead, each in a database of its own that is dropped afterwards, and the importer and backup tests run too.
