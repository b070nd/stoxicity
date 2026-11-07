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
marketcap:>10b           issuer capitalisation: every class's shares times its own
                         last close; k, m, b, t (see Share classes)
classcap:>1b issuercap:>10b
                         one class's value; the whole issuer's (= marketcap)
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
                         type, domain, close, change, marketcap, classcap, issuercap, revenue, income,
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

## Share classes and capitalisation

Shares outstanding come from the **cover page** (inline XBRL) of an issuer's
latest 10-K or 10-Q, read per class. The SEC frames answer is **not** used for
market capitalisation (it leaves out class-level counts); `fundamentals` still
fills revenue, income and the other figures from it.

### Where the counts come from

A background **cover keeper** runs in the reference refresh (`company.covers_per_cycle`,
default 20, 0 to 500; see [configuration.md](configuration.md)), never while a list is drawn. For
each due issuer it reads the SEC's record of the company, then the newest
periodic report's document (first the first megabyte with a `Range` request,
trusted only when the last cover fact lies in the first half of what was read;
otherwise the whole document). Order: issuers never read, then ones you follow,
then ones with several listings, then the longest unread. An issuer is read again
after 30 days and a newer report is stored as a new observation. The SEC's block
or rate limit stops the cycle.

**Retry state is kept.** Each look is recorded in `cover_check` (migration `0022`)
with its kind: `''` it worked, `failed` the request or the parsing failed,
`nocover` the issuer's recent reports have no machine-readable cover page (not an
error: unsupported by this method). A failed issuer is not looked at again before
`next_ms`: the wait starts at 3 days and doubles with every consecutive failure,
to at most 14 days, and a good reading resets it. The order within a cycle is
followed issuers first, then never read, then several listings, then the oldest.
A restart continues from the stored state, so the walk through the universe is
resumable and a failing issuer does not take the cycle's requests again and again.

Each observation is kept in `share_class_obs` (append only): accession, form,
filing day, acceptance time when the record gives it (`precision` `time`) or only
the day (`day`), time received, the class dimensions, the shares, the count date
(the date on the cover) and how many facts stated it. The same count stated
twice (two contexts of one class) is one row with `duplicates` counted; a
different count for the same class and date is a separate row. Which row wins
(`valuation.Select`): the newest report by count date, then filing day, then
accession; two different counts in one report make the class **conflicted** (no
count, with the reason), an earlier report that stated a different count marks it
**revised**, and a class stated earlier but missing now is **dropped** (so the
issuer total is withheld). Weighted-average shares are never used as shares
outstanding. An observation counts for a question only if it was received, and
(by acceptance time, or the end of the filing day when only the day is known)
public, by the time asked.

### Mapping classes to securities

`share_class_map` (append only, provenance kept; a new row only when the status,
security, ticker or method changes) ties each class to a stable security ID.
Rules in order: the cover lists a trading symbol and one listing has it
(`cover-symbol`); the class carries a letter (Class B) and exactly one listing of the
issuer carries that letter in its ticker (`class-suffix`); one class and one
listing of common stock (`sole-listing`). More than one candidate is
`ambiguous` (candidates named); no candidate is `unmapped` (an unlisted class). A
class is never priced from another class's price. Mappings made after a cutoff
are not used for it.

### Values and the reasons they are missing

- `classcap` = the class's shares times its own listing's raw close, after
  aligning the count and price across splits.
- `issuercap` = the sum of every class, only when **every** class can be valued.
  If one is unlisted, ambiguous, stale or conflicting, the issuer total is
  **unavailable** and the classes that can be valued keep their values.
- `marketcap` is defined as **issuer capitalisation** (the same as `issuercap`),
  and is the same number on each of an issuer's listed class tickers. Use
  `classcap` for one class.

A value is computed only if: the class is mapped (not ambiguous, not unmapped);
the security is not an ADR (the ADR conversion ratio is not known, so an ADR
mapped class is rejected rather than priced as one share); the count is at most
200 days old; there is a price for that listing, a raw (not split-adjusted) close,
at most 10 days old, **whose currency is known and is the output currency (US
dollars)**; and the split list covers the days between the count and the price.

**Currency is never assumed.** The quote currency is the one the price source
states for the candle (`candle.currency`, migration `0021`); a candle that states
none (every candle stored before the column, and any source that does not say) is
read in the currency of its listing venue (`exchange.Lookup`), and the output says
which basis it used. A stated currency that contradicts the venue's, or a venue
that is not known, is rejected as `currency` with the reason. A foreign quote can
be converted only with a dated, sourced rate (`valuation.Rate`: from, to, day, rate,
source; at most 4 days from the price session); **no rate feed exists in this
program, so in operation a non-US-dollar quote is rejected, not converted**. The
output currency is stated beside every figure. A split between the count date and the price day multiplies
the count by the split ratio (a reverse split divides it). A split list that was
not read after the price day is not trusted, and the value is unavailable with
that reason. Where a value is unavailable the lists show `n/a`, the profile and
`universe show` print `unavailable: <reason>`, and exports leave the cell empty.

