# Held-out news evaluation

`stoxicity heldout` measures how well the program reads news, on a **licensed, source-linked corpus**, against
**labels written by independent people**. It exists so that "the extraction works" is a measured claim with a
sample size, not an impression from the synthetic tests.

## What is kept apart

| | Evidence? |
| --- | --- |
| Independent results: items labelled by at least two people attested independent | **Yes**, and only these count toward the gate |
| Synthetic regressions (`"synthetic": true`): known cases that must still read as before | **No**. Shown in their own table, never counted |
| Labels by someone not attested independent (for example whoever wrote or tuned the rules) | **No**. Ignored |

A missing external label is **not** a passed gate. With no independently labelled item the gate is
`no_external_labels`. The other non-passing results are `thresholds_not_stated`, `not_frozen`,
`changed_after_freeze`, `insufficient` and `fail`. Only `pass` passes.

## The corpus

An item names its source and URL and its **licence**: `redistributable` (the title and summary may be kept in the
corpus file) or `internal` (the file holds only the SHA-256 of the title, a newline and the summary, and the text
is read from this tenant's article store by `article_id`). An unknown licence is refused. If the stored text no
longer matches the hash, the item is left out and listed, never scored on different text. The corpus also holds
the labellers with their attestation, the names of the companies (so a sentence naming "Acme Corp" is tied to
`ACME`), and the vocabulary of real-world events the labels may name.

Reading goes through the tenant's reader, so a source the tenant has no right to is not read.

## Stages

| Stage | A label states |
| --- | --- |
| extraction | which kinds of event the article claims |
| parties | per claim, which company is the **subject** and which the **counterparty** (acquirer or target, whatever the voice) |
| polarity | negated, denied, and the action (cut, raised, suspended) |
| temporal | proposed, announced or completed, and whether the event is past |
| attribution | who vouches: the company itself, or a report |
| abstention | the article claims nothing about a listed company, and the right reading is none |
| identity | which articles are the same real-world event (compared with the program's own clustering, pairwise) |

Results are also split by **event kind**, **slice** (`contradiction`, `correction`, `multilingual`,
`paraphrase`), **language** and **source**. Every figure is a count with a 95% Wilson interval; a group with few
items is shown as it is and says little.

## Labels, versions and disagreements

* A label is never edited. A revision is a new version that names the version it supersedes and says why; the
  highest version is used and the history is counted in the report.
* Where independent labellers differ on a stage of an item, a **disagreement** is recorded with each person's
  value. It is settled only by `heldout adjudicate`, from a person attested independent who did **not** label that
  item, with a rationale. Until then the item is left out of that stage and counted as contested. Raw agreement
  before adjudication is reported per stage.

## Preregistration

`heldout thresholds` states, before looking at results: the annotators per item (at least 2), the items a stage
needs, the minimum precision and recall, and a minimum group size. The gate applies the minimums to the **lower
end** of the interval, so a lucky point estimate on few items does not pass. `heldout freeze` binds the corpus,
every label version, every adjudication and the thresholds in one hash. After it, labels, adjudications and
thresholds cannot be added, and an evaluation whose inputs no longer match the hash says `changed_after_freeze`.
A mistake found later means a new corpus, not an edit.

## Commands

```
heldout example                        a corpus, labels and thresholds to start from
heldout corpus import -file F
heldout label -file F                  label versions (several labellers, several items)
heldout thresholds -file F             and, without -file, shows the stored ones
heldout disagreements [-json]
heldout adjudicate -item ID -stage S -by PERSON -tokens a,b -rationale TEXT
heldout freeze
heldout evaluate [-json]
```

## What this is not

* It does not contain a corpus. Collecting licensed articles and getting independent people to label them is
  yours to do; until then every gate reads `no_external_labels`.
* The program's rules are read as they are. A model's proposals, if one is ever used, are scored by the same
  stages.
* Event identity is judged against the program's clustering of the articles in the corpus. It does not test
  identity across issuers' separate securities; the parties stage tests the issuer and counterparty roles.
