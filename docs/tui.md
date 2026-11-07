# The full-screen interface

`stoxicity-tui` reads what `stoxicity fetch` and `stoxicity run` have stored. It
never fetches and never scores; it shows the shared base through the licence
rule of your tenant, and writes only three things: your keep/drop labels, the
read marker of a watch, and your tags and lists (`stocks.yaml`).

```sh
stoxicity-tui                       # the default tenant and its default watch
stoxicity-tui -tenant alice -watch buffett-core
stoxicity-tui -root path/to/config  # default: the closest config folder above the working directory
stoxicity-tui -refresh 10s          # reload every 10 seconds (default: a minute, at least a second)
```

## Layout

```
 News  Stocks  Topics  Domains  Exchanges  Sources  Watches  Label
 [x] Relevant only (v) │ Watch: All │ All exchanges
 all exchanges · sources 21 ok 2 stale 1 failing · next fetch sec-edgar in 0:42 · news 1,204 articles, 87 relevant · 5 unread
 News › Stocks › Stock NVDA · Nvidia Corp      <- trail and screen title
 ...
 → open  ← back  / filter  > sort  Space mark  # tag  ? help  quit
 refreshed 09:41 · default
```

- **Section navigation.** The header contains only News, Stocks, Topics, Domains,
  Exchanges, Sources, Watches and Label. `Tab` / `Shift+Tab` switch sections and
  focus the header. While it is focused, `←` / `→` switch sections and `↓`,
  `Enter` or `Esc` return to content. `F6` toggles header focus; `↑` from the first
  content row (or above the exchange chips) also reaches it. The active section
  remains visible on narrow terminals. Shortcuts remain: `n` (or `h`) News,
  `s` Stocks, `c` the grouped stock catalogue, `t` Topics, `d` Domains, `e`
  Exchanges, `o` Sources, `w` Watches, `a` Label. Companies and Stocks now share
  one Stocks tab.
- **News filter.** `[x] Relevant only` is a filter, not a separate navigation tab.
  `v` toggles it for the News feed, search results and day views. The default is
  relevant-only. The News feed is newest first, with an activity timeline below
  it; the repeated digest/top-articles/dashboard lists have been removed. The
  original article evidence remains available, including below-threshold news on
  stock detail pages. Disabling relevance clears the bundled `all-relevant`
  watch if selected, since that watch otherwise filters upstream; custom watch
  constraints remain in force.
- **Watch scope.** The filter row shows the selected watch. `Ctrl+Tab` /
  `Ctrl+Shift+Tab` cycle watches, `1`–`9` select one, and `w` opens the full list
  with unread counts. Digits are an alternative on terminals that do not transmit
  modified Tab. Watch switching preserves the section. Loading a watch's screen
  marks its hits as seen. With no watch selected, news is limited only by tenant
  visibility, exchange scope, query and the relevance checkbox.
- **Breadcrumbs.** The trail shows where you are in the hierarchy (Home › Exchanges
  › NYSE › KO). `←` (or `esc`, `backspace`) goes up one step, `→` (or `enter`) opens
  what the cursor is on, `]` goes forward again. Going back puts the cursor on the
  row you left.
- **Meters.** The line under the filters is always there: the exchange every
  screen is narrowed to (see Exchanges below), how many sources are
  healthy (`ok`), have not worked for more than two polling intervals or failed
  once or twice (`stale`), or are failing (`failing`); when the next source is
  due, or how long overdue it is (a long "overdue" means nothing is running
  `stoxicity run`); how many articles the window holds and how many are
  relevant; and the unread count over all watches.
- **Filter bar.** `/` takes a Google-style query (the syntax is in
  `app/src/filter`): `"free cash flow" -rumor ticker:AAPL site:sec.gov`. `enter`
  opens the results as a screen of their own; a query that does not parse is
  reported and stays in the bar.
- **Key bar.** The line above the status shows the keys that do something where
  you are, and only those: `jump` appears on the catalogue, `+ keep` and
  `- drop` with the cursor on an article, `# tag` with the cursor on a stock. A key that
  is a letter of its label is underlined in the word (**j**ump, **g**roup, e**x**change);
  the others (`→`, `Space`, `+`, `#`, `?`) are written in front.
  A key that does nothing here says why in the status line. `?` lists every key.
