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
This directory is not a git repository yet; `.gitignore` already excludes
`.env.local`, so run `git init` after reading this and check with
`git status` that the file is not listed.

## Variables

| Variable | File | Secret | Purpose |
|---|---|---|---|
| `STOXICITY_TENANT` | `.env` | no | Tenant to open by default |
| `STOXICITY_TIMEZONE` | `.env` | no | Time zone for days and digest times (`Europe/Berlin`) |
| `STOXICITY_DATA_DIR` | `.env` | no | Database and runtime state |
| `STOXICITY_LOG_LEVEL` | `.env` | no | `debug`, `info`, `warn`, `error` |
| `STOXICITY_SMTP_HOST`, `_PORT` | `.env` | no | Mail server for the email channel |
| `STOXICITY_LAYA_URL` | `.env` | no | laya-serve address |
| `STOXICITY_FILE_DIR` | `.env` | no | Folder for the file channel (Markdown, one file per notification); relative paths start at the folder of `.env`. Unset: a watch that names `file` is reported on stderr and its alerts count as delivered |
| `STOXICITY_FEED_PATH`, `_TITLE`, `_URL` | `.env` | no | Feed channel: the Atom file to write, its title and the address it is served from. All three are needed |
| `STOXICITY_PUBLIC_URL` | `.env` | no | Base of links in feed and email; unset for now |
| `STOXICITY_CONTACT` | `.env.local` | personal | Your email or https URL, sent in the User-Agent (the SEC requires it) |
| `STOXICITY_SMTP_USER`, `_PASSWORD` | `.env.local` | **yes** | Mail account |
| `STOXICITY_MAIL_FROM`, `_TO` | `.env.local` | personal | Sender and recipient |
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

`refresh.fast` in `config.yaml` polls sources of kind `filings` more often
(`interval`, default 2m) on weekdays between `from` and `to` in `zone`
(default 06:00-22:00 New York time, following its daylight saving). It is off
by default.

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
has the version `<lens>+<classifier>+laya-<model|auto>-q<8 hex>-p<8 hex>`; the
`q` part changes with the model and the questions, the `p` part with `apply`,
`min_confidence` and `relevance_weight`. A stored row is never overwritten for
a version it already has, so a change of any of these affects the articles
scored from then on, and `stoxicity rescore` adds a row with the new version
for the older ones and keeps the old rows. `batch_size` is not part of the
version because it does not change the answers.

**Starting the server safely.** `laya-serve` listens on `0.0.0.0` unless told
otherwise, so bind it to loopback and give it a key:

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

## Gmail

Placeholders for the email channel are in `.env` (server) and `.env.local`
(account). **How to authenticate is not decided.** Two routes exist:

| Route | Variables | Trade-off |
|---|---|---|
| SMTP with an app password | `STOXICITY_SMTP_USER`, `STOXICITY_SMTP_PASSWORD` | Least code; stores a password-like secret |
| Gmail API with OAuth | client ID, client secret, refresh token (names to be chosen) | More setup; no password stored |

Google's current rules for app passwords and OAuth consent were not checked in
this project. Read them before choosing, and before putting any real value in
`.env.local`. Whichever route is used, a missing credential must stop the
channel with a message naming the empty variable and never printing a value.

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
