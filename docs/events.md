# Events, observations and research files

Articles are evidence. An *event* is one thing that happened; many articles can
be about one event and one filing can concern several. See PROTOCOL.md section
10 for the model. This page is about using it.

```
stoxicity events rebuild [-days 365]        derive the events from stored articles (repeatable)
stoxicity events timeline TICKER [-at TIME] a security's events as known at a time
stoxicity events show EVENT                 every revision of one event
stoxicity signals [-days 7] [-min 2] [-all] securities whose events are corroborated
stoxicity graph TICKER [-format text|json|dot]
                                            the small knowledge graph around a security
stoxicity export [-out research] [-days 30] write Markdown files for Git
stoxicity reconcile [-days 7] [-repair]     compare the SEC daily index with the stored filings
```

New articles are turned into events as they are stored; `events rebuild` is for
the articles stored before this existed (the events table starts empty).

## What "corroborated" means

`signals` is a screen for where to read first. It measures how broad and how
concentrated the evidence is, not whether the news will move a price, and it is
not a trading signal. The word is *corroborated*, never "strong".

- A security is corroborated when a **primary source** (tier 1: a filing, a
  regulator) reports an event about it, or when at least **two distinct
  publishers** that are not primary sources do. When a primary source is among
  the evidence, the stories quoting it are copies of it, not further
  confirmation, and are not counted.
- Events are counted as **episodes**: events of one kind for one security within
  72 hours are one episode, so a release, its 8-K and the follow-up stories are
  not three events.
- **Materiality takes no part in the verdict.** It comes from a keyword rule
  whose known defects (denials, background mentions) are not all fixed, so it
  only orders what to read first. A single episode carrying more than 70% of the
  weight is flagged ("one episode dominates").
- Every output states the limit: publishers are counted by domain, so copies of
  one wire story are not detected as one origin.

## The research folder

`export` writes `research/events/evt_*.md` and `research/securities/sec_*.md`.
The text depends only on the stored history, so a file changes only when the
history did; commit the folder if you want the readable history in Git. The
database remains the record; there is no branch per stock.

## Times

Each event keeps: the effective day, the time the earliest evidence was public,
the time this system recorded each revision, and (per assessment) when it was
made. "Known at T" uses the recorded time.
