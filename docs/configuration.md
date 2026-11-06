# Configuration

## Where things live

| Kind | Where | In git |
|---|---|---|
| Structure and defaults | `app/config/config.yaml`, `sources.yaml`, `tenants/<id>/` | yes |
| Per-machine settings | `.env` | yes, settings only |
| Credentials, tokens, your email | `.env.local` | **no**, ignored |
| Template for a fresh checkout | `.env.local.example` | yes, empty values |

Order, later wins: `config.yaml` < `.env` < `.env.local` < the real environment.
`-env-file FILE` (repeatable) adds files after `.env.local`. Empty values count
as unset, so the placeholders in `.env.local` override nothing until filled in.
Only `STOXICITY_*` variables and the names given by a source's `auth_env` are
read. The loader is described in `app/src/envfile/envfile.go`.

`.env.local` is created with permissions `600` (owner only). Keep it that way.
`.gitignore` excludes it; check with `git status` that it is never listed.

## Variables

| Variable | File | Secret | Purpose |
|---|---|---|---|
| `STOXICITY_TENANT` | `.env` | no | Tenant to open by default |
| `STOXICITY_TIMEZONE` | `.env` | no | Time zone for days and digest times (`Europe/Berlin`) |
| `STOXICITY_DATA_DIR` | `.env` | no | Database and runtime state |
| `STOXICITY_LOG_LEVEL` | `.env` | no | `debug`, `info`, `warn`, `error` |
| `STOXICITY_LAYA_URL` | `.env` | no | laya-serve address |
| `STOXICITY_LAYA_COMMAND` | `.env.local` | no | program that starts laya-serve (a path on your machine) |
| `STOXICITY_FILE_DIR` | `.env` | no | Folder for the file channel (Markdown, one file per notification); relative paths start at the folder of `.env`. Unset: a watch that names `file` is reported on stderr and its alerts count as delivered |
| `STOXICITY_FEED_PATH`, `_TITLE`, `_URL` | `.env` | no | Feed channel: the Atom file to write, its title and the address it is served from. All three are needed |
| `STOXICITY_PUBLIC_URL` | `.env` | no | Base of links in feed and email; unset for now |
| `STOXICITY_CONTACT` | `.env.local` | personal | Your email or https URL, sent in the User-Agent (the SEC requires it) |
| `STOXICITY_GMAIL_CLIENT_ID` | `.env.local` | personal | OAuth client ID of your Google Cloud project (email channel) |
| `STOXICITY_GMAIL_CLIENT_SECRET` | `.env.local` | **yes** | Its client secret (Google marks it optional for desktop clients) |
| `STOXICITY_GMAIL_REFRESH_TOKEN` | `.env.local` | **yes** | Optional. `stoxicity gmail auth` stores the token in `data/gmail/refresh_token` (owner-only); this variable overrides the file |
| `STOXICITY_MAIL_FROM`, `_TO` | `.env.local` | personal | Sender (your Gmail address) and recipient |
| `STOXICITY_WEBHOOK_URL` | `.env.local` | **yes** | Webhook channel: the https address notifications are posted to (the URL carries the secret) |
| `STOXICITY_LAYA_API_KEY` | `.env.local` | **yes** | laya-serve key, sent as a Bearer token; must equal the sidecar's `LAYA_API_KEY` |

A licensed source adds one variable of its own and names it with `auth_env:`.

Only the channels that some watch names are built, and a channel that cannot
be (a variable is empty) is reported once on stderr by name; fetching goes on,
and the alerts for that channel count as delivered so hits do not wait for ever.
The `tui` channel needs no variable.

## Alerts, digests and fast EDGAR polling