- **Status line.** When the data was last loaded, the tenant, how many rows are
  marked, and the last message or error. The screen reloads every minute (see
  `-refresh`) and on `r`. After a reload, the rows that are new or changed since
  the last one stand out in bold for a few seconds and the status line
  counts them; a reload that changes nothing, and sorting, mark nothing. The
  cursor stays on its row, whatever moved. Refresh emphasis preserves the cells'
  financial colors instead of turning negative values green.

## The keys

The key map is data (`DefaultKeys`) and a test keeps four rules over it:

1. **No collisions.** No key is bound twice, and none is capitalised: `s` is never
   one thing and `S` another.
2. **Keys are in the words.** Each screen has a letter of its own name, shown
   underlined in the tab bar; each key bar entry shows its letter inside the word
   (**j**ump, **g**roup, **r**efresh). Only keys that are no letter of the label
   (arrows, `Space`, `+`, `-`, `/`, `#`, `?`) are written in front of it.
3. **Arrows are the hierarchy.** `←` goes up, `→` opens, `↑` and `↓` move the
   cursor. Everything has a page, so everything is one `→` from where it is
   mentioned: a stock in an exchange's news, the exchange on a stock's profile,
   the stocks of an article (`1`–`9` in an open article open its n-th stock).
4. **Deterministic.** The same data draws the same screens: every cloud, group and
   table comes from sorted data, never from the order of a map.

| Where | Keys |
|-------|------|
| Sections | `Tab` `Shift+Tab` next/previous · `F6` focus header · `←` `→` select when focused · `↓` content |
| Watches | `Ctrl+Tab` `Ctrl+Shift+Tab` next/previous · `1`–`9` the n-th |
| Screens | `n` / `h` `s` `c` `t` `d` `e` `o` `w` `a` |
| Move | `↑` `↓` `pgup` `pgdn` `home` `end` · `→` open · `←` back · `]` forward |
| Query | `/` filter with suggestions · `v` relevant only · `f` facets (catalogue) · `j` jump · `>` `<` sort · `i` invert · `g` group |
| Stocks | `Space` mark · `ctrl+u` clear marks · `#` tag · `l` list |
| Articles | `+` keep · `-` drop · `b` browser · `1`–`9` open its stock |
| Exchanges | `x` next · `ctrl+x` previous · `u` use this one · `↑` from the top: the cloud |
| Other | `r` refresh · `?` help · `q` quit |

### The exchange cloud

Stocks (including the grouped catalogue) and Exchanges start with chips: **All** and
each exchange with its number of companies, drawn heavier the bigger it is (the
same log-scale tiers as the topic cloud); ● marks the one every screen is narrowed
to. It is the global exchange filter: step up into it from the first row with `↑`,
move along it with `←` `→` (`home` and `end` jump to the ends), pick with `Enter` or
`Space`, and leave with `↓` or `Esc`. The focus stays in the cloud after a pick, so
trying each exchange in turn is one key each; any other key leaves the cloud and
does its own work. `x` and `ctrl+x` cycle the same choice from anywhere.

## Typing

Every prompt (the filter bar, `#`, `l` and `j`) edits the same way: `←` `→`
`ctrl+a` `ctrl+e` move inside the line, `backspace` and `delete` cut a character,
`ctrl+w` a word, `ctrl+u` to the start, `ctrl+k` to the end. In the filter bar
and the tag and list prompts `↑` and `↓` recall what you typed before, each kind
of prompt with a history of its own (the last 50). `enter` accepts, `esc`
cancels.

Search suggests tickers and company names from the local universe, query
operators, and exchange values. `↑` / `↓` select a suggestion, `Tab` accepts it,
and `Enter` accepts a selected suggestion without submitting the query. A
subsequent `Enter` applies the query. With no suggestion selected, `Enter`
applies the typed query directly. Suggestions preserve preceding query terms,
never access the network, and never navigate sections while the prompt is open.
With no suggestions, `↑` / `↓` retain prompt-history behavior.

## Columns, sorting and marks