### Valuation at a past time

`classes -at TIME` values the issuer for a past time, in one of two bases that are
never mixed, and the output names the basis, every input with its dates and the
completeness (classes valued of classes):

- `-basis known` (default): what **this installation held** then. A count counts
  if it was received by then and the filing was public; a mapping if it was made
  by then; a price if it was fetched by then (`candle.fetched_ms`, and the earlier
  versions of a replaced candle in `candle_revision`); a split if it was first seen
  by then (`split.first_seen_ms`) and the split coverage as read by then
  (`split_read`). A price whose fetch time was not recorded (candles stored before
  migration `0021` are of that kind) cannot be proved to have been held, so a
  `known` query leaves it out; it is not backdated.
- `-basis reconstructed`: what the **record supports now** for that time: filings
  public by then, sessions closed by then (a session is public from the end of its
  UTC day), splits executed by then, whenever they were found. Mappings are
  **today's**: they are not reconstructed. It is a reconstruction, not what the
  installation showed.

Later filings, mappings, prices, splits and assessments never enter either basis.
A missing input gives a reason (`no price known`, `not mapped`, ...) and the
figure is withheld, not estimated. Ages are measured at the time asked.

### Commands

Existing: `universe fundamentals [-force]`, `universe fundamentals history
TICKER [-at TIME]`, `universe show TICKER`, queries `marketcap:`.

New or changed:

```
stoxicity universe fundamentals classes TICKER [-stored] [-at TIME [-basis known|reconstructed]]
                       per class: symbol, mapping and its basis, count date, shares,
                       split alignment, price, session, currency, value or reason; revised,
                       conflict and dropped notes; the issuer total; the inputs with their
                       dates and the completeness. Without -stored it reads the cover page
                       first, which is a request to the SEC (and stores it); -stored reads
                       what is stored and makes none; -at TIME values the issuer for a past
                       time in the named basis (see "Valuation at a past time").
stoxicity universe fundamentals covers [-max N]
                       read the due cover pages by hand (as the keeper does)
stoxicity universe fundamentals probe TICKER [-annual SPEC] [-instant SPEC] [-out DIR]
                       the read-only live check of the SEC frames (see PROTOCOL.md section 11)
```

### Coverage

```
stoxicity universe fundamentals coverage [-list STATE,...|all] [-followed]
```

counts, for all issuers and for the ones you follow: issuers, checked, mapped,
valued, stale, incomplete, unresolved, unsupported, failed and not read yet, with
the number waiting for a retry; `-list` names the issuers behind a number, each
with its reason. The same states appear in the lists (below). It reads what is
stored; it makes no request.

| state | meaning |
|---|---|
| valued | every class has a value |
| stale | a count or a price is too old |
| incomplete | something is missing that more data may supply (a price, a mapping) |
| unresolved | the evidence does not decide (ambiguous mapping, conflicting counts) |
| unsupported | this method cannot value it (ADR, no cover page, a foreign currency without a rate) |
| failed | the last look failed; retried later |
| pending | the cover page has not been read |

Queries accept `classcap:` and `issuercap:` (the same operators as `marketcap:`),
and `sort:classcap` and `sort:issuercap` order by them. A stock whose value is
unavailable matches none of these filters and sorts last. TSV export
adds the columns `classcap` and `issuercap`. The CLI, the TUI (the MCAP column
shows `n/a` with the reason in the profile), the query engine and exports all use the same
calculation (`valuation.Build`).

### Examples with expected availability

- **Moog** (classes A and B, both listed): mapped by cover symbol or class
  letter, both priced: `classcap` for each ticker and `issuercap`/`marketcap` the
  sum, e.g. 28,000,000 sh x 200 + 4,000,000 sh x 190 in the tests.
- **Alphabet** (A, B and C; class B has no listing in the universe): GOOGL and
  GOOG have `classcap`; class B is `unmapped`; so `issuercap`/`marketcap` are
  **unavailable** ("a class is not listed") and no value is guessed for B.

### Limits

Coverage grows with `covers_per_cycle`; an issuer whose cover has not been read
has no market cap (it says so). Only US dollars are valued: a candle is read in the
currency its source states or, failing that, its venue's, and anything else is
rejected because no rate feed exists. ADR ratios are not available. Polygon may
list a split late. Candles stored before migration `0021` have no stated currency
and no fetch time, so a `known` past valuation cannot use them. A reconstructed
valuation uses today's mappings. The mapping rules are heuristics checked on a
handful of issuers, not on the universe.
