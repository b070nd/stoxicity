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
 All news  1 Tech (2)  2 Buffett core │ Home  Stocks  Companies …   <- watches, then screens (the key of each is underlined)
 all exchanges · sources 21 ok 2 stale 1 failing · next fetch sec-edgar in 0:42 · news 1,204 articles, 87 relevant · 5 unread
 Home › Stocks › NVDA                          <- breadcrumbs
 Stock NVDA · Nvidia Corp                      <- the screen
 ...
 → open  ← back  / filter  > sort  Space mark  # tag  ? help  quit
 refreshed 09:41 · default
```

- **Screen tabs.** After the watches the bar lists the screens that stand on their
  own: Home, Stocks, Companies, Topics, Domains, Exchanges, Sources and Watches. The
  one you are in is lit, and so is the one a detail sits under (a stock opened from
  Stocks lights Stocks, an exchange lights Exchanges). The key of each is a letter
  of its own name, underlined in the word: **h**ome, **s**tocks, **c**ompanies,
  **t**opics, **d**omains, **e**xchanges, s**o**urces, **w**atches. The screens that need something to show (a stock, a topic, a domain,
  a day, a search, one exchange) are not tabs; they open from the lists.
- **Watch bar.** `tab` and `shift+tab` go to the next and previous watch, a digit
  goes to that watch, `w` opens the list of watches. Switching keeps the
  screen: Home › Topics › AI infrastructure for one watch becomes the same topic
  page for the next. Loading a watch's screen marks its hits as seen.
  "All news" shows every article the tenant may see, with no watch applied.
- **Breadcrumbs.** The trail shows where you are in the hierarchy (Home › Exchanges
  › NYSE › KO). `←` (or `esc`, `backspace`) goes up one step, `→` (or `enter`) opens
  what the cursor is on, `]` goes forward again. Going back puts the cursor on the
  row you left.
- **Meters.** The line under the watch bar is always there: the exchange every
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
  the last one stand out in bold green for a few seconds and the status line
  counts them; a reload that changes nothing, and sorting, mark nothing. The
  cursor stays on its row, whatever moved.

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
| Watches | `tab` `shift+tab` next and previous · `1`–`9` the n-th |
| Screens | `h` `s` `c` `t` `d` `e` `o` `w` |
| Move | `↑` `↓` `pgup` `pgdn` `home` `end` · `→` open · `←` back · `]` forward |
| Query | `/` filter · `j` jump (catalogue) · `>` `<` sort · `i` invert · `g` group (catalogue) |
| Stocks | `Space` mark · `ctrl+u` clear marks · `#` tag · `l` list |
| Articles | `+` keep · `-` drop · `b` browser · `1`–`9` open its stock |
| Exchanges | `x` next · `ctrl+x` previous · `u` use this one · `↑` from the top: the cloud |
| Other | `r` refresh · `?` help · `q` quit |

### The exchange cloud

Home, Companies (and Stocks) and Exchanges start with a line of chips: **All** and
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
| `h` | Home | digest, top articles, stocks, domains, topic cloud, 14-day timeline |
| `s` | All stocks | every stock and ETF (about 16,000), those with news first: the catalogue below, opened with `sort:news` |
| | Stocks in the news | opened from the Home heading "Stocks ›": only the stocks in relevant news, with mentions, materiality and tone; ★ marks your watchlist |
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
below, dim when the candle is old (more than five days) or the day closed flat.
**CLOSE** and **CHG** (the move from the day before) take the same colour.
The ticker is bold, the exchange has a colour of its own (Nasdaq blue, NYSE
magenta, OTC yellow, CBOE cyan), the news count turns yellow with one article
and bold yellow with five, tags are magenta, and the star is yellow. The full
name comes last, so a long one runs to the edge of the screen and is never cut.
No table cuts a text or shortens it with an ellipsis: a column is as wide as its
longest entry.
**MCAP**, **REVENUE**, **GROWTH** and **MARGIN** appear once the SEC's figures are
stored (see `company.fundamentals` in [configuration.md](configuration.md)):
market value, revenue of the latest fiscal year, its growth over the year
before (green up, red down) and net income over revenue. A stock's page lists
all the figures with the note of where they come from. Try
`type:stock marketcap:>10b margin:>15% growth:>5% sort:-marketcap`.
**BUYS** and **SELLS** (green and red) appear once Form 4 filings are read (see
`company.insiders`): the money the company's insiders spent on open market
purchases, and took in on sales, in the last 90 days. A stock's page then has an
**Insiders** block: the last ten trades, each on two lines (day, what was done,
shares at price and value; who, their role, how many shares they hold after, and
whether it was under a Rule 10b5-1 plan or held indirectly), purchases green,
sales red, awards and tax withholding dim; `b` opens the filing. Try
`type:stock buys:>=250k sort:-buys`. A purchase says more than a sale, which is
often scheduled in advance; a fund that sits on the board (Baker Bros. in
Madrigal) counts as an insider because the form says so.
`sort:close`, `sort:change`, `sort:type` and `sort:domain` order by them (a
stock with no candle is last whichever way round). A move of more than five
times, or less than a fifth, between two closes a day apart is not a day's
move (mixed price series, a split not applied) and shows no change; the columns appear once
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

`e` opens the list of exchanges (NOW is `open` in the regular session, `extended`
before and after it, `closed` otherwise; ● marks the chosen one), and `u` on a row, or
on an exchange's page, chooses it (again: back to all). The page of an exchange has:

- **About.** Full name, operator, place, market ID (ISO 10383), currency, website
  (`b` opens it) and what kind of market it is.
- **Opening hours.** Whether it trades now and until when, each session in the market's
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

## The stock page loads the latest news

Opening a stock asks the backend for the newest articles that name it (and the other
tickers of the same company, such as GOOG and GOOGL), of any age and relevance, and
lists them under "Latest news" right after the profile, newest first; the dim ones are
below the relevance threshold. This is not limited to the window of the other
screens, so a stock that has been quiet for a month still shows what was last said of
it. The window's summary, timeline and charts follow when it holds anything.
