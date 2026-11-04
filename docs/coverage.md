# Coverage and source evaluation

News comparison services show, for one story, how many outlets cover it and
which ones, and they rate the outlets. stoxicity takes that way of working and
applies it to what a long-term investor needs: not a political lean, but **what
a story rests on** and **what each source is worth**. Nothing here fetches or
copies any such service; the design is the idea only.

## Stories and backing

A story is a cluster of articles about one event (package `cluster`: canonical
URL, then title similarity within 48 hours). Outlets are counted by **domain**,
so two feeds of one publisher are one outlet, not corroboration.

| Backing | Meaning |
|---|---|
| `primary` | a tier 1 source reports it: a filing, a regulator, a central bank. Strongest, even with one outlet |
| `corroborated` | no primary source, but two or more independent outlets of tier 2 report it |
| `unconfirmed` | one tier 2 outlet only |
| `opinion` | only commentary (tier 3) |

A story is **thin** when it is material (materiality 4 or more), rests on one
outlet and has no primary source: the case to check before acting on it. This
is the counterpart of a "blind spot": not a story one camp ignores, but a
consequential claim that only one newsroom carries.

```
stoxicity coverage [-days 7] [-limit 20] [-backing primary|corroborated|unconfirmed|opinion] [-thin] [-all]
```

By default only stories at or above the lens threshold are listed; `-all` adds
the rest. Columns: outlets, backing, materiality, mean tone, first report (time
and outlet) and the lead's title with tickers.

## Source evaluation

```
stoxicity sources eval [-days 30]
```

| Column | Meaning |
|---|---|
| RELEVANT | share of the source's articles at or above the lens threshold |
| SHARED | stories at least one other outlet also reported |
| FIRST | share of shared stories where this source reported first |
| LAG | median delay behind the first report, when it was not first |
| ALONE | stories only this outlet reported |
| PRIMARY | stories that rest on a primary source |
| KEPT | share of your keep/drop verdicts on the source that were keep (shown as a percentage from 3 verdicts) |

Read it as a set of questions, not a score: a source that is often first on
shared stories is early; one that is mostly alone and mostly dropped is noise;
one that is never alone is redundant with others. Keep what you read, drop what
you do not. FIRST and LAG mean little until several sources overlap, and KEPT
needs your labels (`stoxicity label sample`).

## Limits

- Clustering is by URL and title similarity, so two outlets that retell an event
  in different words are two stories, and unrelated headlines can merge. Check a
  cluster before relying on its breadth.
- Domains are compared as written, without a public-suffix list: `news.example.com`
  and `example.com` are two outlets.
- Tiers are the curator's judgement in `sources.yaml`.
- Run on 2026-11-04 against a copy of the stored data, every story was
  `primary`, because only SEC filings and Fed releases had been fetched; the
  other columns become meaningful after the new feeds have run.
