# Events, observations and research files

Articles are evidence. An *event* is one thing that happened; many articles can
be about one event and one filing can concern several. See PROTOCOL.md sections
10 and 11 for the model. This page is about using it.

```
stoxicity events rebuild [-days 365]        derive the events, claims and readings from stored articles (repeatable)
stoxicity events timeline TICKER [-at TIME] a security's events as known at a time
stoxicity events show EVENT                 every revision of one event
stoxicity signals [-days 7] [-at TIME] [-explain] [-all] [-min N]
                                            securities whose events rest on primary or corroborated evidence
stoxicity graph TICKER [-format text|json|dot]
                                            the small knowledge graph around a security
stoxicity export [-out research] [-days 30] write Markdown files for Git
stoxicity reconcile [options]               compare the SEC indexes with the stored filings (see below)
```

Existing before this change: `events rebuild | timeline | show`, `signals`
(`-days`, `-min`, `-limit`, `-all`), `graph`, `export`, `reconcile -days -repair`.
New: `signals -at -explain` and a `-min` that defaults to 0; `reconcile -from -to
-catchup -no-quarterly -refetch -max-requests -enrich -facts`; `events rebuild`
now also writes assertions and assessments (see below).

New articles are turned into events as they are stored; `events rebuild` is for
the articles stored before this existed, and for re-deriving after the rules
change (it appends; it never edits an earlier row).

## Three states of evidence, not a score

`signals` is a screen for where to read first. It measures how broad and how
independent the evidence is, not whether the news will move a price, and it is
not a trading signal. The word is *corroborated*, never "strong".

| State | Means |
|---|---|
| **primary** | A filing, a regulator or the issuer's own release reports the event. That is primary evidence; it is **one** reporting origin. Stories quoting it are copies of it, not confirmation. |
| **corroborated** | At least two reporting origins that do not rest on each other report it. A primary document counts as one origin; derivative coverage of it adds none. Independently reported follow-up evidence counts. |
| **unresolved** | Independence is not established: one origin, no attribution, or only derivative or unknown-origin articles. Unknown stays unknown; it is never counted as independent and never as a copy. |

One filing is therefore *primary evidence*, never "independent corroboration of
its own claims".

**Materiality takes no part in the verdict.** Before this change `signals`
defaulted to `-min 2` and removed low-materiality events before the evidence was
assessed, so a keyword score could manufacture or remove confidence. The default
is now `-min 0`: every event is assessed. If `-min N` is given the output says
how many events were left out before assessment and that the states can be lower
than with every event.

`signals -explain` (and `graph`, `export`) print the evidence behind each verdict:
each reporting origin, how it was identified, its confidence and assessment
version, and why two stories were or were not counted as one.

`signals -at TIME` judges what was known then: observations, origins, episode
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

Limits, stated plainly: the current feeds supply no provider IDs, so that rule has
no input yet; wire attribution in the text is taken as written, not verified; a
conservative near-copy detector is not implemented, so a rewritten copy is
`unknown`, i.e. unresolved, not independent.

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

## Assertions, assessments and the four dimensions

`events rebuild` splits each article into sentences and records **assertions**
(table `assertion`, append only, version `assert-1`, extractor `rules-1`). One
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

If an AI model ever proposes assertions, `assertion.Vet` keeps only those whose
span is verbatim in the text at the stated offsets, whose kind is valid and whose
span contains words that give that kind; the model, prompt version and whether
the input was truncated are stored. Text in an article that gives orders to the
extractor changes nothing (test: `TestTextThatGivesOrdersChangesNothing`). **No
model is wired in**; only the schema and the vetting exist.

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

- No provider IDs from current feeds; no verified wire provenance; no near-copy
  detector.
- No AI extractor is wired in.
- The lens's per-article score is still first-keyword; events use the assertions.
- An event is still one per story; several claims in one article are kept as
  assertions and the best one names the event.
- Claim importance ranking is a judgement, not calibrated.
- The holiday calendar cannot know special closures.
