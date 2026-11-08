# Operations and recovery

How to run the program unattended, keep it healthy, back it up and get it back. Everything here is the `ops` command, plus one change to how `alerts deliver` runs.

```
stoxicity ops health [-json]            stoxicity ops backup [-to DIR] [-mirror DIR] [-keep N] [-keep-weeks N]
stoxicity ops metrics [-out FILE]       stoxicity ops list | verify BACKUP
stoxicity ops leases                    stoxicity ops restore BACKUP -replace-data -by NAME
stoxicity ops service launchd|systemd   stoxicity ops recover | reconciled -by NAME
stoxicity ops drill [-work DIR] [-keep] [-target-backup D -target-restore D -target-open D]
```

None of these opens a database through the application's store, so none of them migrates one. Databases are read through read-only connections and copied with SQLite's own snapshot (`VACUUM INTO`); a live database is never written. (`ops restore` and `ops reconciled` open the restored data afterwards, on purpose, to switch off orders and to check the broker records.)

## Health and metrics

`ops health` shows each database (size, write-ahead log, schema applied of the schema this program knows, and whether it is `current`, `behind` or `ahead`), free disk, the data folder size, the newest backup and its age, the job leases, and whether a restore is waiting to be reconciled. It exits with an error only when the state is `fail`: a database written by a newer program, free space under the limit, a data folder over its limit, or a restore that stopped half way. `ops metrics` prints the same as Prometheus text (or writes a file for a collector that reads files). Both are redacted: databases are `base`, `tenant-1`, `tenant-2`; there is no path, tenant name, address, key, lease holder or free text. A test checks that.

## Limits

`STOXICITY_MIN_FREE_MB` is the free space the disk must keep; `STOXICITY_MAX_DATA_MB` is the most the data folder may hold. Neither is set by default (no limit). When one is reached, `fetch` and `ops backup` are refused with the reason, and `ops health` fails. Cancelling an order, reconciling, delivering an alert and recording in the books are never refused for space. A value that is not a whole number of megabytes is an error, not "no limit". These are the only quotas: nothing here limits the requests made to a data provider.

## Job leases

A lease is a small file in `data/leases/` naming the job, its owner, an expiry and a generation number that only grows. `alerts deliver` (and `alerts scan -deliver`) takes `alerts-deliver-TENANT` for ten minutes, and `ops backup` takes `backup` for an hour, so two copies cannot deliver the same alerts or back up at once. A job that dies stops renewing, the lease runs out, and the next job takes it over with a higher generation; the old owner finds out it lost the lease when it next renews. Taking over is decided by creating the next generation's marker file exclusively, so two jobs that both see an expired lease cannot both win. Leases are advisory: they stop two copies of this program from colliding, not a program that ignores them. `fetch`, `plan generate` and the other commands do not take leases.

## Backups

`ops backup` copies every database (`base.db` and each `tenants/*.db`) and the `company` folder to `backups/<UTC time>/` (or `STOXICITY_BACKUP_DIR`, or `-to`). The folder is built as `<time>.partial` and renamed when complete, so a stop in the middle leaves nothing that looks like a backup.

Coherence: the databases are copied one after another, so the set is one moment of the data folder only if nothing changed in between. The command fingerprints every database (size and time of the file and its log, and the rows of every table) before and after; if it differs, the set is copied again, up to three times, and then refused. `-allow-incoherent` keeps such a set, marked `coherent: false` everywhere it is shown. The fingerprint sees commits; it does not stop writers, and an update that changes neither a size, a time nor a row count could go unseen.

The manifest (`MANIFEST.json`, format `stoxicity.backup/1`) lists every file with its SHA-256 and size and, for each database, the schema it was at and the rows of every table. The shell tool's own files (`MANIFEST`, `*.counts`) are written too, so `./backup.sh verify` and `./backup.sh restore` read a backup made here.

`ops verify` checks every checksum, SQLite's integrity check, the rows of every table, that no file is unlisted, and that no database was written by a newer program than this one. `ops backup` verifies what it just wrote before saying it is done.

Rotation: `-keep N` after a good backup keeps the newest N, the newest of each of the last `-keep-weeks` calendar weeks, the backup just made and never the newest; it removes the rest. Without `-keep` nothing is removed. Only folders named like a backup that hold a manifest are touched.

