# Events, observations and research files

Articles are evidence. An *event* is one thing that happened; many articles can
be about one event and one filing can concern several. See PROTOCOL.md sections
10 and 11 for the model. This page is about using it.

```
stoxicity events rebuild [-days 365]        derive the events, claims and readings from stored articles (repeatable)
stoxicity events timeline TICKER [-at TIME] a security's events as known at a time
stoxicity events show EVENT                 every revision of one event
stoxicity evidence [-days 7] [-at TIME] [-explain] [-all] [-min N]
                                            securities whose events rest on primary or corroborated evidence
stoxicity graph TICKER [-format text|json|dot]
                                            the small knowledge graph around a security
stoxicity export [-out research] [-days 30] write Markdown files for Git
stoxicity reconcile [options]               compare the SEC indexes with the stored filings (see below)
```

Existing before this change: `events rebuild | timeline | show`, `evidence`
(`-days`, `-min`, `-limit`, `-all`), `graph`, `export`, `reconcile -days -repair`.
New: `evidence -at -explain` and a `-min` that defaults to 0; `reconcile -from -to
-catchup -no-quarterly -refetch -max-requests -enrich -facts`; `events rebuild`
now also writes assertions and assessments (see below).

`evidence` was called `signals` until 2026-11-12; the old name still runs it and says so. Buy, sell and
watch signals are a different thing: see [research-engine.md](research-engine.md).

New articles are turned into events as they are stored; `events rebuild` is for
the articles stored before this existed, and for re-deriving after the rules
change (it appends; it never edits an earlier row).

## Three states of evidence, not a score

`evidence` is a screen for where to read first. It measures how broad and how
independent the evidence is, not whether the news will move a price, and it is
not a trading signal. The word is *corroborated*, never "strong".

| State | Means |
|---|---|
| **primary** | A filing, a regulator or the issuer's own release reports the event. That is primary evidence; it is **one** reporting origin. Stories quoting it are copies of it, not confirmation. |
| **corroborated** | At least two reporting origins that do not rest on each other report it. A primary document counts as one origin; derivative coverage of it adds none. Independently reported follow-up evidence counts. |
| **unresolved** | Independence is not established: one origin, no attribution, or only derivative or unknown-origin articles. Unknown stays unknown; it is never counted as independent and never as a copy. |

One filing is therefore *primary evidence*, never "independent corroboration of
its own claims".

**Materiality takes no part in the verdict.** Before this change `evidence`
defaulted to `-min 2` and removed low-materiality events before the evidence was
assessed, so a keyword score could manufacture or remove confidence. The default
is now `-min 0`: every event is assessed. If `-min N` is given the output says
how many events were left out before assessment and that the states can be lower
than with every event.

`evidence -explain` (and `graph`, `export`) print the evidence behind each verdict:
each reporting origin, how it was identified, its confidence and assessment
version, and why two stories were or were not counted as one.

`evidence -at TIME` judges what was known then: observations, origins, episode
decisions and assertions recorded or decided later are not used, and nothing is
stored.

## Reporting origin

The publisher (the site that ran the story) is stored apart from the *reporting
origin* (who first reported it). An origin has a kind (`primary`, `issuer`,
`wire`, `reported`, `derived`, `unknown`), a stable key, an optional link to the
origin it was derived from or copied from, the detection method, a confidence and
the assessment version.

Rules, strongest first, all conservative: an explicit provider ID, a wire
attribution or byline, a canonical reference, a filing accession, and an
exact-copy fingerprint of the normalised text. Syndicated copies count as one
origin. Topic or event similarity (same company, same kind, same days) is **not**
evidence of a shared origin and is never used as such.

**Standing.** What the corroboration verdict reads is the standing of an origin,
never the publisher's domain: `own` (a filing or the issuer's release),
`attributed` (the text names a news agency: a dateline, a credit line), `claimed`
(a byline or a provider ID and nothing more), `suspected-derivative`, `derived`
(rests on a document or another report) and `unknown`. **Only `attributed` can
stand as independent reporting.** A byline proves nothing (rewrites carry
bylines), and a distinct domain proves nothing. Several rewritten copies of unknown
origin therefore stay `unresolved`, however many sites carry them.