A hit is stamped with the time it was detected, so an article fetched after you
last looked is unread. Every `fetch` and every poll of `run` evaluates only the
articles fetched since the last time, then sends the alerts that came due: one
notification per watch and alert, holding all its new articles. `quiet_hours`
(in the `timezone` of `config.yaml`) and `cooldown` of an alert hold a
notification back until they allow it; nothing is dropped. `run` also checks
once a minute for held alerts and for digests: a watch with
`digest: {cadence: daily|weekly, at: "07:30", channels: [...]}` gets one digest
per day (weekly: Mondays) at or after that time, covering the last 24 hours (7
days).

**Sources are separate resources, fetched in parallel by domain.** `run` polls
sources whose domains differ at the same time (`refresh.workers`, default 16, is
the most polls at once). Sources on one domain are polled at most
`refresh.per_host` at a time (default 2), each domain has its own request rate
limit (2 a second; the SEC's hosts share one limit of 10 a second), and a source
that is waiting for its domain never holds up a source of another. A domain
that shows trouble is left alone for a while, whichever of its sources saw
it: after a `429` for what its `Retry-After` says (at least a minute), after a
`403` for 10 minutes, and after the second failure in a row (network, timeout,
5xx) for a minute, doubling with each further failure up to 30 minutes. Its
other sources wait too (their next-due time moves), one source is tried when the
rest period is over, and one success ends the caution. A `404` or a parse error
concerns one feed and does not make the domain rest. Each source still has its
own backoff and circuit breaker on top.

**Every source before any again.** When several sources are due, the one polled
longest ago goes first and one never polled goes before any, so each source is
polled once before any is polled a second time, however short its interval and
however long the backlog (a slow start, a busy domain, few workers). After a
restart the order continues where it stopped, by the time of each source's last
good poll. A source whose domain is cooling down does not hold the others up;
it simply keeps its place.

**The live board.** When `stoxicity run` has a terminal, it keeps every source
on a board at the bottom of the screen while the result lines scroll above it.
All sources start together (each domain within its limits above), not one after
the other:

```
13:44:47  2/16 polling · 1 waiting · 14 idle · next snb-press in 4:12
   ▶ sec-edgar                polling   0:07
   ▶ boe-news                 polling   1:15
   ◷ boe-news                 late      0:20  (timer ran out while polling; starts again when it ends)
   … fed-press                waiting   0:03
   · snb-press                idle      next in 4:12 · last 13:41:02, 3 new
   ! ecb-press                idle      retry in 2:30 · failed 2× · httpx: throttled ...
```

The first line counts the polls running against `refresh.workers`, the most
that may run at once over all domains (1 to 64, default 16; lower it on a slow
machine or connection), and ends with how the sources are doing once any has
been polled: `health 20 ok, 2 stale, 1 failing` (stale: failed once or twice, or
no good poll for more than two intervals). Sources with work come first, with the time they have been at it (polling), have
been due (waiting) or have been late; the idle ones follow in the order their
timers run out, with the last good poll. A source's timer runs from the *start*
of its poll, so the cadence holds however long polls take. A poll that outlasts
its interval shows as *late* and runs again once it ends, but never two polls of
one source at once and never one run per missed timer, and not sooner than a
quarter of the interval after it ended, so a slow source cannot hammer its
host. *Resting* means waiting for a domain that is cooling down; *parked* is a
source that nothing polls until it is refreshed (a 404). Rows beyond the height
of the terminal are counted ("… 3 more"). Without a terminal (a pipe, a log
file) or with `stoxicity run -plain` there is no board, only one line per poll.

`refresh.fast` in `config.yaml` polls sources of kind `filings` more often
(`interval`, default 2m) on weekdays between `from` and `to` in `zone`
(default 06:00-22:00 New York time, following its daylight saving). It is off
by default.

## Company master

`company:` in `config.yaml` controls the cached lists behind the stock universe
([universe.md](universe.md)): the SEC's list of US-listed tickers
(`https://www.sec.gov/files/company_tickers_exchange.json`: ticker, EDGAR
conformed name, CIK, exchange) and the Nasdaq symbol directory. They do four
things: news is matched to a company by its name as well as its ticker, EDGAR
filings get their tickers (a filing names only a CIK), `stoxicity stock TICKER`
shows the company name, and every ticker gets a profile page.

```yaml
company:
  enabled: true   # fetch and run refresh the cache when it is older than max_age
  max_age: 168h
  url: ""         # a mirror or a test server; empty means the SEC
  listing: true   # also fetch the Nasdaq symbol directory (ETF flag, market tier, venue)
  nasdaq_url: ""  # nasdaqlisted.txt; empty means Nasdaq Trader
  other_url: ""   # otherlisted.txt
  submissions_url: "" # the SEC record of one company; %s is the ten-digit CIK
  refresh_every: 6h   # how often `run` refreshes the reference database; off = never
  enrich_per_cycle: 200 # companies whose industry etc. are fetched in one refresh (0 to 5000)
  feed_search_per_cycle: 60 # company websites read for their news feed in one refresh (0 to 2000)
```

`feed_search_per_cycle` is how many company websites one refresh reads to find the
company's own news feed (see [fetchers.md](fetchers.md#12-the-feeds-of-every-stock));
the SEC filing record of every stock is written down in each refresh whatever it
says. The pools `stock-feeds-sec` and `stock-feeds-sites` in `sources.yaml` read
those feeds; `interval`, `batch` and `cycle` on them set how often a batch is
read, how many feeds it holds and how long a feed rests.

`refresh_every` is a fixed interval (at least 1m, or `off`) with no jitter; the
refresh runs in the background of `stoxicity run` and ends with it. What it
keeps, and how it stays the same from run to run, is in
[universe.md](universe.md#the-reference-database).

- The cache is `data/company/company_tickers.json` plus a small file with the
  ETag and the time it was last checked, and the same pair for each Nasdaq file.
  A refresh is one conditional request per file (the SEC's 10 requests a second
  limit and the contact in the User-Agent apply as for any SEC call); a `304`
  only renews the time. The older `company_tickers.json` shape (no exchange) is
  still read.
- A new list replaces the old one only if it parses and holds at least half as
  many tickers, so a damaged answer cannot wipe a good cache.
- The SEC says the file is updated "periodically" with no guarantee of accuracy
  or scope. Names are EDGAR conformed names, often in capitals (`NVIDIA CORP`);
  the program shows them in title case and still recognises the original
  spelling. Matching by name is a convenience, so measure it before trusting it.
- Where one company has several tickers (share classes, warrants) only the
  shortest ticker is matched by name, so "Alphabet" is one company; the other
  tickers keep their name for display and are found by ticker.
- The watchlist and the tickers in watches are added, so a listing outside the
  SEC's list is found by its ticker.
- The Nasdaq Trader files are for personal, non-commercial use, one local copy,
  not passed on. Set `listing: false` to leave them out.

```
stoxicity company status            # tickers cached, when it was checked
stoxicity company update [-force]   # ask the SEC now
stoxicity company find TEXT...      # search tickers and names
stoxicity universe status|update|enrich|find|show|tag|list   # see universe.md
```

## Topics and sectors

`topics:` and `sectors:` in `config.yaml` tell the ingest step how to file an
article. They feed topic pages, the tag cloud, the `topic:` and `sector:`
filter operators and a watch's `scope.topics` and `scope.sectors`. The shipped
lists (about 16 topics, the 11 GICS sectors) are starting points to edit.

```yaml
topics:
  - name: buybacks-and-dividends   # lower-case slug, unique
    words: [buyback, share repurchase, special dividend]
    tickers: []                    # optional, upper case
sectors:
  - name: energy
    words: [crude oil, refiner]
    tickers: [XOM, CVX]
```

- A **topic** applies when any word or phrase occurs in the title or summary
  (whole words, any case, dashes and curly quotes unified), or any of its
  tickers is among the article's tickers. Topics a source already set are kept.
- A **sector** is the one with the highest score: 3 for each of its tickers
  among the article's tickers, 1 for each distinct word found. A tie, or no hit,
  leaves the sector empty; nothing is guessed.
- Each rule needs a name and at least one word or ticker. At most 200 rules per
  list and 100 words per rule. Prefer phrases to ambiguous single words such as
  "rate" or "bank". With neither key set, nothing is classified.

Rules apply to articles as they are ingested. The classifier has a version (a
hash of the rules, `c1-` and 8 hex characters) and the stored score's version
becomes `<lens version>+<classifier version>`. A score row is never rewritten
for a given version, so editing a rule creates a new score row the next time an
article is ingested. Stored articles are not reclassified until a source
delivers them again.

## Laya: a second opinion

The keyword lens (stage L0) is the base of every score. With `laya.enabled`
the pipeline also asks a local [laya](https://huggingface.co/convaiinnovations/laya)
sidecar (`laya-serve`) about each batch of new articles in one call, and merges
what it says under the policy below. It adds judgements the keywords cannot
make: an event kind for text that matches no rule, a tone (the lens leaves it at
zero), a sector, a topic from your list, a materiality and a relevance.

```yaml
laya:
  enabled: true
  url: http://127.0.0.1:8000
  model: ""               # empty lets laya choose
  min_confidence: 0.7     # the gate; see below
  timeout: 60s            # the first request loads the model
  apply: [kind, topics, tone]
  relevance_weight: 0.3   # used only when `relevance` is in apply
  batch_size: 16          # articles per request, 1 to 64
  device: cpu             # torch device of a server that run starts; "" lets laya choose
  threads: 0              # CPU threads of that server; 0 = torch's choice
  offline: true           # use only checkpoints already downloaded; false for the first start
```

**The confidence gate.** laya returns a confidence with every answer. It is
not calibrated, so `min_confidence` is a starting point to tune, not a
probability of being right. An answer below it never changes a score.

**`apply`.** Only the fields listed may change the score; the allowed names are
`kind`, `tone`, `sector`, `topics`, `materiality` and `relevance`. An unknown
or repeated name is a configuration error. Even when switched on, an answer
only fills a gap:

| Field | Applied when |
|---|---|
| `kind` | the lens found none (`other`) and laya names a real kind, confidently. Materiality becomes at least the kind's base and relevance moves 0.12 per step gained; the noise kinds (`analyst_target`, `price_chatter`) cap relevance at 0.35 |
| `tone` | the lens left it at zero |
| `sector` | the classifier found none; a sector from your `sectors:` is never overridden |
| `topics` | always, added to the topics already set |
| `materiality` | replaces the lens's by the rounded mean of both, relevance moves 0.12 per step |
| `relevance` | blended: `(1 - w) * lens + w * laya` with `w = relevance_weight` |

When laya confidently names another kind than the lens, the lens keeps its
kind and the disagreement is recorded.

**Advisory reasons.** Every answer that did not change the score is still
written to the score's reasons, so nothing laya said is lost:
`laya (advisory): sector technology (0.80)` (switched off, or the lens already
had a value), `laya (unsure): ...` (below the gate), `laya (disagrees): kind
buyback (0.90) against dividend`, and `laya: text truncated` when the article
did not fit the model. Applied answers read `laya: kind results (0.82)`. Use
`stoxicity classify` to see them for one headline, and package `eval` to
compare them with your labels.

**What laya reads.** Only articles without a SEC form: news and press
releases, where there is prose to read. A filing such as `4 - Kenvue Inc.` has
a title and boilerplate and nothing else, and for 8-Ks the lens already reads
the item numbers exactly, so filings are not sent to laya and keep the version
without its part. (On a copy of real data, laya's tone for Form 4 titles was
noise.)

**Failure and versions.** If the sidecar does not answer, the batch is stored
with its rule-based scores and a warning is logged (the address is never
logged). Their version has no laya part, so `stoxicity rescore` later adds the
row with laya's opinion and keeps the old one. A score written with an opinion
has the version `<lens>+<classifier>+laya-<model|auto>-q<8 hex>-p<8 hex>`, and
`-r<8 hex>` after it once the server has told what it runs (the checkpoints it
has loaded, their exact revisions and the device; left out when it cannot tell);
the `q` part changes with the model and the questions, the `p` part
with `apply`, `min_confidence` and `relevance_weight`. A stored row is never overwritten for
a version it already has, so a change of any of these affects the articles
scored from then on, and `stoxicity rescore` adds a row with the new version
for the older ones and keeps the old rows. `batch_size` is not part of the
version because it does not change the answers.

**Started and stopped by `stoxicity run`.** With `laya.manage: true` (the
default), `run` checks whether the server answers at `laya.url`. If it does, it
is left alone, also at the end. If not, and the address is on this machine,
`run` starts `laya.command` bound to that address with `STOXICITY_LAYA_API_KEY`
as its key, waits up to `laya.start_timeout` for it to answer (the first start
loads the models), and stops it again when `run` ends: SIGTERM to its whole
process group, SIGKILL after ten seconds. Its output goes to `data/laya.log`.
`run` records the server's process ID in `data/laya.pid`. If `run` is killed
without a chance to clean up, the server keeps running; the next `run` finds it
answering, takes it over (only if the recorded process still leads its own
process group and its command line names `laya.command`, so a reused process ID
is never signalled) and stops it at its end. If the command is missing or the
server does not come up, `run` says so on stderr and carries on with the rules
alone. `laya-serve` is not part of this repository: set `laya.command`, or
`STOXICITY_LAYA_COMMAND` in `.env.local` for the path of your install (for
example `.../laya/.venv/bin/laya-serve`). Only `run` does this; `fetch` and the
other commands use a server that is already running.

**Repeatable answers.** A server that `run` starts gets the settings that make
the same article score the same on every start: `LAYA_DEVICE` from
`laya.device` (CPU by default; `mps` or `cuda` can differ in the last digits),
`LAYA_THREADS` from `laya.threads`, `LAYA_PRELOAD=1` (the checkpoints are loaded
and named before the first request), `LAYA_AUTO_TASK=0` (no guessing which
checkpoint to use), `PYTHONHASHSEED=0`, and, with `laya.offline`,
`HF_HUB_OFFLINE=1` and `TRANSFORMERS_OFFLINE=1` so that no checkpoint changes
under it. With `laya.model` set it is also the only checkpoint the server loads
and its default. Set `offline: false` for the first start, which downloads the
checkpoints, then back to `true`. A server you start by hand or that was running
already keeps its own settings; the `-r` part of the version tells what it ran.

**Starting the server by hand, safely.** `laya-serve` listens on `0.0.0.0` unless
told otherwise, so bind it to loopback and give it a key:

```
LAYA_HOST=127.0.0.1 LAYA_API_KEY=<a long random value> laya-serve
```

Put the same value in `.env.local` as `STOXICITY_LAYA_API_KEY`. The program
sends it only as an `Authorization` header, never in the address, a log line or
an error message. `STOXICITY_LAYA_URL` in `.env` overrides `laya.url`.
`stoxicity doctor` pings the server; an unreachable sidecar is only a warning,
because the rules work alone.

**How good is it? Not proven.** A probe of the zero-shot answers was mixed: a
restatement was filed under `results`, "analyst raises price target" was read
as negative, Tesla came out as technology with 0.82 confidence, and the
`relevant` question gave 0.04 to 0.17 for everything. That is why the shipped
`apply` list is short, and why sector, materiality and relevance are advisory.
Measure before switching more on:

1. `stoxicity label sample` draws articles to label; `stoxicity label keep|drop ID...`
   records your verdicts.
2. `stoxicity eval` compares the scores, with laya's reasons included, against
   your labels.
3. Add a field to `apply` only when its advisory answers agree with your labels
   better than the rules do, and raise `min_confidence` until they do.

## Time zone

`Europe/Berlin` is Central European Time (UTC+1) in winter and Central European
Summer Time (UTC+2) from the last Sunday of March to the last Sunday of
October. It decides which day an article belongs to, so the timeline, the daily
digest and the digest `at:` times (for example `07:30`) all follow Berlin
clocks. Times are stored in UTC.

One consequence to know: a US filing at 9 pm New York time is 3 am Berlin the
next day, so late US filings count towards the following Berlin day.

## Email through the Gmail API

The email channel sends each alert and digest through the Gmail API with OAuth
2.0, for one Google account. It uses the scope
`https://www.googleapis.com/auth/gmail.send` only: it can send mail on your
behalf and cannot read it. The evidence for every fact below is in
`docs/research/gmail-api.md`.

**Set up once**

1. In the Google Cloud console create a project, enable the Gmail API, configure
   the OAuth consent screen, and create an OAuth client of type **Desktop app**.
   (These console steps are not checked against Google's pages in this project;
   follow Google's current screens.)
2. Put the client ID and secret in `.env.local`, with your address:

   ```
   STOXICITY_GMAIL_CLIENT_ID=...
   STOXICITY_GMAIL_CLIENT_SECRET=...
   STOXICITY_MAIL_FROM=you@gmail.com
   STOXICITY_MAIL_TO=you@gmail.com
   ```

3. Run `stoxicity gmail auth`. It prints an address (and tries to open it). Approve
   "Send email on your behalf". Google sends the browser back to a listener on
   `127.0.0.1` that the command runs for a few minutes, with PKCE and a random
   `state`. The refresh token goes to `data/gmail/refresh_token` (folder 0700,
   file 0600). It is never printed or logged.
4. `stoxicity gmail status` lists which values are set (never their content), and
   `stoxicity gmail test` sends one message to `STOXICITY_MAIL_TO`.

**The seven-day trap.** Google issues a refresh token that **expires after 7
days** to a project whose consent screen has the publishing status *Testing*,
for any scope beyond name, email address and profile. For a long-running
installation, set the status to *In production*. The app is then "unverified"
(`gmail.send` is a sensitive scope), which shows a warning screen you pass with
*Advanced, Go to (project) (unsafe)* and caps new users at 100 over the project's
life; for one person that is no limit. Google states the 7-day rule only for
*Testing* and names no fixed lifetime for production tokens. A token also stops
working when you revoke it, when it was unused for six months, and when you
change the Google password (the token has a Gmail scope).

When Google refuses the token, sending stops with `Google no longer accepts the
stored authorisation; run "stoxicity gmail auth" again`, and the alert counts as
failed and is retried, not dropped.

**Not confirmed.** Google's pages do not say whether the base64url `raw` field
needs `=` padding (the program sends it padded, as the official Python sample
does), what size limit the API has, whether a desktop client may omit the
secret in practice, or the daily limit through the API. For a personal account
Google's help page names "more than 500 emails sent in a day" as the point where
sending can be blocked for 1 to 24 hours; a watch's alerts stay far below it.

The message is plain text, UTF-8, quoted-printable; the subject is encoded, and
text from articles cannot add headers. A notification that would exceed 1 MB
loses articles from its end until it fits.

## Name and domain

The project, module and binary stay `stoxicity`. For a public address,
`stoxi.city` is the candidate you suggested. Checked on 2026-11-04:

| Name | Result |
|---|---|
| `stoxi.city` | `.city` is a working TLD. The name has no name servers, which fits an unregistered name but does not prove it; a WHOIS lookup timed out. Check at a registrar. |
| `stoxicity.com` | Has name servers at GoDaddy: registered by someone |
| `stoxicity.io`, `.app`, `.news`, `stoxi.news` | No name servers; availability unconfirmed |

Nothing is registered or reserved. `STOXICITY_PUBLIC_URL` stays commented out
until you own a domain.

## Platforms

macOS and Linux. Windows is out of scope: there are no Windows build files,
file locking is Unix-only, and the Makefile and CI assume a Unix shell.