The tables (Stocks, Topics, Domains, Sources and the catalogue) have a header
with the sorted column marked ▲ or ▼. `>` and `<` move the sort to the next and
previous column, `i` turns it round; a column that counts starts with the
largest first. The choice is remembered for the kind of screen. The cursor stays
on the row it was on. In the catalogue the columns map onto `sort:` (typing a
query with its own `sort:` replaces your choice).

`space` marks the stock under the cursor (●) and moves down, so holding it marks
a run. `#` and `l` then tag or list every marked stock in one go; `ctrl+u` clears the
marks. Marks stay while you move between screens.

## Screens

| Key | Screen | What it shows |
|-----|--------|---------------|
| `n` / `h` | News | chronological news feed, relevant-only checkbox, activity timeline |
| `s` | All stocks | every stock and ETF (about 16,000), those with news first: the catalogue below, opened with `sort:news` |
| | Stocks in the news | related-stock drill-downs show mentions, materiality and tone; ★ marks your watchlist |
| `c` | Companies | the catalogue of all stocks and ETFs (about 12,000), narrowed as you type, with counts by exchange, kind, industry, list and tag |
| | Stock | profile (identity, listing, industry, links, feeds, your lists and tags, watches), the latest news about it (any age, loaded when the page opens), then summary, weekly tone and materiality, timeline, what happened, domains, topics, articles |
| `t` | Topics | tag cloud and a table with the lift over the window before |
| | Topic | its stocks, domains, timeline, related topics, top articles |
| `d` | Domains | relevant against total per domain |
| | Domain | its output, your precision on it (kept against dropped), timeline, articles |
| | Day | one day's digest, relevant articles first |
| | Search | results with domain and event counts |
| `e` | Exchanges | the exchange cloud, then the markets stocks are listed on: whether each trades now, its regular hours, companies, stocks, ETFs, news and operator; `→` opens one, `u` narrows every screen to it |
| | Exchange | about it (operator, place, market ID, website), opening hours, statistics, the types of stocks it lists, the newest news about its stocks, the stocks that news is about (each opens its page) and a table of its stocks |
| `o` | Sources | state of every source: health, what it is doing, last good poll, next due, failures, last error; failing ones first |
| `w` | Watches | the tenant's watches with unread counts; `enter` shows one |

`a` opens **Label**: how many of the 300 labels it takes are made, how the lens
does against them (precision and recall of its relevant calls, against all
labels and against the random sample only, the honest number), and below them
20 articles to judge. The queue is the articles the lens is least sure of, one
kind of event after the other, and a random fourth (marked `°`). Their scores
are not on the rows, so the verdict is yours; `enter` opens the article with its
score. `+` keeps and `-` drops, and the article leaves the queue at once. The
status line shows "labels 42 of 300 · precision 0.71 · recall 0.64".

`→` (or `enter`) on a row opens it: a heading opens its list, a table row opens its page,
an article opens its detail (score, the rules that fired, summary, your label).
`+` keeps and `-` drops the article under the cursor, in a list or in the detail;
those verdicts are the gold set for `eval` and for calibrating the lens. `b`
opens the article in your browser, for `http` and `https` addresses only; on a
stock profile it opens the company website or its EDGAR filings.

## Companies, tags and lists

`c` opens the catalogue of every company, not a shortlist: all of them are rows,
by ticker, and the status line says where you are ("company 4,521 of 12,345").
`home`, `end`, `pgup` and `pgdn` move through it, and `j` jumps: type the start
of a ticker (or of a name, when sorted by name) and the cursor lands on the first
one that starts with it. `sort:name`, `sort:news`, `sort:industry`,
`sort:exchange` and `sort:kind` change the order ([universe.md](universe.md) has
the whole query language and the export). `/` there filters the list as you type: `exchange:nyse tag:moat -etf`. `enter`
keeps the query as part of the breadcrumb, `esc` leaves the list as it was.
The catalogue is grouped under a heading per exchange ("Exchange Nasdaq · 5,608"),
the biggest first, then the stocks the SEC lists but no exchange does; inside a
group the order is the one you sort by. `g` cycles the grouping through exchange,
kind, industry and none, and `group:kind` (and so on) in the query sets it for
that query. The exchange comes from the Nasdaq symbol directory; a code it does
not explain is shown as it is. `s` opens the same list flat, news first.
Headings are rows too: `enter` on "Exchange NYSE · 2,894" opens the list narrowed
to that exchange (`exchange:"NYSE"`), flat. The catalogue starts on its first
stock, and the status line counts stocks only.