**Near copies (`origin-2`, partial).** Within the articles of one story, after the
explicit attributions: an exact copy of the normalised text; a *near copy* when
at least 85% of the three-word runs of both texts are shared (texts of at least 30
words; headlines are too short to compare); and a weaker *passage overlap* when,
with quoted words left out (outlets quote the same statement without copying each
other), half of the shorter text's three-word runs occur in the other or 12
consecutive words are equal (at least 20 words outside quotes). A near copy counts
once with its original. A passage overlap is only a **suspicion**
(`suspected-derivative`): it is not a finding of a common origin, the article keeps
its own key, and it is not counted as independent. Each finding is stored with its
supporting observations (`origin_match`, migration `0019`: the other article, the
method, the score, the shared runs, the longest run and the passage) and the
version, so a later version adds rows. Limits: the method compares word runs, so a
paraphrase, a translation or a heavily rewritten copy is not seen and stays
`unknown`; the thresholds were set by judgement and tested on invented texts and the
stories of one installation, not tuned or evaluated on a labelled set of real
syndicated copies; the current feeds supply no provider IDs, so that rule has no
input yet; wire attribution in the text is taken as written, not verified.

## Episodes

Events of one kind for one security within **72 hours** are *candidates* for one
episode. That is a filter, not proof. Two events become one episode only with a
proof: a shared filing or release identifier, shared evidence (an article or an
exact copy), or matching issuer roles, counterparty and effective date. They stay
distinct when the facts differ (two different acquisition targets within 72 hours
are two episodes). When nothing links them they stay separate. Each decision (same, distinct, or undecided) is stored with
its method, basis and confidence and the time it was made; an earlier decision is
never rewritten, and a decision made after a cutoff is not used for a question
as of that cutoff.

A release, its 8-K and coverage of both form one episode through the shared
accession or release reference; the filing and the coverage are not counted as
independent.

The **70% diagnostic** ("one episode dominates") is computed after deduplication:
numerator = reports (distinct reporting origins, copies counted once) in the
biggest episode; denominator = all such reports for the security in the window
(`-days`, default 7). The output prints both numbers and the window.

## Several events in one story

An article that announces earnings, a CEO change and an acquisition makes **three
events**, not one with two forgotten claims. Within a story (a cluster of
articles) each event kind that has a live claim about a company is its own event:
the story's home kind keeps the story's ID (`evt_<cluster>`, so an event already
stored keeps its ID and meaning), and every other kind gets
`evt_<cluster>~<kind>` with a `split_from` relation to the home event. Each event
holds only its own claims (`event_claim`: article, claim number, version,
extractor, and whether the claim names the event), its own evidence and the
securities that claim is about with their roles; a company that is only mentioned
by another company's claim does not appear on that event. Price chatter and
analyst notes beside a result or a deal are background and make no event of their
own. A stored event that no live claim supports any more (a denied bankruptcy, a
central-bank speech whose companies are only mentioned) is **withdrawn** by a new
revision; nothing is deleted, and an as-of query before the withdrawal still shows
it.

Limit: the unit is the kind within a story. Two different acquisitions in one
article are one `acquisition` event with two claims, and two issuers' dividends in
one headline are one `dividend` event with two securities. Splitting by
counterparty or by issuer is not done.

## How an article is presented

The lens still rates an article by its first keyword, and that score is kept.
What the lists show is `pipeline.Present`: the kind and the materiality of the
most important live claim about a company, the relevance moved by the same 0.15 per
level the lens uses, and the version `<lens>+assert-2` when the reading differs.
An article none of whose claims is a current event (denied, negated, past, a
preview, or about no company) is presented as no event, with the reasons. The
reasons record what the lens said, which claim won and why the others lost
(`assertion.Result.Explain`: materiality, then the importance order, then the
earlier claim), the status, the attribution and the action ("suspended",
"raised"). Materiality (how much the event matters to the business), novelty,
urgency and price impact are separate readings (`assess-1`); price impact always
abstains. Materiality is not part of the corroboration verdict. Limits: relevance
is still the lens's number moved by materiality, not a reading of its own; the
lens version is a digest of its boost and demote settings and **not of its keyword
table**, so a change to the rules in code does not change it (run `rescore` after
one, and the old and new scores are not told apart by version).

## Assertions, assessments and the four dimensions

