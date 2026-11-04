# Official sources: what was read, and what each licence allows

Read on 2026-11-04 from the publishers' own feed pages, terms pages and
robots.txt files (fetched through a web-fetch agent; the tool returns converted
text, so quotes are as the tool returned them). This is a reading of the terms,
not legal advice. `app/config/sources.yaml` is the result; this file is the
evidence behind it.

## Decisions at a glance

| Source | Feeds used | Licence set | Enabled | Why |
|---|---|---|---|---|
| SEC | press releases, speeches, litigation releases, trading suspensions | redistributable | yes | "considered public information and may be copied or further distributed" (privacy-information, Website Dissemination) |
| Federal Reserve Board | all press releases (includes FOMC and enforcement), speeches and testimony | redistributable | yes | "in the public domain and may be copied and distributed without permission" (disclaimer, Copyright) |
| BLS | JOLTS (Atom) | redistributable | yes | "everything that we publish ... is in the public domain" (copyright information) |
| BEA | news releases | internal | **no** | apps.bea.gov/robots.txt: `User-agent: *` `Disallow: /`, `/rss/` not in its Allow list; no reuse sentence found |
| ECB | press (includes speeches and monetary policy decisions), statistical press releases; publications off | internal | yes | copyright is the ECB's; the disclaimer allows reproduction with the ECB cited, but that wording was read as a summary only |
| Eurostat | news releases (Atom) | redistributable | **no** | CC BY 4.0 and reuse authorised with source; but robots.txt disallows the search-portlet addresses that the feed is served from |
| EBA | news | redistributable | yes | "Reproduction of all EBA material on this site is authorised, provided the source is acknowledged" |
| Bank of England | news, speeches, publications; prudential off | internal | yes | "typically grants permission for non-commercial re-use" |
| FCA | news | internal | **no** | terms bar scrapers, robots and bots without written consent (4.8) and data feeds to comparison tables (3.4); feed dates need a custom parser |
| Swiss National Bank | press releases, monetary policy, speeches | internal | yes | reuse "for non-commercial purposes", commercial use not addressed |

`internal` means only the owner tenant sees the articles, so nothing is
republished by accident. Move a source to `redistributable` only after reading
the cited page.

## Feed facts that shaped the code

- **SEC**: RSS 2.0, `guid` is a UUID with `isPermaLink="false"`, some `link`
  values carry a trailing newline (trimmed), descriptions are truncated lead-ins.
  Trading-suspension links point to PDFs. The SEC's own limit is 10 requests a
  second and it blocks undeclared tools, which the HTTP client already honours.
- **Fed**: RSS 2.0, RFC 822 GMT dates, `guid` equals the link. Speech
  descriptions can name a different event than the title; do not read them as
  the event name. robots.txt answers 404, so it states no rule.
- **BLS**: `jolts.rss` is Atom despite its name. `bls_latest.rss` is a single
  item rewritten in place with no `guid` and one shared link, so a poll cannot
  tell a new value from the old one; it is not used.
- **ECB**: RSS 2.0 served as `.html`; no descriptions on press items; links
  contain a double slash (`ecb.europa.eu//press/...`), now collapsed by the RSS
  fetcher so one article keeps one identity. `Crawl-delay: 5`.
- **EBA**: RSS 2.0, ten items, `Crawl-delay: 10`; the site was offline for about
  25 hours on 21 to 22 October 2026, so expect gaps.
- **Bank of England**: RSS 2.0, mixed date forms (`... 14:51:45 Z` and
  `... +0100`), brace `guid`s with `isPermaLink="false"`. A speech item can be a
  forthcoming event. No separate MPC feed: summaries arrive in `/rss/news`.
- **SNB**: RSS 2.0 with `dc:date` in UTC and URL `guid`s. The page says RSS 1.0
  but the files are 2.0. Press-release and policy items have no description.
- **Eurostat**: Atom, 408 pages because it is a search result; the newest page
  is the one polled.

`app/src/feeds/rss/official_test.go` pins the four quirks that could have lost
or mis-dated articles (trailing newline in links, `Z` zones, double slashes,
`dc:date`), on invented items in those shapes.

## Left out, and why

- **Treasury**: no press-release RSS exists on home.treasury.gov (only an email
  subscription). The yield curve is `yield.xml`, an XML table (`QR_BC_CM`) that is
  not a feed and needs a fetcher of its own. No reuse sentence was found.
- **BLS CPI, employment, PPI**: no per-release stream was found; the BLS
  index page `rssfeeds.htm` is a 404. The release calendar
  (`bls.ics`) and the BLS API are the next things to try.
- **ESMA**: no RSS link on its site. **FINMA**: feeds exist (German, all rights
  reserved, prior written consent required). **London Stock Exchange RNS**: no
  free feed found. **European Commission forecasts**: not checked.
- **Wording not read verbatim**: the ECB disclaimer and the Eurostat
  exceptions list (third-party and some trade data are non-commercial only).

## Still open

- **robots.txt is not enforced in code.** The HTTP client leaves it to callers
  (httpx.go). The two sources where it matters (BEA, Eurostat) are off by hand.
  A check at source load would make that rule mechanical.
- Update frequency is stated by none of these publishers, so the default 30
  minute interval is a choice, not a requirement. Release days (FOMC, CPI, GDP)
  are where a shorter interval pays; the BLS and BEA calendars are machine
  readable.
