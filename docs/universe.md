# The stock universe

The universe is the reference list of stocks: every company the program knows,
what is known about each, and ways to find, filter, tag and group them. It
answers "what is this ticker", feeds the `company:`, `list:` and `tag:` operators
of the article filter, and is what the profile pages of the interface are made
from.

## Where it comes from

Five sources, each optional, joined by ticker:

| Source | Gives | Fetched by |
|--------|-------|------------|
| SEC `company_tickers_exchange.json` | ticker, name, CIK, exchange (Nasdaq, NYSE, OTC, CBOE, TXSE) | `stoxicity universe update` (and `fetch`/`run` when the cache is older than `company.max_age`) |
| Nasdaq Trader `nasdaqlisted.txt`, `otherlisted.txt` | ETF or stock, test symbol, market tier, financial status, the listing venue | the same command, when `company.listing` is on |
| SEC `submissions/CIK##########.json` | industry (SIC code and words), the exchange its stock trades on (the only one for most over-the-counter companies), business address and phone, EIN and LEI, state of incorporation, fiscal year end, filer category, former names | `stoxicity universe enrich` |
| Wikidata (open data, CC0), through its query service | the official website of the ticker's company, matched on the company's name so that a ticker another company uses elsewhere lends nothing (the SEC leaves its own website field empty) | `stoxicity universe enrich` (`company.sites_url` replaces the address) |
| SEC XBRL frames `data.sec.gov/api/xbrl/frames/...` | revenue and net income of the latest fiscal years, assets and equity at a date, shares on the cover page (market value = shares times the last close); no key | `stoxicity universe fundamentals` (and the background refresh, weekly); every reading is also kept as history (`fundamental_fact`), and `universe fundamentals history TICKER [-at TIME]` reads one company's whole history from the SEC and shows its figures as known at a time |
| SEC Form 4 filings (the complete submission text of each, `Archives/edgar/data/<cik>/<accession>.txt`) | who in a company bought or sold its common stock (code P or S: open market or private), how many, at what price, and what they are to the company; no key | `stoxicity universe insiders` (and the background refresh, 300 filings a cycle) |
| your layer, `tenants/<tenant>/stocks.yaml` | lists, tags, and companies the SEC lacks (non-US listings) | you, or `universe tag` / `universe list` / the interface |

