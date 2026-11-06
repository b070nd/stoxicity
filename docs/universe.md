# The stock universe

The universe is the reference list of stocks: every company the program knows,
what is known about each, and ways to find, filter, tag and group them. It
answers "what is this ticker", feeds the `company:`, `list:` and `tag:` operators
of the article filter, and is what the profile pages of the interface are made
from.

## Where it comes from

Four sources, each optional, joined by ticker:

| Source | Gives | Fetched by |
|--------|-------|------------|
| SEC `company_tickers_exchange.json` | ticker, name, CIK, exchange (Nasdaq, NYSE, OTC, CBOE, TXSE) | `stoxicity universe update` (and `fetch`/`run` when the cache is older than `company.max_age`) |
| Nasdaq Trader `nasdaqlisted.txt`, `otherlisted.txt` | ETF or stock, test symbol, market tier, financial status, the listing venue | the same command, when `company.listing` is on |
| SEC `submissions/CIK##########.json` | industry (SIC code and words), state of incorporation, fiscal year end, filer category, website, former names | `stoxicity universe enrich` |
| your layer, `tenants/<tenant>/stocks.yaml` | lists, tags, and companies the SEC lacks (non-US listings) | you, or `universe tag` / `universe list` / the interface |

The first three are cached under `data/company/`. The interface never fetches;
only these commands touch the network. `universe enrich` makes one request per
company (default at most 300 per run, paced by the shared HTTP client, which stays
below the SEC's limit), stops cleanly when the SEC throttles, and can be run
again to continue; records younger than 30 days are skipped. Without arguments
it covers the stocks you follow, those in your lists or with tags, and those in
relevant news in the last 30 days; `-all` goes through every company, and
`TICKER...` names the ones you want.

The Nasdaq directory is for personal, non-commercial use under its terms: the
program keeps a single local copy and never passes it on. Turn it off with
`company.listing: false`; the universe then has no ETF flag, market tier or
venue.

## The catalogue: every stock, no shortlist

`c` in the interface opens the catalogue of **all** companies: every ticker of
the SEC list, every security of the Nasdaq Trader directory the SEC list lacks
(most ETFs, preferred shares, warrants, test symbols; they have no CIK), and
what you added yourself. Nothing is cut off at a number: the list holds one row
for each match, however many (about 12,000 with no query), and the screen draws
only the rows in view, so it stays quick.

- **Order.** Without free text, by ticker; with free text, best match first;
  `sort:name`, `sort:news`, `sort:industry`, `sort:exchange` or `sort:kind`
  (`sort:-news` reverses) overrides both and combines with any filter, for
  example `exchange:nyse kind:etf sort:name`. The line under the title says which
  order and how many of how many.
- **Groups.** Under a heading per exchange by default, the biggest first; `x`
  cycles exchange, kind, industry and none, and `group:kind` in a query sets it.
  Sorting applies inside each group.
- **Moving.** `home`, `end`, `pgup`, `pgdn`, and `g` to jump: type the start of a
  ticker (of a name, under `sort:name`) and the cursor goes to the first one
  that starts with it, without filtering the list. The status line says where
  you are ("company 4,521 of 12,345").
- **Narrowing.** `/` filters as you type with the query language below; the
  lines under the title count the matches by exchange, kind, industry, list and
  tag, so you can see what to narrow by.
- **Exporting.** `stoxicity universe find -limit 0 [-format tsv|json] [QUERY...]`
  prints every match, with every field for tsv and json, for a spreadsheet or a
  script: `stoxicity universe find -limit 0 -format tsv kind:etf > etfs.tsv`.
- **Test symbols** the exchanges publish are in the list and marked `test`;
  `-kind:test` leaves them out.

## The reference database

Everything above is stored in the shared `base.db` (tables `ref_source`,
`ref_ticker`, `ref_listing`, `ref_company`, `ref_snapshot`, `ref_change`), and
the interface, `universe` and `stock` read it and never write it. The files in
`data/company/` stay the download cache (ETag and time of the last check);
`refdata.Sync` copies what they hold into the tables, and an empty database is
filled from them the first time it is needed.

- **Self-refreshing.** `stoxicity run` refreshes in the background every
  `company.refresh_every` (6h; `off` disables): the ticker list, the Nasdaq
  files, then the industry data of up to `company.enrich_per_cycle` companies
  (those you follow, list or tag first, then the rest in CIK order, so it covers
  everything over a few cycles), then the sync, then the feeds of each stock
  (the SEC filing record of every stock with a CIK, and the websites of up to
  `company.feed_search_per_cycle` companies searched for their own news feed).
  `universe update`, `universe enrich` and `universe feeds` do the same by hand.
- **Deterministic.** A source is compared by a hash of its parsed rows sorted by
  key, not of the bytes, so the same content in another order or fetched at
  another time changes nothing and writes nothing but the time it was checked.
  A real change writes one snapshot and, when it is 2000 rows or fewer, one
  `ref_change` per added, removed or changed row. The first load is one snapshot
  without per-row changes. A source that fails keeps its old rows and records the
  error until the next good answer. Aliases are sorted and de-duplicated.
- **Checking.** `universe status` shows the sources with their content hash and
  the line `universe N profiles, fingerprint <12 hex>`: the fingerprint is the
  hash of every profile, list and tag, so two installs with the same data print
  the same one. `universe changes [-days N]` lists what changed.

## Lists, tags and extra companies

`stocks.yaml` is yours:

```yaml
lists:
  - id: compounders
    name: Long-term compounders
    tickers: [KO, BRK-B, MSFT]
tags:                      # ticker: its tags
  KO: [moat, dividend-grower]
  MSFT: [moat]
  INTC: [turnaround]
extra:
  - ticker: SAP
    name: SAP SE
    exchange: XETRA
    isin: DE0007164600
    aliases: [SAP AG]
```

- A **list** is a set of stocks you group by hand; use it to say "my picks". It
  is called a list, not a group, because `group:` already means an event group in
  the article filter.
- A **tag** is a label on a stock: `moat`, `dividend-grower`, `cyclical`. Lists
  and tags are lower-case words with digits and dashes (up to 40 characters).
- **extra** adds a company the SEC lists lack: a non-US listing, a stock the
  list has not caught up with. The universe covers the US through the SEC lists;
  other markets only through `extra:`.

The file is rewritten whole when you tag or list from the interface or the
command line, so comments in it are lost; keep notes elsewhere.

```
stoxicity universe tag NVDA moat -turnaround     # add moat, remove turnaround
stoxicity universe list                          # your lists with sizes
stoxicity universe list compounders              # one list
stoxicity universe list add compounders KO MSFT
stoxicity universe list remove compounders KO
stoxicity universe changes -days 7               # added, removed and changed rows
```

## Finding companies

```
stoxicity universe find [-limit N] [-days N] QUERY...
```

and `c` then `/` in the interface use one query language, cut down from the
article filter to what a company has:

```
apple                    a word: the ticker, or a word of the name, former
                         names or industry, starting with it ("app" finds Apple)
"berkshire hathaway"     a phrase
-etf  -"blank check"     not
a | b   a OR b           either
ticker:KO  ticker:A*     the ticker; a trailing * is a prefix
name:nvidia              name, aliases and former names (company: is the same)
cik:320193
exchange:nyse            the SEC's exchange, or Nasdaq's venue ("NYSE Arca")
kind:etf                 stock, etf or test
industry:software        words of the SIC description
sic:3571  sic:73*
state:de                 state of incorporation
sector:technology        your sector rules in config.yaml
market:"global select"   Nasdaq market tier
status:deficient         financial status; status:any is any trouble
list:compounders         member of one of your lists
tag:moat                 carries your tag
watched:yes              on your watchlist or in a watch
news:>=3  news:yes       relevant articles in the window
sort:name  sort:-news    the order: ticker, name, news, industry, exchange, kind
group:exchange|kind|industry|none
                         how the catalogue arranges the list under headings; the
                         default is by exchange. Not a filter.
```

Terms are ANDed; `|` or `OR` makes a choice; there are no parentheses. Free text
ranks by how well it matches (ticker exactly, ticker prefix, start of a name, a
word in the name, a former name, the industry), then by news in the window, then
by ticker.

## Filtering articles by company

The same facts work inside the article filter (search, watch queries, `/` on
every other screen):

```
company:apple            articles that name a company called like that
list:compounders         articles that name any stock in the list
tag:moat -tag:cyclical   by tag
exchange:nyse            by listing
industry:semiconductors  by industry words (after `universe enrich`)
sic:3674  state:de  cik:320193
```

Each of these is turned into a set of tickers before the query runs, so
`list:compounders dividend` means "dividend news about my compounders". An
unknown list is an error that names the lists you have; an unknown tag matches
nothing. `ticker:` stays the filter's own.

## Profile page

Every ticker has a page in the interface (`enter` on any stock row, or `c` for
the company list): name, share-class siblings, listing, kind, industry and SIC,
sector, state, fiscal year end, CIK, ISIN, website and an EDGAR link (`o`
opens them), then what is yours: watchlist star, lists, tags and the watches
that follow it, and below that the news. A stock with no news in the window still
has its page. `#` tags the stock under the cursor and `L` puts it in a list; a
leading `-` removes.

## Limits

- Ticker spelling follows the SEC (`BRK-B`). The exchanges write `BRK.B`;
  lookups accept both, and the Nasdaq files are converted when read. The
  conversion is untested against the live files.
- The SEC list holds US-listed issuers. It does not cover other markets.
- SIC codes are the SEC's, not a modern sector classification; use the
  `sectors:` rules in `config.yaml` for that.
