# Moving the store from SQLite to PostgreSQL: plan

Written 2026-11-14 from reading the code. Nothing was built and nothing connected to any Postgres.
Every number below was counted in the repository on this date; what I did not read is listed at the end.

## 0. Decision of 2026-11-14: keep both backends

You chose to **keep SQLite and add Postgres**, not replace it. That changes the plan from "port" to "make one store run on two databases", and sections 3 to 9 below are written or amended for it. The consequences, all verified against the code:

- **One method set, two backends.** The 142 methods of `store.DB` are not copied. The SQL text is made **portable**, and only a small set of things differ per backend (section 3.0). Two parallel implementations would drift; a shared one cannot.
- **Most of the dialect gap closes on the SQLite side.** The bundled SQLite is **3.54.1** (modernc v1.61.1 changelog), which already accepts `INSERT ... ON CONFLICT DO NOTHING`, `IS DISTINCT FROM` and `RETURNING`, all of which Postgres accepts too. So `INSERT OR IGNORE` (15 files), `IS NOT` (22), `LastInsertId` (2) and most `INSERT OR REPLACE` sites can be rewritten **once, in portable form, and tested on SQLite alone**, before any Postgres code exists. That first step is shippable by itself and risks nothing.
- **What cannot be made portable** and stays per backend: migrations (`WITHOUT ROWID`, `AUTOINCREMENT`, FTS5, `RAISE(ABORT)` triggers vs identity columns, `tsvector`, plpgsql), the full-text query, scalar `MAX(a,b)`/`MIN(a,b)` (3 sites; Postgres has `GREATEST`/`LEAST`, SQLite has none, so a `CASE` form or a helper), connection setup, the write lock, and `ops` backup/restore/doctor.
- **Every future migration is written twice**, and a test keeps the two in step (section 3.8). This is the permanent cost of the choice.
- **The parity test becomes permanent**, not a one-off: the same fixtures must give the same hashes on both backends, on every CI run.
- **The traps in section 2 stay**, but they only bite on the Postgres side; SQLite behaviour must not change, and the existing SQLite files keep opening unchanged.

## 1. What the store is today

| Fact | Where |
|---|---|
| Two schemas: **base** (63 tables, 31 migrations) and **tenant** (17 tables, 8 migrations) | `app/src/store/migrations/{base,tenant}` |
| One file for base, one file per tenant (`tenants/<id>.db`); a tenant's data cannot be reached through another tenant's handle | `store/db.go`, `store/tenant.go` |
| Per file: one write connection (`_txlock=immediate`) and a read-only pool (`query_only`), WAL, `foreign_keys(1)` | `store/db.go` `dsn()` |
| `store.DB` is one concrete type with about 142 methods, 58 write sites (`BeginTx`/`w.Exec`), in 25 files; 33 other places name `*store.DB` | `store/*.go` |
| Real data is small: `base.db` 228 MB, `tenants/default.db` 576 KB | `~/.stoxicity/data` |
| Migrations are embedded `NNNN_name.sql` files, each run whole in a transaction, recorded in `schema_migrations`; a database newer than the binary is refused untouched | `store/migrate.go` |
| Pure Go, CGO off (`modernc.org/sqlite`); CI runs `go test -race ./...` on ubuntu and macOS, and `no-data` | `go.mod`, `.github/workflows/ci.yml` |
| Times are `INTEGER` unix milliseconds supplied by the application (`db.now()` or the caller), never by the database | everywhere (`ms()`, `fromMS()`) |

Counts of SQLite-specific constructs (files containing them, non-test store code plus migrations):
`WITHOUT ROWID` 22, `AUTOINCREMENT` 15, `INSERT OR IGNORE` 15, `INSERT OR REPLACE` 6, `IS NOT` 22, `rowid` 24,
`ON CONFLICT` 13 (valid in Postgres as written), `MATCH` 11, scalar `MAX(a,b)`/`MIN(a,b)` 3, `LastInsertId` 2,
`?N` numbered placeholders in 7 files, `PRAGMA`/`VACUUM INTO`/`sqlite_master` in `ops` and `store/schema.go`.
Schema: 202 `INTEGER`, 34 `REAL`, 422 `TEXT` columns, no `BLOB`, no `NUMERIC`.