The first three are cached under `data/company/`. The interface never fetches;
only these commands touch the network. `universe enrich` makes one request per
company (default at most 300 per run, paced by the shared HTTP client, which stays
below the SEC's limit), stops cleanly when the SEC throttles, and can be run
again to continue; records younger than 30 days are skipped. Without arguments
it covers the stocks you follow, those in your lists or with tags, and those in
relevant news in the last 30 days and then everything else, up to the limit;
`-all` goes through every company without a limit (the SEC records of about 10,000
companies take some twenty minutes at four requests in flight), and `TICKER...`
names the ones you want.

`enrich` is the one call that fills a stock in, in three steps, each resumable:

1. **the SEC record** (what the table says), stored again whatever its age when
   the program has learnt to keep more of it since;
2. **the official website** from Wikidata, forty tickers to a request (ten times
   the SEC budget of `-max` in tickers), skipped for ETFs and test symbols; an
   answer stands for 90 days, a "not on Wikidata" for 30 (`-sites=false` skips
   the step);
3. **the feeds**: the SEC filing record of every stock, and the news feed the
   website announces for up to `-feeds` stocks (default 50; 0 reads none).

It ends with a coverage line, the same one `universe status` prints: of the
stocks, how many have an SEC record, an address, an exchange and a website, and
how many have a filing feed and a news feed. With up to three tickers named it
then prints their profiles, so `stoxicity universe enrich AWCA` shows what was
found: for Awaysis Capital the exchange (OTC), the kind (stock), the address
(Miramar, FL), the phone and EIN, with the SEC's typos and `<br>` tags tidied.
A company with an SEC record that no exchange list names is a stock when the SEC
calls it an operating company, and its exchange is the SEC's own word.

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
  files, then the industry data of up to `company.enrich_per_cycle` companies and the
  websites of up to `company.sites_per_cycle` tickers
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
type:preferred           stock, preferred, warrant, unit, right, adr, note, etf, test
                         (read from the Nasdaq security name, the company name
                         and the ticker; finer than kind:)
domain:finance           the SIC division: agriculture, mining, construction,
                         manufacturing, infrastructure, wholesale, retail,
                         finance & real estate, services, public administration
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
close:>=10 change:<-5%   last close and the move from the day before (need prices)
marketcap:>10b           shares outstanding times the last close; k, m, b, t
revenue:1b..5b           revenue of the latest fiscal year; a range with ..
income:>0  assets:>1b  equity:>0
growth:>5%  margin:>10%  revenue growth over the year before; net income over
roe:>15  equity_ratio:>20 revenue; net income over equity; equity over assets
                         (a ratio takes a percentage, with or without the %)
buys:>=100k  sells:>1m   money the insiders (officers, directors, 10% owners)
                         spent on purchases, or took in on sales,
                         in the last 90 days (Form 4 codes P and S; awards, tax
                         withholding, exercises and gifts are not counted)
buyers:>=2  sellers:>=3  how many different insiders did; a stock none of whose
                         insiders traded has no figure, not a zero
sort:name  sort:-news    the order: ticker, name, news, industry, exchange, kind,
                         type, domain, close, change, marketcap, revenue, income,
                         growth, margin, roe, buys, sells (the last ones need prices or the
                         SEC figures; a stock without the number is last either
                         way round, and matches none of the number filters)
group:exchange|kind|industry|none
                         how the catalogue arranges the list under headings; the
                         default is by exchange. Not a filter.
```

Terms are ANDed; `|` or `OR` makes a choice; there are no parentheses. Free text
ranks by how well it matches (ticker exactly, ticker prefix, start of a name, a
word in the name, a former name, the industry), then by news in the window, then
by ticker.

## Prices

The lists show each stock's newest daily candle: open, high, low, close and the
move of the close from the day before. They come from Polygon's grouped daily
bars (`company.quotes`, key in `STOXICITY_POLYGON_KEY`), stored in the
reference database (`candle`, with `candle_check` remembering market days that
had none). `stoxicity run` keeps them current in the background after each
sync; `stoxicity universe quotes` does it by hand, and `universe status` and
`universe enrich` print the share of stocks that have one ("price 94%"). Only
candles a chart can draw are kept (positive prices, low under high, open and
close between them), and only for tickers the list knows.

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

## Exchanges

`Profile.Exchange` is the grouping key of the catalogue and of the exchange screens of
the TUI. `Universe.Exchanges` counts the companies of each (the biggest first),
`Universe.ResolveExchange` finds one by its name or an unambiguous prefix, and
`Universe.ScopeNews` narrows articles to the news of one exchange (see
[tui.md](tui.md#exchanges)). `exchange:nyse` in a query matches the whole name, so
it does not take in NYSE Arca; write `exchange:nyse*` for both.


## Splits and the basis of prices

Candles are fetched raw (as traded that day) and marked so. Polygon's list of
splits is read once a day (`split`, with the span it covers in `split_run`).
A move between two closes is `marketdata.Compare`d with the splits between
them: a split is applied to the earlier close and the move is shown on one
basis, however large it is. When a candle's basis is unknown (candles stored
before this change) next to a later split, or a move looks like a split that
the known splits do not cover, the lists show `split?` instead of a number.
Nothing is hidden because of its size.

## Share classes

`stoxicity universe fundamentals classes TICKER` reads the cover page (inline
XBRL) of the company's latest 10-K or 10-Q, stores the shares outstanding per
class and prints each class's value at its own symbol's price. The issuer's
value is printed only when every class has a price.
