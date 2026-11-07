#!/bin/sh
# Backs up, verifies and restores the data folder (the databases and the company
# files). It uses SQLite's online backup, so it is safe while the program runs,
# and it checks every copy it makes: the integrity check passes, and the row count
# of every table matches the source.
#
#   ./backup.sh backup  [data-dir] [backup-root]   copy to <backup-root>/<UTC time>/   (defaults: data, backups)
#   ./backup.sh verify  <backup-dir>               checksums, integrity check and row counts
#   ./backup.sh restore <backup-dir> [data-dir]    verify, then put the backup in place
#
# restore never deletes: the folder it replaces is renamed to <data-dir>.before-restore-<time>,
# and it refuses to run while stoxicity or stoxicity-tui is running. Run a backup
# before the first run of a new binary: the databases migrate when they are opened,
# and there is no downgrade.
set -eu

die() { echo "backup.sh: $*" >&2; exit 1; }
command -v sqlite3 >/dev/null 2>&1 || die "sqlite3 is needed"
command -v shasum >/dev/null 2>&1 || die "shasum is needed"

counts() { # counts <db>: "table<TAB>rows" for every table, sorted
  db=$1
  sqlite3 "$db" "select name from sqlite_master where type='table' and name not like 'sqlite_%' order by name" |
    while IFS= read -r t; do
      printf '%s\t%s\n' "$t" "$(sqlite3 "$db" "select count(*) from \"$t\"")"
    done
}

check_db() { # check_db <db>: the integrity check says ok
  r=$(sqlite3 "$1" "pragma integrity_check")
  [ "$r" = "ok" ] || die "integrity check of $1 failed: $r"
}

mode=${1:-}
case "$mode" in
backup)
  data=${2:-data}
  root=${3:-backups}
  [ -d "$data" ] || die "no data folder $data"
  stamp=$(date -u +%Y%m%dT%H%M%SZ)
  dest=$root/$stamp
  [ ! -e "$dest" ] || die "$dest exists"
  mkdir -p "$dest"
  (cd "$data" && find . -name '*.db' -type f | sort) | while IFS= read -r f; do
    mkdir -p "$dest/$(dirname "$f")"
    sqlite3 "$data/$f" ".backup '$dest/$f'"
    check_db "$dest/$f"
    counts "$data/$f" >"$dest/$f.counts"
    counts "$dest/$f" | cmp -s - "$dest/$f.counts" || die "row counts of $f differ between the source and its copy"
  done
  if [ -d "$data/company" ]; then cp -Rp "$data/company" "$dest/company"; fi
  (cd "$dest" && find . -type f ! -name MANIFEST | sort | xargs shasum -a 256 >MANIFEST)
  echo "backup written to $dest"
  echo "verify it:  ./backup.sh verify $dest"
  ;;
verify)
  b=${2:-}
  [ -d "$b" ] || die "usage: ./backup.sh verify <backup-dir>"
  [ -f "$b/MANIFEST" ] || die "$b has no MANIFEST"
  (cd "$b" && shasum -a 256 -c MANIFEST >/dev/null) || die "a file differs from its checksum"
  (cd "$b" && find . -name '*.db' -type f | sort) | while IFS= read -r f; do
    check_db "$b/$f"
    counts "$b/$f" | cmp -s - "$b/$f.counts" || die "row counts of $f differ from those recorded at backup"
  done
  echo "backup $b is intact"
  ;;
restore)
  b=${2:-}
  data=${3:-data}
  [ -d "$b" ] || die "usage: ./backup.sh restore <backup-dir> [data-dir]"
  "$0" verify "$b"
  if pgrep -x stoxicity >/dev/null 2>&1 || pgrep -x stoxicity-tui >/dev/null 2>&1; then
    die "stoxicity is running; stop it first"
  fi
  stamp=$(date -u +%Y%m%dT%H%M%SZ)
  if [ -e "$data" ]; then mv "$data" "$data.before-restore-$stamp"; fi
  mkdir -p "$data"
  (cd "$b" && find . -type f ! -name MANIFEST ! -name '*.counts' | sort) | while IFS= read -r f; do
    mkdir -p "$data/$(dirname "$f")"
    cp -p "$b/$f" "$data/$f"
  done
  (cd "$data" && find . -name '*.db' -type f | sort) | while IFS= read -r f; do
    rm -f "$data/$f-wal" "$data/$f-shm"
    check_db "$data/$f"
    counts "$data/$f" | cmp -s - "$b/$f.counts" || die "row counts of $f differ from the backup after restoring"
  done
  echo "restored $b into $data"
  [ ! -e "$data.before-restore-$stamp" ] || echo "the folder it replaced is $data.before-restore-$stamp"
  ;;
*)
  die "usage: ./backup.sh backup [data-dir] [backup-root] | verify <backup-dir> | restore <backup-dir> [data-dir]"
  ;;
esac
