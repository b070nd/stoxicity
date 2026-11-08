# Where the data lives

Databases, backups, the company cache, the Gmail token and the job leases are **real data**. They
belong outside the source folder, so that no `git add`, `git add -f` or editor plug-in can ever see them.

| What | Setting | Recommended value | Default without it |
|---|---|---|---|
| Databases and runtime state (`base.db`, `tenants/`, `company/`, `leases/`, `gmail/`, `restore.json`) | `STOXICITY_DATA_DIR` | `/Users/you/.stoxicity/data` | `data/` beside `.env` |
| Backups (`ops backup`, `backup.sh`) | `STOXICITY_BACKUP_DIR` | `/Users/you/.stoxicity/backups` | `backups/` beside `.env` |

Put both in **`.env.local`** (git-ignored, never published), not in `.env`, which is tracked: an absolute
path with your user name does not belong in a public repository. `.env.local.example` shows the lines.

- Use **absolute paths**. `~` and `$HOME` are not expanded. A relative value starts at the folder of `.env`.
- The folder is created on first use, but create it yourself with owner-only access:
  `mkdir -p -m 700 ~/.stoxicity/data ~/.stoxicity/backups`.
- Order, later wins: `config.yaml` (`data_dir`) < `.env` < `.env.local` < the real environment. Every
  command, `stoxicity-tui` and `backup.sh` read the same two settings; `stoxicity doctor` prints the data
  folder it is using, and `stoxicity ops health` reports its size and free space.
- A restore keeps what it replaces as `<data>.before-restore-<time>` **beside the data folder**, so it
  lands in `~/.stoxicity/` too. A backup mirror (`ops backup -mirror DIR`) should be on another disk.
- Back up the new place the way you backed up the old one; moving the folder moves nothing else
  (the watches and the configuration stay in `app/config/`).

## Moving an existing installation

With every `stoxicity`, `stoxicity-tui` and `laya-serve` process stopped (`pgrep -fl stoxicity`), from the
repository folder:

```sh
mkdir -p -m 700 ~/.stoxicity
mv data ~/.stoxicity/data              # a rename on the same disk: instant, nothing is copied
mv backups ~/.stoxicity/backups
printf '\nSTOXICITY_DATA_DIR=%s\nSTOXICITY_BACKUP_DIR=%s\n' "$HOME/.stoxicity/data" "$HOME/.stoxicity/backups" >> .env.local
stoxicity ops health                   # both databases "current"; the newest backup is listed
stoxicity doctor                       # prints the data folder in use
```
If `.env` still says `STOXICITY_DATA_DIR=./data`, `.env.local` wins; remove the old line when you next
touch `.env`.
