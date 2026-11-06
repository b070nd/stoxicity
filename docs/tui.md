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
 All news  1 Tech (2)  2 Buffett core         <- watches, unread badge
 sources 21 ok 2 stale 1 failing · next fetch sec-edgar in 0:42 · news 1,204 articles, 87 relevant · 5 unread
 Home › Stocks › NVDA                          <- breadcrumbs
 Stock NVDA · Nvidia Corp                      <- the screen
 ...
 Enter open  Esc back  / filter  > sort  Space mark  # tag  ? help  q quit
 refreshed 09:41 · default
```

- **Watch bar.** `tab` and `shift+tab` go to the next and previous watch, a digit
  goes to that watch, `W` (or `w`) opens the list of watches. Switching keeps the
  screen: Home › Topics › AI infrastructure for one watch becomes the same topic
  page for the next. Loading a watch's screen marks its hits as seen.
  "All news" shows every article the tenant may see, with no watch applied.
- **Breadcrumbs.** `esc` or `backspace` goes back, `]` forward. Going back puts
  the cursor on the row you left.
- **Meters.** The line under the watch bar is always there: how many sources are
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
  you are, and only those: `g jump` appears on the catalogue, `k keep` and
  `d drop` with the cursor on an article, `# tag` with the cursor on a stock.
  A key that does nothing here says why in the status line. `?` lists every key.
- **Status line.** When the data was last loaded, the tenant, how many rows are
  marked, and the last message or error. The screen reloads every minute (see
  `-refresh`) and on `r`. After a reload, the rows that are new or changed since
  the last one stand out in bold green for a few seconds and the status line
  counts them; a reload that changes nothing, and sorting, mark nothing. The
  cursor stays on its row, whatever moved.

## Typing

Every prompt (the filter bar, `#`, `L` and `g`) edits the same way: `←` `→`
`ctrl+a` `ctrl+e` move inside the line, `backspace` and `delete` cut a character,
`ctrl+w` a word, `ctrl+u` to the start, `ctrl+k` to the end. In the filter bar
and the tag and list prompts `↑` and `↓` recall what you typed before, each kind
of prompt with a history of its own (the last 50). `enter` accepts, `esc`
cancels.

## Columns, sorting and marks

The tables (Stocks, Topics, Domains, Sources and the catalogue) have a header
with the sorted column marked ▲ or ▼. `>` and `<` move the sort to the next and
previous column, `I` turns it round; a column that counts starts with the
largest first. The choice is remembered for the kind of screen. The cursor stays
on the row it was on. In the catalogue the columns map onto `sort:` (typing a
query with its own `sort:` replaces your choice).

`space` marks the stock under the cursor (●) and moves down, so holding it marks
a run. `#` and `L` then tag or list every marked stock in one go; `U` clears the
marks. Marks stay while you move between screens.

## Screens

| Key | Screen | What it shows |
|-----|--------|---------------|
| `H` | Home | digest, top articles, stocks, domains, topic cloud, 14-day timeline |
| `s` | Stocks | stocks in relevant news with mentions, materiality and tone; ★ marks your watchlist |
| `c` | Companies | the catalogue of all stocks and ETFs (about 12,000), narrowed as you type, with counts by exchange, kind, industry, list and tag |
| | Stock | profile (identity, listing, industry, links, your lists and tags, watches), then summary, weekly tone and materiality, timeline, what happened, domains, topics, articles |
| `t` | Topics | tag cloud and a table with the lift over the window before |
| | Topic | its stocks, domains, timeline, related topics, top articles |
| `D` | Domains | relevant against total per domain |
| | Domain | its output, your precision on it (kept against dropped), timeline, articles |
| | Day | one day's digest, relevant articles first |
| | Search | results with domain and event counts |
| `S` | Sources | state of every source: health, what it is doing, last good poll, next due, failures, last error; failing ones first |
| `W` | Watches | the tenant's watches with unread counts; `enter` shows one |

`enter` on a row opens it: a heading opens its list, a table row opens its page,
an article opens its detail (score, the rules that fired, summary, your label).
`k` keeps and `d` drops the article under the cursor, in a list or in the detail;
those verdicts are the gold set for `eval` and for calibrating the lens. `o`
opens the article in your browser, for `http` and `https` addresses only; on a
stock profile it opens the company website or its EDGAR filings.

## Companies, tags and lists

`c` opens the catalogue of every company, not a shortlist: all of them are rows,
by ticker, and the status line says where you are ("company 4,521 of 12,345").
`home`, `end`, `pgup` and `pgdn` move through it, and `g` jumps: type the start
of a ticker (or of a name, when sorted by name) and the cursor lands on the first
one that starts with it. `sort:name`, `sort:news`, `sort:industry`,
`sort:exchange` and `sort:kind` change the order ([universe.md](universe.md) has
the whole query language and the export). `/` there filters the list as you type: `exchange:nyse tag:moat -etf`. `enter`
keeps the query as part of the breadcrumb, `esc` leaves the list as it was.
`enter` on a company opens its profile, which also exists for stocks with no news
in the window.

On a stock page, or with the cursor on any stock row: `#` asks for tags
(`moat dividend-grower`, or `-moat` to remove) and `L` for lists (a name that
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