The table is colour-coded. **TYPE** is read from the exchanges' name for the
security (the Nasdaq directory's own words after the dash: "Warrant", "Units",
"Series B Right", "Common Stock", "Senior Notes due 2030") and, failing that, the
company's name and the shape of its ticker: stock, preferred, warrant, unit,
right, adr, note, etf or test (a reading, not a fact; `type:warrant` filters on
it).
**DOMAIN** is the SEC's industry division (Manufacturing, Finance & Real
Estate, Services, Infrastructure, Retail, Mining and so on), one colour each;
the finer industry is on the stock's page and in `industry:` and `sort:industry`. **DAY** is the day's candle drawn as a bar: the
whole bar is the range from low to high, the thick part is open to close, so a
body at the right end closed near its high; green closed above its open, red
below, yellow when flat, dim when stale according to the market-data session
policy. **CLOSE** follows candle direction; **CHG** follows the reconciled
close-to-close return, which may have a different direction. Stale figures remain
dim. Yellow is also used for unresolved corporate actions; the `split?` text
distinguishes these from an actual flat move.
The ticker is bold, the exchange has a colour of its own (Nasdaq blue, NYSE
magenta, OTC yellow, CBOE cyan), the news count turns yellow with one article
and bold yellow with five, tags are magenta, and the star is yellow. The name
sits beside the bold ticker. Numeric columns are right-aligned, missing values
use a dim dash, and financial colors remain visible on the selected row.
The stock table adapts to terminal width: secondary metadata and financial
columns disappear before identity and prices, while the active sort column
stays visible. Names and metadata have bounded widths and ellipses; numeric
values are not shortened to fit. All sort keys remain available with `<` / `>`;
sorting by a hidden column brings it into view. Full values are on the stock's
detail page. The table header repeats while scrolling, and `f` expands or
collapses kind/industry/list/tag facets. Growth and margin use green positive,
yellow neutral and red negative; topic lift is colored relative to 1× coverage,
not interpreted as a predicted price trend.
**MCAP**, **REVENUE**, **GROWTH** and **MARGIN** appear once the SEC's figures are
stored (see `company.fundamentals` in [configuration.md](configuration.md)):
market value, revenue of the latest fiscal year, its growth over the year
before (green up, red down) and net income over revenue. A stock's page lists
all the figures with the note of where they come from. Try
`type:stock marketcap:>10b margin:>15% growth:>5% sort:-marketcap`.
**BUYS** and **SELLS** (green and red) appear once Form 4 filings are read (see
`company.insiders`): the money the company's insiders spent on purchases of its common stock
(open market or private: Form 4 code P) and took in on sales (code S) in the last
90 days, counting each filing once, with an amendment (4/A) replacing the form it
amends. Trades without a price are counted apart and shown as a warning, not as
zero. A stock's page then has an
**Insiders** block: the last ten trades, each on two lines (day, what was done,
shares at price and value; who, their role, how many shares they hold after, and
whether it was under a Rule 10b5-1 plan or held indirectly), purchases green,
sales red, awards and tax withholding dim; `b` opens the filing. Try
`type:stock buys:>=250k sort:-buys`. A purchase says more than a sale, which is
often scheduled in advance; a fund that sits on the board (Baker Bros. in
Madrigal) counts as an insider because the form says so.
`sort:close`, `sort:change`, `sort:type` and `sort:domain` order by them (a
stock with no candle is last whichever way round). Unresolved corporate actions
show `split?`, rather than an invented return or a silently hidden move. The
selected row's end-of-day session and stale status appear in the footer. The columns appear once
prices exist (see `company.quotes` in [configuration.md](configuration.md)).
`enter` on a company opens its profile, which also exists for stocks with no news
in the window.

