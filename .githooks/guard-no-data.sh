#!/bin/sh
# Refuses to let real data leave this machine by accident.
#
#   guard-no-data.sh staged                 what is about to be committed
#   guard-no-data.sh range REVLIST-ARGS...  every commit in the range, e.g. HEAD --not --remotes
#
# It looks at paths, sizes and added lines. It never prints a matching value, only
# where it was found. Exceptions go in .githooks/allow (one exact path per line),
# which is committed and therefore reviewable. Hooks can be skipped with
# --no-verify, so the same script also runs in CI (.github/workflows/ci.yml).
set -eu

root=$(git rev-parse --show-toplevel)
allow="$root/.githooks/allow"
max_bytes=1048576
mode=${1:-}
[ -n "$mode" ] || { echo "usage: guard-no-data.sh staged | range REVLIST-ARGS" >&2; exit 2; }
shift
findings=$(mktemp)
trap 'rm -f "$findings"' EXIT

is_allowed() { [ -f "$allow" ] && grep -qxF -- "$1" "$allow"; }
is_fixture() { case "$1" in */testdata/*|*/fixtures/*|testdata/*|fixtures/*) return 0;; esac; return 1; }

say() { printf '%s\n' "$1" >>"$findings"; printf 'BLOCKED  %s: %s\n' "$1" "$2" >&2; }

raw_diff() {
  if [ "$mode" = staged ]; then
    git -c core.quotepath=off diff --cached --raw --no-abbrev --no-renames --diff-filter=AMC
  else
    git -c core.quotepath=off diff-tree -r --root --no-commit-id --raw --no-abbrev --no-renames --diff-filter=AMC "$1"
  fi
}
patch_diff() {
  if [ "$mode" = staged ]; then
    git -c core.quotepath=off diff --cached -U0 --no-color --no-renames --diff-filter=AMC
  else
    git -c core.quotepath=off diff-tree -r --root --no-commit-id -p -U0 --no-color --no-renames --diff-filter=AMC "$1"
  fi
}

check_path() {
  p=$1
  is_allowed "$p" && return 0
  lower=$(printf '%s' "$p" | tr 'A-Z' 'a-z')
  case "$lower" in
    data/*|*/data/*|backups/*|*/backups/*|bin/*|dist/*|private/*|local/*|research/*|exports/*|export/*|reports/*|report/*)
      say "$p" "a folder that holds real data, backups, exports or build output" ;;
    *.db|*.db-shm|*.db-wal|*.sqlite|*.sqlite3|*.bak|*.dump)
      say "$p" "a database or a copy of one" ;;
    *.pem|*.key|*.p12|*.pfx|*.keystore)
      say "$p" "a key or certificate" ;;
    .env.local|*/.env.local|.env.*.local|*/.env.*.local)
      say "$p" "your private settings and credentials" ;;
    maintenance.md)
      say "$p" "your private maintenance notes" ;;
    *.xls|*.xlsx|*.parquet|*.zip|*.gz|*.tgz|*.7z)
      is_fixture "$p" || say "$p" "a spreadsheet or archive (put real fixtures under testdata/ and invent the numbers)" ;;
    *.csv|*.tsv)
      is_fixture "$p" || say "$p" "a data file (statements, prices, bars, marks, rates); fixtures belong under testdata/ with invented numbers" ;;
  esac
  case "$lower" in
    */testdata/*|testdata/*|*/fixtures/*) ;;
    *policy*.yaml|*policy*.yml|*policy*.json|*fees*.json|*candidates*.csv|*statement*|*stmt*|*portfolio*.json|*holdings*|*broker*.json)
      case "$lower" in *.go|*.md|*.sql) ;; *) say "$p" "looks like your policy, fee schedule, statement, holdings or broker file" ;; esac ;;
  esac
}

check_size() {
  p=$1 sha=$2
  is_allowed "$p" && return 0
  size=$(git cat-file -s "$sha" 2>/dev/null || echo 0)
  [ "$size" -gt "$max_bytes" ] && say "$p" "$size bytes is over $max_bytes: a binary or a data file, not source"
  return 0
}

# Added lines only. Prints "path:line kind", never the value.
check_content() {
  patch_diff "$1" | awk '
    /^\+\+\+ b\// { file = substr($0, 7); next }
    /^\+\+\+ /   { file = ""; next }
    /^@@/        { split($3, a, ","); n = substr(a[1], 2) + 0; next }
    /^\+/ && file != "" {
      line = substr($0, 2)
      lf = file
      skip = (lf ~ /(^|\/)testdata\//) || (lf ~ /_test\.go$/) || (lf ~ /\.sum$/)
      if (!skip) {
        if (line ~ /(api[_-]?key|secret|token|passw(or)?d|webhook)[A-Za-z_]*[ \t]*[=:][ \t]*["\x27]?[A-Za-z0-9\/+_.-]{20,}/ && line !~ /(REPLACE|example|placeholder|your|<)/)
          printf "%s:%d a secret-looking assignment\n", file, n
        if (line ~ /ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16}|sk-[A-Za-z0-9]{24,}|-----BEGIN [A-Z ]*PRIVATE KEY-----/)
          printf "%s:%d a token or private key\n", file, n
        if (line ~ /(^|[^A-Za-z0-9])(U|DU)[0-9]{7,8}([^A-Za-z0-9]|$)/ && line !~ /(DU|U)1234567/)
          printf "%s:%d something shaped like a broker account number\n", file, n
        if (line ~ /(^|[^A-Za-z0-9])[A-Z]{2}[0-9]{2}( ?[A-Z0-9]{4}){3,7}( ?[A-Z0-9]{1,4})?([^A-Za-z0-9]|$)/ && line !~ /(EXAMPL|REPLACE|0000 0000)/)
          printf "%s:%d something shaped like an IBAN\n", file, n
      }
      if (file == ".env" && line ~ /^(export )?[A-Z0-9_]*(KEY|SECRET|TOKEN|PASSWORD|WEBHOOK|CONTACT|MAIL)[A-Z0-9_]*=[^ \t#]+/)
        printf "%s:%d a credential or personal value in the tracked .env (it belongs in .env.local)\n", file, n
    }
    { if ($0 ~ /^\+/) n++ }
  ' | while IFS= read -r hit; do
    path=${hit%%:*}
    is_allowed "$path" || say "${hit%% *}" "${hit#* }"
  done
}

scan() {
  c=${1:-}
  raw_diff "$c" | while IFS="$(printf '\t')" read -r meta path; do
    set -- $meta
    new=$4
    check_path "$path"
    check_size "$path" "$new"
  done
  check_content "$c"
}

if [ "$mode" = staged ]; then
  scan ""
else
  for c in $(git rev-list "$@" 2>/dev/null); do scan "$c"; done
fi

if [ -s "$findings" ]; then
  n=$(sort -u "$findings" | wc -l | tr -d ' ')
  {
    echo
    echo "$n path(s) look like real data, credentials or build output. Nothing was committed or pushed."
    echo "Fix the file, or if it is a deliberate, invented fixture list its exact path in .githooks/allow."
  } >&2
  exit 1
fi
exit 0