## 2. Traps that would silently change results

These matter more than the syntax work, because the research chain promises byte-identical hashes.

1. **`INTEGER` is 8 bytes in SQLite, 4 bytes in Postgres.** Every `*_ms` column would overflow. All 202 must become `BIGINT`.
2. **`REAL` is 8 bytes in SQLite, 4 bytes in Postgres.** All 34 must become `DOUBLE PRECISION`, never `REAL` or `NUMERIC`.
3. **JSON bodies must stay `TEXT`, not `jsonb`.** `jsonb` reorders keys and normalises numbers; `event_fact.body`, `records.body` and the others are hashed as written.
4. **Text order.** SQLite compares text by bytes. A default Postgres database orders by locale, so `ORDER BY id`, `ORDER BY lens_version DESC` (`base.go` says "compared as text") and every tie-break could reorder rows. The database must be created with `LC_COLLATE 'C'` (or every column `COLLATE "C"`), and a test must fail if it is not.
5. **Application time stays application time.** `recorded_ms` is what as-of reads filter on. Do not use `now()`, `timestamptz` or `DEFAULT` clocks: tests use a fake clock and hashes include these values. A generated `timestamptz` column for ad-hoc SQL can be added later, beside, not instead.
6. **Looser typing disappears.** SQLite accepts a text value in an integer column. Postgres refuses. Expect a few latent bugs to surface; that is a benefit but it costs time.
7. **Booleans stored as 0/1.** `sql.NullBool` in `tenant.go`, `SUM(basis = 'known')` in `eventfacts.go:227`: Postgres needs `SUM((basis = 'known')::int)`, and the stored column type must be decided per column.
8. **`INSERT OR REPLACE` is delete-then-insert** in SQLite (it fires delete triggers and cascades). The 6 sites must each be read and turned into `ON CONFLICT DO UPDATE` deliberately, not mechanically.
9. **Float digests are already architecture-dependent** (commit `76a94d4`). The parity test in section 8 must compare on one machine.

## 3. Decisions the code forces

**3.0 The shape of two backends in one package.** `handle` already hides "one write pool, one read pool" and `openHandle` is the only place that knows the database kind. Plan:
- A small unexported `dialect` value on each `handle`: `rebind(sql)` (turns `?` and `?N` into `$N` for Postgres, identity for SQLite), `writeTx(ctx, fn)` (SQLite: begin as today; Postgres: begin, then `pg_advisory_xact_lock`), `search(...)` for `SearchIn`/`Search`, `leastGreatest`, and the migration directory. Everything else in the 25 store files calls these and otherwise stays as is.
- The 58 write sites go through `writeTx`; the 142 methods keep their signatures, so the 33 outside references to `*store.DB` and the `Store` interface do not change.
- Rebind is now **justified** (the same SQL text has to run on both), which reverses the recommendation in 3.7 below. It is one function applied where statements are prepared or executed, with a test that every `?N` pattern in the repository rebinds correctly.
- Selection: `STOXICITY_DATABASE_URL` set (from `.env.local` or the environment) → Postgres; unset → SQLite in the data folder exactly as today. `stoxicity doctor` and `ops health` print which backend is in use. A mixed state (URL set and `base.db` present with newer data) gets a loud warning, never a silent choice.
- Tenants keep the same rule on both: SQLite file per tenant, Postgres schema per tenant (3.2).

