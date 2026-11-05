# The full-screen interface

`stoxicity-tui` reads what `stoxicity fetch` and `stoxicity run` have stored. It
never fetches and never scores; it shows the shared base through the licence
rule of your tenant, and writes only three things: your keep/drop labels, the
read marker of a watch, and your tags and lists (`stocks.yaml`).

```sh
stoxicity-tui                       # the default tenant and its default watch
stoxicity-tui -tenant alice -watch buffett-core
stoxicity-tui -root path/to/config  # default: the closest config folder above the working directory
```

## Layout

```
 All news  1 Tech (2)  2 Buffett core         <- watches, unread badge
 Home › Stocks › NVDA                          <- breadcrumbs
 Stock NVDA · Nvidia Corp                      <- the screen
 ...
 / filter · ? help
 refreshed 09:41 · default
```

- **Watch bar.** `tab` and `shift+tab` go to the next and previous watch, a digit
  goes to that watch, `W` (or `w`) opens the list of watches. Switching keeps the
  screen: Home › Topics › AI infrastructure for one watch becomes the same topic
  page for the next. Loading a watch's screen marks its hits as seen.
  "All news" shows every article the tenant may see, with no watch applied.
- **Breadcrumbs.** `esc` or `backspace` goes back, `]` forward. Going back puts
  the cursor on the row you left.
- **Filter bar.** `/` takes a Google-style query (the syntax is in
  `app/src/filter`): `"free cash flow" -rumor ticker:AAPL site:sec.gov`. `enter`
  opens the results as a screen of their own; a query that does not parse is
  reported and stays in the bar.
- **Status line.** When the data was last loaded, the tenant, and the last error.
  The screen reloads every minute and on `r`.

## Screens

| Key | Screen | What it shows |
|-----|--------|---------------|
| `H` | Home | digest, top articles, stocks, domains, topic cloud, 14-day timeline |
| `s` | Stocks | stocks in relevant news with mentions, materiality and tone; ★ marks your watchlist |
| `c` | Companies | every known company, narrowed as you type, with counts by exchange, kind, industry, list and tag |
| | Stock | profile (identity, listing, industry, links, your lists and tags, watches), then summary, weekly tone and materiality, timeline, what happened, domains, topics, articles |
| `t` | Topics | tag cloud and a table with the lift over the window before |
| | Topic | its stocks, domains, timeline, related topics, top articles |
| `D` | Domains | relevant against total per domain |
| | Domain | its output, your precision on it (kept against dropped), timeline, articles |
| | Day | one day's digest, relevant articles first |
| | Search | results with domain and event counts |
| `S` | Sources | health of every source, failing ones first |
| `W` | Watches | the tenant's watches with unread counts; `enter` shows one |

`enter` on a row opens it: a heading opens its list, a table row opens its page,
an article opens its detail (score, the rules that fired, summary, your label).
`k` keeps and `d` drops the article under the cursor, in a list or in the detail;
those verdicts are the gold set for `eval` and for calibrating the lens. `o`
opens the article in your browser, for `http` and `https` addresses only; on a
stock profile it opens the company website or its EDGAR filings.

## Companies, tags and lists

`c` opens the company list ([universe.md](universe.md) has the query language).
`/` there filters the list as you type: `exchange:nyse tag:moat -etf`. `enter`
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