Independent copy: `-mirror DIR` copies the backup there and verifies the copy in place. The mirror is never rotated by this command. If the mirror is on the same filesystem as the backup, the command says it guards against deletion and not against the disk failing. It cannot tell whether a folder is on another machine, only another device.

## Restore

`ops restore BACKUP -replace-data -by NAME`, with the program stopped:

1. An earlier restore that stopped half way is finished or undone (`ops recover` does the same on its own).
2. The backup is verified in full; if it does not verify, nothing is changed.
3. The program must not be running: no other `stoxicity` or `stoxicity-tui` process (found with `pgrep`; if `pgrep` is missing the restore refuses), no unexpired lease, no database held for writing.
4. The backup is copied into a staging folder beside the data folder, the databases are checked again there (integrity and rows), and the manifest is copied into it as `restored-from/`.
5. The data folder is renamed to `data.before-restore-<time>` (nothing is deleted) and the staging folder takes its place.

A stop before step 5 leaves the data folder untouched. A stop between the two renames leaves it missing: `ops recover` (or the next restore) puts back the complete staged folder, or else the one set aside; `ops health` says so.

Then the restored folder is not trusted to be current. What a backup holds is how things were; the broker may have moved. Therefore, `ops restore` writes `restore.json` (state `needs_reconcile`), switches off new orders for `real` and `paper_broker` books in every tenant, and from then on every order gate (command line and terminal interface) refuses new risk (a buy) until `ops reconciled -by NAME` succeeds. Selling, cancelling and reporting stay possible. `ops reconciled` checks, for every book bound to a broker in every tenant, that a `broker sync` was made after the restore and that no difference with the broker is open; if not it lists what is missing and changes nothing. With no book bound to a broker it says so and marks the restore reconciled on your word. Switching new orders on again is a separate, manual step (`order control enable`), as after any restart.

## Service files

`ops service launchd|systemd` writes (or prints) the definitions for two jobs: `alerts scan -deliver` every 15 minutes and `ops backup -keep 7 -keep-weeks 4` daily at 02:30 (with `-mirror` if you give one). It installs nothing, enables nothing and reads no setting: the systemd units name your settings file as `EnvironmentFile`. Existing files are not replaced.

## Drills

`ops drill` runs the recovery procedures on copies in a temporary folder (outside the data folder; the live databases are only read) and reports what each took:

| Drill | What it shows |
|---|---|
| backup | a coherent backup, its time, size and copy window |
| verify | the checks agree |
| restore | restore into an empty folder, the restore marker and the carried manifest |
| open-and-migrate | opening the restored copy through the store; how many migrations it applied |
| interrupted-restore | a stop after verifying, after staging and between the two renames each leaves a complete folder (the last after `recover`) |
| interrupted-backup | a stop mid-backup leaves a `.partial` folder that is not listed and does not verify; the next backup completes |
| timeouts | a backup and a restore past their deadline fail cleanly and change nothing; an expired lease is taken over |
| mirror | the verified second copy, and whether it is on the same device |

Targets are `-target-backup` (default 5 min), `-target-restore` (15 min) and `-target-open` (5 min), and a drill that misses one fails with the figure. They are starting points, not promises. Measured on this machine (macOS, local SSD) on a fixture workspace padded to 262 MiB: backup 1.07 s (copy window 0.75 s), verify 0.30 s, restore into an empty folder 0.85 s, recovery after a stop between the renames 0.75 s, opening the restored copy 0.01 s. On the unpadded fixture (0.7 MiB) every drill takes under 0.2 s. These say nothing about a larger folder, a slower disk or a network mirror: run the drill where you will use it.

The recovery point is set by how often backups run, not measured by the drill: with the daily job, up to a day of records can be lost, less the broker facts that `broker sync` applies again after a restore.

## Limits

- The migration drill cannot exercise a real migration: there is no database from an older schema to copy, so it reports "no migration was pending" for a current folder. A backup from an older schema is accepted and migrates when opened; that path is covered by the store's own migration tests, not by this drill.
- Leases are advisory and only two jobs take them.
- The coherence check is a fingerprint of commits, not a lock.
- The process check relies on `pgrep` and the process names `stoxicity` and `stoxicity-tui`.
- Disk and data-size limits only. No request quotas.
- Unix only (free space comes from `statfs`).
- Nothing starts, supervises or restarts the program; the service files only schedule the two jobs.
- Reconciliation after a restore is as good as `broker sync`, which is verified against a fixture broker only (see docs/broker.md).