On a stock page, or with the cursor on any stock row: `#` asks for tags
(`moat dividend-grower`, or `-moat` to remove) and `l` for lists (a name that
does not exist yet makes the list). Both are saved to `tenants/<tenant>/stocks.yaml`.
The filter bar's article search understands `company:`, `list:` and `tag:` too.

Watches are YAML files in `tenants/<tenant>/watches/`; edit them there and
check them with `stoxicity watch check`. The interface only reads them. To change tenant, restart with `-tenant`.

## What the window covers

Every list screen covers the last 14 days in the configured time zone, and
compares topics with the 14 days before. The articles come from one query per
watch, so moving between Home, Stocks, Topics and Domains does not hit the
database again.

## Safety

Feed text is untrusted. Everything drawn is cleaned first (`sanitize.Text`), so
an escape sequence in a title cannot move the cursor, set the window title or
hide text. The browser is only given `http` and `https` addresses, as one
argument and never through a shell.

## For developers

`tui` knows nothing of databases or files. It talks to a `tui.Backend`;
`workspace` implements it over the stores, and the tests use a fake. Screens are
pure functions (`tui.Build`: route and data in, rows out) and the `Model` is
driven by messages, so the tests press keys and read the drawn view without a
terminal.

The pieces behind the keys, each with a small interface of its own:

- `Viewport` owns the cursor and the scroll position: the cursor row is always
  in the window, after a move, a page, a resize or a reload.
- `Table` lays out columns, orders items by a column and says which columns can
  be sorted by. Every list screen builds one; the model steps along its columns
  without knowing which screen it is.
- `Prompt` is the line being typed, with its cursor and its history. What the
  text means is up to the mode that opened it.
- `Binding` says where a key works (`Needs`, `Screens`, `Detail`) and what the
  key bar calls it. The key bar, the refusal messages and the help screen come
  from the same table.
- `package status` is what the sources are doing and how well, for both the
  meters and the Sources screen. `status.FromStored` reads what the scheduler
  persisted (this program is not the one that polls), `status.FromBoard` reads
  the live board of `stoxicity run`, and `status.Summarise` adds them up. The
  level is graded when read, so a source nothing polls any more turns yellow
  instead of staying green.

## Feeds on the stock page

The profile of a stock lists its feeds under `feeds`: the SEC filing record and,
where the company's website announces one, its own news feed, each with when it
was last read and how many articles that gave, or `failing (n×)` with the reason.
`b` on a feed row opens it (the SEC record opens the company's page on EDGAR). The
rows come from `Backend.StockFeeds`; where none is recorded yet the page says how
to get them (`stoxicity universe feeds`).

## Exchanges

`x` narrows **every screen** to one exchange, and `ctrl+x` goes back the other way:
the order is all exchanges, then each exchange the companies are listed on,
the one with most companies first. The line under the watch bar says which is
chosen. From the chosen exchange on:

- the news is the news that names a stock listed there, and its tickers are cut
  down to those stocks, so the stock list, mentions, tone, topics, domains, the
  timeline, days, search results and the meters all speak of that exchange only;
- the catalogue (`c`, `s`) lists only its companies, grouped by kind rather than
  by exchange;
- screens that would be pointless narrowed are left whole: a stock's own page, the
  list of exchanges, the sources and the watches; and one exchange's page is always
  about that exchange, whatever is chosen.

`e` opens the list of exchanges (NOW is the **schedule**: `open` in the regular
session, `extended` before and after it, `closed` otherwise; it is read from the
recurring rules and the calendar file below and is **not** a confirmation that
trading is available, because halts and outages are not observed; ● marks the
chosen one), and `u` on a row, or on an exchange's page, chooses it (again: back to
all). The page of an exchange has:

- **About.** Full name, operator, place, market ID (ISO 10383), currency, website
  (`b` opens it) and what kind of market it is.