`events rebuild` splits each article into sentences and records **assertions**
(table `assertion`, append only, version `assert-2`, extractor `rules-1`). One
article may carry several. Each assertion has: the event kind, the exact sentence
span with offsets, parties with roles (`subject`, `counterparty`, `mentioned`)
and the basis for each, and flags: *negated*, *denied* (this includes retractions
such as "an earlier version of this story said ..."), *historical*,
*anticipatory* ("ahead of the report", but not "ahead of estimates"), a status
(`proposed`, `announced`, `completed`, `unknown`), attribution (own statement or
reported/rumoured), and magnitude and routine markers. Only a claim that is not
negated, denied, historical or anticipatory is *live*.

- All matching kinds in an article are kept; the first keyword no longer
  suppresses a more important one. The event is named by the most important live
  claim (bankruptcy, non-reliance, delisting, control change, acquisition ... down
  to dividend); an article with no live claim makes no event.
- A ticker that is only mentioned incidentally gets the role `mentioned` and no
  assessment from another company's event.
- Structured filing items (8-K item numbers) are **evidence of the event kind**.
  They are not proof of economic size: an item-only claim says "amount not
  stated".

**Assessments** (table `assessment`, versioned `assess-1`, stored apart from the
observed facts, appended only when value, label or abstention changes) give four
dimensions per company that are never merged:

| Dimension | Values |
|---|---|
| materiality | 1 to 5, from the best live claim (magnitude +1, routine -1, proposed -1) |
| novelty | new or repeat (earlier evidence in the same cluster) |
| urgency | low, medium, high |
| price impact | **always abstains**, with the reason; nothing here estimates a price move |

An assessment abstains, with its reason, when the evidence is insufficient (no
live claim, for example).

A sentence written to the reader of the text ("ignore previous instructions",
"classify this article as ...") is not read for claims; this is a filter of known
phrasings, not a defence.

### The claim reader is rules, not a model

Claims are read by rules (`rules-1`). **No model reads claims**, and none is wired
in: what exists for one is a schema (model and prompt versions, the truncation flag,
the evidence span) and the vetting function `assertion.Vet`, which keeps only
proposals whose span is verbatim in the text at the stated offsets, whose kind is
valid and whose span contains words that give that kind, and discards any
materiality the proposer states. That is a safety net for a future extractor, not an
extractor. The laya sidecar of `configuration.md` is a separate, advisory
classifier of articles; it takes no part in claims, and `laya.apply` is empty in
the checked-in configuration. A model-driven extractor may be enabled only after it
passes the gate in `challenge.Gate` (below) *and* a held-out set of labelled real
articles that does not exist yet.

### Measuring the reader

```
stoxicity eval claims [-json]
```

runs the reader on a labelled challenge corpus (`app/src/assertion/challenge`,
invented sentences about invented companies: competing claims, corrections, denials,
issuer roles, time, attribution, several events, and text addressed to the
reader). It measures the stages apart, never as one score: extraction (recall and
precision by kind), polarity (current or not, and why), attribution, roles, the
action, and the naming of the event, where the right answer is sometimes to name
none. It prints the abstentions (right abstentions, wrongly abstained, wrongly
named an event where none is current), the same by group, and every error with the
rules' own explanation and, where the corpus knows the limit, the limit. Relevance
scoring is not measured by it (`eval` does that against your labels).