**3.1 One write lock, to keep today's semantics.** SQLite's `_txlock=immediate` makes every write transaction exclusive; code relies on it. Examples: `Save` computes `MAX(seq)`, `MAX(change_seq)` and `MAX(fetched_ms)` and stamps new articles later than every stored fetch time so no consumer cursor can miss one (`base.go:134`); `PutEventFacts` reads the latest revision and writes `rev+1`; `pf_tx` triggers check "no gap in the sequence" and "recorded time cannot go backwards" with a `MAX()` subquery. Under Postgres's default isolation two concurrent writers would race on all of these.
Plan: every write transaction begins with `pg_advisory_xact_lock(<key of this database>)`. That is exactly today's behaviour (one writer at a time, readers never blocked, MVCC) and needs no per-site analysis. Relaxing it per table is possible later and is a separate decision. `SERIALIZABLE` with retries was rejected: 58 sites would each need retry logic and the cursor guarantee would be harder to argue.

**3.2 Tenants: one schema per tenant, one pool per tenant.** Keeps the current shape: `tenantHandle` caches a handle, the handle has a write pool and a read pool, and `model.ValidID` already guards the name. Each tenant pool sets `search_path=tenant_<id>` as a connection parameter, so SQL text stays unqualified and a handle cannot see another tenant's tables. Rejected: a `tenant_id` column plus row-level security (touches every query and every unique key, and weakens the isolation rule). Base lives in schema `base`.

**3.3 Read pool read-only.** The read pool sets `default_transaction_read_only=on`, the equivalent of `query_only`.

**3.4 Append-only is enforced twice.** Today 37 `CREATE TRIGGER` statements; most are `BEFORE UPDATE`/`BEFORE DELETE` → `RAISE(ABORT, '<table> is append-only')`. In Postgres: one `plpgsql` function taking the table name as an argument, `BEFORE UPDATE OR DELETE ... FOR EACH ROW` per table, **plus `BEFORE TRUNCATE`** (row triggers do not see `TRUNCATE`), **plus** the application role gets no `UPDATE`, `DELETE` or `TRUNCATE` privilege on those tables. The privilege layer is stronger than SQLite could ever be. The non-trivial triggers (`pf_tx_next`, `pf_tx_active`, `pf_tx_time`, `pf_books_archive_only`, the `policy_*` and `order_*` ones, the `0024_instruments` six) need individual porting and a test each; they already have SQLite tests to copy from.