- **Opening hours.** Whether it is scheduled to trade now and until when (the line
  says "scheduled open" or "scheduled closed", names the calendar version, any
  override that decided the day with its source, and that halts and outages are not
  observed), each session in the market's
  clock and in yours (`Regular 09:30–16:00 EST = 15:30–22:00 CET`), today's calendar
  (closed, early close at 13:00, or a full day) and the next three market holidays.
  The US calendar is built in: the ten holidays with their observed dates (a Saturday
  holiday is taken on the Friday, a Sunday one on the Monday), Good Friday and the
  three early closes. Extended hours are the exchange's; brokers open them for their
  customers at their own times.
- **Statistics.** How many of the companies the program knows are listed there, how
  many are stocks and ETFs, how many have an SEC record, the market tiers (Nasdaq),
  the main industries, how many of the stocks you watch are there, and the news of
  the window: articles, relevant ones, stocks named, average materiality and tone.
- **Types of stocks.** What the exchange lists (large established companies, growth
  companies, ETFs, small caps, over-the-counter) with the counts of stocks and ETFs.
- **Latest news** about its stocks, newest first, each line naming the stocks it is
  about; then **Stocks in the news**, the stocks that news is about with their
  mentions, materiality and tone, each a link to its page (the heading opens the
  catalogue narrowed to the exchange); and **the stocks** themselves in the
  catalogue's table: sort with `>` and `<`, open one with `→`.

The reference facts (operator, hours, what each exchange lists) are in
`app/src/exchange`; an exchange the companies name but that package does not know is
shown with the counts only.

### Exceptional closures and changed sessions

The recurring rules cannot know a special closure. `exchange_calendar.yaml` in the
configuration folder holds dated, sourced overrides; **none ships**, and nothing is
fetched for it. A file that is missing means the recurring rules alone
(`recurring-rules`); one that is invalid (an override without a source, a reason, a
recorded time or a known venue, an early close without its time) is refused whole and
the recurring rules apply.

```yaml
version: 2026-11-a            # names this set; shown beside the status
overrides:
  - venue: Nasdaq             # as the exchange list names it, or "*" for every venue
    date: "2026-11-10"        # the day in the venue's own time zone
    kind: closed              # closed | early (needs close) | open (a session on a closed day; close optional)
    close: "13:00"            # for early and open
    reason: what happened
    source: where it was announced (a notice's address or reference)
    recorded: 2026-11-01T09:00:00Z   # when you learned of it; a reading as of an earlier time does not have it
observations:                 # halts and outages actually seen, not scheduled
  - venue: Nasdaq
    kind: halt                # halt | outage | closure
    from: 2026-11-10T15:00:00Z
    to: 2026-11-10T15:20:00Z  # empty while it is on
    source: where it was seen
    recorded: 2026-11-10T15:01:00Z
```

An override replaces the recurring rules for that venue and day (the newest by
`recorded` if there are several); an observation is shown beside the schedule as
"halted (halt)" with its source and does not change the schedule. Nothing records
observations automatically, and the entries in the tests are invented.

### Colours and words in the stock table

The figure columns carry a legend under the table. MCAP is the **issuer's** whole
capitalisation in US dollars (every class of stock), prefixed "issuer" on a class
that shares it; when there is no figure the cell says why in a word instead of a
blank or a zero: `stale`, `incomplete`, `unresolved`, `unsupported`, `failed`,
`pending` (the stock page gives the reason, the dates of the count and the price).
GROWTH and MARGIN use green for a positive and red for a negative value and yellow
for zero: the colour is the sign, never a judgement of the stock or a trade. The
insider columns use cyan and magenta only to tell BUYS from SELLS apart on the
list. Sorting and filtering see no number for a stock without a figure, so the
words and the order agree. Columns grow to their content except the text
columns declared with a maximum width (on the stock table NAME, TYPE, LISTED, DOMAIN
and TAGS: `MaxWidth` in `app/src/tui/companies.go`), which are cut with "…" at that
width; the full text is on the stock's page. Figures and states are never cut.

## The stock page loads the latest news

Opening a stock asks the backend for the newest articles that name it (and the other
tickers of the same company, such as GOOG and GOOGL), of any age and relevance, and
lists them under "Latest news" right after the profile, newest first; the dim ones are
below the relevance threshold. This is not limited to the window of the other
screens, so a stock that has been quiet for a month still shows what was last said of
it. The window's summary, timeline and charts follow when it holds anything.