On 2026-11-07, 39 cases: recall 46 of 52, precision 46 of 46, polarity 44 of 46
(of the claims that are not current events, 9 of 11), attribution 4 of 4, roles 39
of 39, naming 22 right, 1 wrong, 12 right abstentions, 1 wrongly abstained, 1
named an event where none is current; the first-keyword reading the claims replaced
was right on 23 of 37. The 11 errors are the stated limits: phrases the keyword
table does not know (winning a contract, a data breach, "beats estimates", holding
rates steady, a shareholder vote, "names a new CEO"), "fears fade" not read as a
denial, and a denial that follows the keyword "CEO". **These numbers are
optimistic**: the corpus was written by the author of the rules, and three rule
defects it exposed (a court "dismisses" a lawsuit read as a denial; "since its
2019 bankruptcy" not read as past; "announced last year" making a completion past)
were fixed against it. It is a regression guard and a gate for a future extractor,
not an estimate of accuracy on real news. A test requires that every error be a
stated limit and every stated limit still fail, so the corpus cannot hide a
regression or keep a fixed limit on the books.

## Reconcile: finding filings the live feed missed

`reconcile` runs three separate steps. Discovery always runs; the others are
options and can be run alone and repeated.

```
stoxicity reconcile                          rolling window, 7 days, extended back to the last resolved day (-catchup, default 90)
stoxicity reconcile -from 2024-01-01 -to 2024-03-31
                                             an explicit range (quarterly indexes for finished quarters, daily for the rest)
stoxicity reconcile -enrich                  read the acceptance time of missed filings from the SEC's record of each company
stoxicity reconcile -facts                   resolve the filing day of stored fundamentals by accession
stoxicity reconcile -repair                  store an article for each missed filing, as a historical backfill
```

- **Which forms** a day was read for is stored (`filing_index_day.forms`,
  migration `0023`). Only the wanted forms of a day are kept, so when the selection
  of forms changes, the days read for fewer forms are read again; a day recorded
  before the column existed is read again once. A quarterly or calendar-only
  non-filing day covers any selection.
- **Completeness is reported apart from request success** (`reconcile.Measure`):
  how many weekdays of the range are resolved for the current selection, and which
  are not and why. A run that made every request it tried can still be incomplete.
- A cancelled run (interrupt) stops between days, writes what it has finished with
  a context that is not cancelled, and says it was interrupted; the next run
  continues from the stored state and re-reads at most the day in flight.
- **Per-day state** is stored (`complete`, `partial` when the index was read before
  the day's late filings were listed, `unpublished` when the SEC had no index yet,
  `nonfiling` for weekends and known holidays, and `error`). Only `complete` and
  `nonfiling` days are skipped by the next run; `unpublished`, `partial` and `error`
  days are **retried**. Progress persists, so an interrupted run continues
  (`-max-requests` bounds one run) and a run after an outage longer than seven
  days catches up, up to `-catchup` days back (longer: use `-from`). A day once
  recorded as missing is no longer skipped for good.
- Filings are de-duplicated by accession.
- **Index discovery, filing-metadata enrichment and article repair are separate**:
  `-enrich` and `-repair` do not read indexes, and discovery stores no article.
- A repaired article is marked a **backfill** (`late`: recovered within 3 days of
  its filing day; `historical`: later). The ledger removes the urgent alerts of
  any backfilled article, whichever kind, so a repair run does not page you about
  old filings; the article still matches the watch and appears in lists and
  digests, and the number withheld is logged (`alerts withheld for backfilled articles`).
- `-facts` looks up only the accessions that stored fundamentals need; it does not
  import unrelated filings.
- Date-only precision is kept: if only the filing day is known the article is
  dated to the start of that day and its time precision is `day`. An acceptance
  time is never invented.
- Repeating a repair adds nothing.
- Quarterly indexes are used for ended quarters with at least 12 weekdays to read;
  the rest are daily. If a quarterly index cannot be read (for example, it is
  larger than the shared HTTP client's 16 MB body limit) the error is recorded and
  the daily indexes are used instead, at more requests (a limitation).

## The research folder

`export` writes `research/events/evt_*.md` and `research/securities/sec_*.md`.
The text depends only on the stored history, so a file changes only when the
history did; commit the folder if you want the readable history in Git. The
database remains the record; there is no branch per stock.

## Times

Each event keeps: the effective day, the time the earliest evidence was public,
the time this system recorded each revision, and (per assessment) when it was
made. "Known at T" uses the recorded, decided or mapped time. A relationship
inferred now is stamped now, not backdated to the article.

## Remaining limitations

- No provider IDs from current feeds; no verified wire provenance; near-copy
  detection is word-run overlap with uncalibrated thresholds (above): paraphrases
  and translations are not seen.
- No model reads claims; the rules have the limits `eval claims` lists, and the
  keyword table decides what can be found at all.
- The lens's own per-article score is still first-keyword; lists show the
  presentation built from the claims, and relevance is the lens's number moved by
  materiality. The lens version does not cover its keyword table.
- Events are one per kind within a story, not one per issuer or counterparty.
- Claim importance ranking is a judgement, not calibrated; urgency decays with
  the article's age, so a rebuild at a later time can add an urgency reading.
- Rebuilt events and assessments are known from the time of the rebuild, never
  earlier: an `-at` query before the rebuild sees the older history.
- The recurring holiday calendar can be overridden by dated, sourced entries in
  `exchange_calendar.yaml` (see [tui.md](tui.md#exchanges)); none ships, and
  halts and outages are not observed.