**3.5 Full-text search.** `articles_fts` is FTS5 over title, summary, tickers. The match string comes from `filter.Query.FTS()`, which double-quotes every token and is documented as a **prefilter**: the reader then checks every candidate exactly with `q.MatchIn` (`reader.go:150-184`). So Postgres only has to return a **superset** of the right rows, which makes this much safer. Plan: generated `tsvector` column (config `simple`, to match FTS5's unaccented-case-folded tokens without stemming) with a GIN index; translate the small FTS5 subset that `FTS()` emits (quoted tokens, `OR`, `AND`/space, `NOT`, column filters, prefix `*`) into a `tsquery`. The direct `Search` method that takes a raw FTS5 string (`base.go:289`) and its `tickers:AAPL` examples were decided (decision 4): on Postgres that method accepts only the `FTS()` subset.

**3.6 Primary keys.** `INTEGER PRIMARY KEY` (rowid alias, `articles.seq`) becomes `BIGINT GENERATED ALWAYS AS IDENTITY`; the 15 `AUTOINCREMENT` tables likewise. `LastInsertId` (2 sites: `filing_index.go:243`, `policy.go:78`) becomes `INSERT ... RETURNING`. Because writers are serialised by 3.1, identity values are assigned in commit order, which the change-number cursors need; without 3.1 a sequence can commit out of order.

**3.7 Driver.** `github.com/jackc/pgx/v5` through its `database/sql` adapter (`pgx/v5/stdlib`): pure Go, no libpq, CGO stays off, and the 142 methods keep `*sql.Tx`. Native pgx (`COPY`) only for the importer and the bulk candle/bar loads, where it is much faster. Placeholders: `?` and `?N` become `$N`; either a rebind step at `Prepare`/`Query` time (cheap, one function, but a hidden rewrite) or editing the SQL in place (about 450 statements, explicit). With both backends kept, the recommendation changes: edit the statements **once into the portable subset** (section 0) **and** rebind `?` to `$N` at the handle, because one text must run on both.

**3.8 Migrations.** The 39 SQLite files cannot run on Postgres. Layout becomes `migrations/sqlite/{base,tenant}` (the existing files, moved, **never edited**) and `migrations/postgres/{base,tenant}`. Postgres gets a squashed **baseline** `0001_baseline.sql` per schema equal to base 31 / tenant 8 (written once, by hand, with the traps of section 2), then `0002+` forward. **Lockstep rule:** from the baseline on, a new migration is added to both directories in the same commit, with the same version number and name. A test enforces it: both directories have the same version list, and, with a Postgres available, the introspected tables, columns (through a fixed type map: `INTEGER`→`bigint`, `REAL`→`double precision`, `TEXT`→`text`), primary keys, indexes and trigger names are equal on a freshly migrated copy of each. The squash means the numbering of the Postgres baseline is "31", so a Postgres database and a SQLite database at the same schema version have the same content model. `schema_migrations` keeps the same shape (`version`, `name`, `applied_ms`), and "database newer than the binary: refuse untouched" must keep working, checked before any DDL. Postgres can run a multi-statement file only through the simple query protocol, so the migrator uses a dedicated connection with `QueryExecModeSimpleProtocol`. Concurrent starts (TUI + CLI + daemon) take `pg_advisory_lock` around migration so two processes never migrate at once. Because the version numbers of the baseline equal the SQLite ones, the importer (section 5) can refuse a source at the wrong version.

**3.9 What stays outside the database.** Job leases (`ops/lease.go`, files with exclusive create), `company/`, `gmail/`, logs, `restore.json`. They are not SQL and keep working. `STOXICITY_DATA_DIR` shrinks to "files that are not the database".

## 4. The part that is more than the store: `ops`, doctor, scripts

With both backends kept, `ops` gets a small interface (`snapshot`, `verify`, `counts`, `restore`) with a SQLite implementation (the current code, moved behind it, unchanged) and a Postgres one. `backup.sh` picks the path by the same setting; the backup folder records which backend a backup came from, and `restore` refuses a backup of the other kind rather than guessing. The rest of this section describes what the Postgres implementation replaces.

The store layer is about 13,600 lines; but backup and restore are written against SQLite files and **do not work on Postgres unchanged**:

- `ops/backup.go`: `PRAGMA integrity_check`, `VACUUM INTO`, `sqlite_master` table counts, a fingerprint made of file sizes and mtimes of `*.db` and `*.db-wal`.
- `ops/restore.go:112`, `ops/drill.go`, `ops/health.go`, `cmd_ops.go:410`: assume `base.db` and `tenants/<id>.db`.
- `store/schema.go` `ReadSchema` opens the file read-only for `doctor`; `cmd_doctor.go:137-152` prints "base.db is at schema N".
- `backup.sh` and `docs/data-location.md`, `docs/operations.md`.
- `README.md:37` ("One embedded SQLite database per install, no account and no cloud service") stays true by default and gains one sentence: Postgres is optional.

Postgres equivalents: `pg_dump --format=custom` per schema (consistent snapshot, safe while running), `pg_restore --list` plus a restore into a scratch database as the verify step (the existing `drill` idea), row counts per table from the restored copy, `amcheck`/`pg_amcheck` or a full `pg_dump` read for integrity. The "restore never deletes, renames the old data to `.before-restore-<time>`" rule becomes "restore into `stoxicity_restore_<time>`, verify, then swap by renaming databases". These tools are separate binaries (`pg_dump`, `pg_restore`) that must be on the PATH: new dependency for the user and for CI.

`doctor` must still not open the database (standing rule): `ReadSchema` becomes a single read-only query, with a short connect timeout, and a clear "Postgres is not running" line instead of a failure of the whole report.

## 5. Moving the existing data

A one-off `stoxicity db import-sqlite -from <data-dir> -dsn-env STOXICITY_DATABASE_URL`:

1. Refuse unless the source `schema_migrations` equals the binary's known version (31 / 8) and the target is empty and at the same version.
2. Open each SQLite file read-only; copy table by table in foreign-key order with `COPY`, **preserving `seq`, ids and every `*_ms` value**; keep `pf_tx` in `(book_id, seq)` order so its triggers validate the data as it arrives (a free integrity check). Then `setval` every identity sequence to the maximum, and build the `tsvector` column and indexes after the load.
3. Verify: per-table row counts and a per-table content hash over the primary key order; the same on the Postgres side.
4. Never writes to the source. The SQLite files stay where they are as the rollback.
5. 231 MB: minutes. It runs against a scratch database first.

## 6. Test strategy

- The store has 118 tests and about 20 other packages call `store.Open(t.TempDir())` (pipeline, workspace, reader, reconcile, plan, refdata, cmd tests). A harness `storetest.Each(t, func(t, open))` runs a test **once per available backend**: always SQLite (as today, temp folder), and Postgres when `STOXICITY_TEST_DATABASE_URL` is set, from a **template database that already has the schema** (fast, parallel-safe), dropped on cleanup, C collation checked.
- Because SQLite always runs, the required checks can never pass vacuously. Postgres runs are skipped locally without the setting (printing why); in the CI Postgres job a missing setting is a **failure**.
- Tests are moved to `storetest.Each` in risk order; the other packages only change if they touch backend behaviour (most can stay on SQLite; the ones that exercise the research chain run on both).
- CI: ubuntu gets `services: postgres`. **macOS runners have no service containers.** Cheapest: the macOS job stays SQLite-only and one added ubuntu job runs the Postgres matrix; this adds a check, so the ruleset (required `test (macos-latest)`, `test (ubuntu-latest)`, `no-data`) only needs a change if you want the new job to be required, which is yours to decide.
- Port, in this order: migrate tests, append-only trigger tests (including `TRUNCATE` and the privilege layer), as-of read tests (`valuation_asof_test.go`, `eventfacts_test.go`), then the rest.
- **Parity test, the strongest one, permanent:** run the same fixtures on both backends in one process; the spec hash, run hash and report digest must be identical, and `EventFacts` with `AsOf` must return identical sets for several past instants, ordered the same. This exercises collation, float width, JSON text and as-of reads end to end. A second, one-off run of it against your real data is the cut-over gate (section 8).

## 7. Where Postgres runs: your machine's shared instance

- The instance hosts other apps. The plan uses **its own database** `stoxicity` (the one in the DSN) and its own role for the program, never the superuser or the role that owns other apps' data; extensions are not required (no `pg_trgm`, no `unaccent`).
- Two roles: an **owner** that runs migrations and creates objects, and an **app** role with `SELECT/INSERT` (and `UPDATE` only on the few mutable tables), as the privilege layer of 3.4 needs.
- The DSN has no password in what you gave me (local trust). It must live in `.env.local` (git-ignored) or the real environment, **never in `.env`**, which is tracked. The program would read one setting, for example `STOXICITY_DATABASE_URL`, with the same precedence as the data dir.
- Your container is stopped. **While `STOXICITY_DATABASE_URL` is unset, nothing changes and nothing needs Postgres.** Once you set it, every command and the TUI need the server running; they must fail at start with one clear line ("Postgres at <host> is not reachable"), never fall back to SQLite silently, because a silent fallback would split your history across two databases.
- Check before the first connection, read-only: does a database named `stoxicity` already hold anything (the DSN names it, but it might be used by another app), and what are its collation and encoding. If it is not `C`, create a second database (decision 3).

## 8. Order of work (when you say go)

Each step ends green on SQLite and is shippable on its own; Postgres appears only in step 3.

1. **Portable SQL on SQLite only.** Rewrite the dialect-bound statements into the portable subset (`ON CONFLICT DO NOTHING`, `IS DISTINCT FROM`, `RETURNING`, `CASE` instead of `SUM(bool)`; each of the 6 `INSERT OR REPLACE` sites read and decided one by one). Add the `dialect` value and `writeTx` with SQLite behaviour identical to today. No Postgres code, no new dependency. Existing 118 store tests and the pinned report hashes must not change.
2. **Test harness.** `storetest.Each`, the lockstep test skeleton, the rebind function with its test over every placeholder pattern in the repository.
3. **Postgres backend.** `pgx` dependency, `openHandle` for Postgres (pools, `search_path` per tenant, read-only pool), advisory write lock, migrator with advisory lock, baseline schemas (base 31, tenant 8), `ReadSchema`, FTS (`tsvector` and the `FTS()` translator), then the trigger ports with a test each. Verified on a scratch database.
4. **Run the whole store suite on both backends**, fix what the traps of section 2 reveal, add the permanent parity test.
5. **Importer** `db import-sqlite` with count and content-hash verification.
6. **`ops` interface** with the SQLite implementation moved behind it and the Postgres one added; doctor, `backup.sh`, docs, README.
7. **CI** ubuntu Postgres job.
8. **Cut over your real data** (only when you say so): `./backup.sh backup` and verify, close the TUI, import, run the parity check on the real data, then set `STOXICITY_DATABASE_URL` in `.env.local`. The SQLite files stay untouched as the way back.

Rough size: step 1 is small and the safest start; steps 3-4 are the bulk (about 25 store files, 2 baseline schemas, 142 methods, about 37 trigger ports); step 6 is comparable to one store group. The permanent cost of keeping both is every later migration written twice and every new SQL statement checked on both.

## 9. Decisions (all answered 2026-11-14)

1. **Backends:** keep both (section 0).
2. **Default:** SQLite when `STOXICITY_DATABASE_URL` is unset.
3. **Database:** a new dedicated database (for example `stoxicity_app`, `C` collation) created by the owner; the shared `stoxicity` database is not inspected or used.
4. **Raw FTS search:** on Postgres `Search` accepts only the subset `filter.Query.FTS()` emits; any other string is a clear error.
5. **CI:** a separate ubuntu Postgres job, not a required check at first.
6. **Roles:** an owner role (migrations) and a limited app role (no `UPDATE`/`DELETE`/`TRUNCATE` on append-only tables); the owner runs the `CREATE ROLE` statements, which are written in the plan's step 3.
7. **Backups:** `pg_dump` and `pg_restore` on the PATH are acceptable.
8. **Lockstep:** every new migration is written for both backends in the same commit, enforced by a test.

Nothing is left open. Nothing has been built; step 1 (portable SQL on SQLite only) starts when the owner says go.

## 10. What I did not read

- The SQL bodies of every store file beyond the ones named above (I counted constructs by search; I read `db.go`, `migrate.go`, `schema.go`, `base.go`, `tenant.go`, `eventfacts.go`, `visible.go` `SearchIn`, `ops/backup.go` excerpts, `ops/lease.go` head, migrations 0001, 0029 and the portfolio triggers).
- The complete `ops/restore.go`, `ops/drill.go`, `ops/health.go`.
- The `0024_instruments`, `0025_observations`, tenant `0005`/`0008` trigger bodies (their guards need individual porting; counted, not read).
- Which of the 6 `INSERT OR REPLACE` sites rely on delete cascades.
- The planner behaviour of the as-of `ROW_NUMBER()` windows on 228 MB of data in Postgres (a `DISTINCT ON` form is likely faster; measure after the port, do not guess).
