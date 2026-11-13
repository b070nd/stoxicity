# Is there a free, authoritative source of ticker changes, mergers and delistings?

Research date: **2026-11-13**. Pages were read through an automated page reader (a small model summarises the page text, so a summary can be wrong). Where a claim matters, the original text was checked: the SEC Form 8-K and Form 25 instructions, the SEC Form 25 final rule, the Nasdaq Daily List specification and the SEC filing on Nasdaq's Daily List fees were downloaded as PDF and read as extracted text. Primary sources only (sec.gov, nasdaqtrader.com, nyse.com, cboe.com, finra.org, openfigi.com, massive.com). Secondary pages were used only to find primary ones and are labelled. No login, no API key, and no `.env` file was used. Nothing was run against the repo. This is not legal advice.

## Short answer

- **No free source states ticker-by-date history for US stocks.** Not the SEC, not an exchange, not OpenFIGI. The nearest are Massive's ticker events (paid beyond 2 years, personal-use licence, rename only) and the Nasdaq Daily List (about USD 3,500 a month, Nasdaq-listed names only).
- **SEC EDGAR is the only free, authoritative, reusable source, but it gives documents and company-level dates, not a symbol timeline.** It has delisting notices (Form 25 and 25-NSE, electronic since 2006), name-change dates (`formerNames`), and the 8-Ks and merger documents a person checks against. Its ticker fields are current only.
- **Recommendation.**
  1. Use EDGAR as the **verification layer**: the `source_ref` of a span is an EDGAR accession number, and `verified = 1` means a person opened that filing.
  2. Use Massive's `ticker_change` events (and `delisted_utc`) as the **candidate generator** you already pay for, imported with `verified = 0`. Read its licence first (section 8): rows that come from it are Massive "Information".
  3. Fill `kind = merger`, `spinoff` and `successor` by hand from the 8-K Item 2.01 text and merger documents. No source reviewed gives a structured successor.
  4. Treat the Nasdaq Daily List as optional and paid. Revisit it only if Nasdaq's free-file proposal (section 5) went live.
- Nothing reviewed fills `relisting`.

## 1. Which source fills which column of `security_symbol`

Legend: **yes** = stated by the source; **partly** = with a caveat; **no** = not in the source.

| Column | SEC EDGAR (free) | Massive/Polygon (paid beyond 2 years) | Nasdaq Daily List (about USD 3,500/month) | OpenFIGI (free) |
|---|---|---|---|---|
| `security` (key) | partly: CIK is per issuer, not per share class (GOOG and GOOGL share one) | partly: `composite_figi`, `share_class_figi`, `cik` returned by the tickers endpoints | no | yes: share-class FIGI, persistent |
| `symbol` | partly: current `tickers` only; a past symbol appears only inside an old filing's cover page | yes, through events or the `date` parameter | yes (Symbol, New Symbol) | no history, no date |
| `valid_from` / `valid_to` | partly: name-change dates, filing dates, Form 25 filing date | yes: event `date`; `delisted_utc` (last trade day) | yes: Effective Date | no |
| `kind` rename | partly: `formerNames` shows a name change, not a ticker change | yes, but only as `ticker_change`; reason never stated | yes: Name/Symbol Changes event | no |
| `kind` merger | partly: Form 25 box 12d2-2(a)(3) (substitution), 8-K Item 2.01 text | no: some mergers leave no event at all | partly: Delisting Reason "Acquisition/Merger" | no |
| `kind` delisting | yes: Form 25 / 25-NSE filing | yes: `active=false`, `delisted_utc` | yes: Issue Deletions with a reason list | partly: `includeUnlistedEquities` flag only |
| `kind` spinoff | partly: Form 10 / 8-K text | no | partly: a Separation Date field exists | no |
| `kind` relisting | no | no | no | no |
| `successor` | no structured field (read the 8-K or merger document) | no | no field named successor in the spec | no |
| `source` / `source_ref` | yes: accession number | yes: endpoint + query | n/a | n/a |
| `verified` | yes: this is where a person verifies | no | no | no |

Evidence for each cell is in sections 2 to 7.

## 2. SEC EDGAR

### 2.1 Submissions API and `formerNames`

- Endpoint family `data.sec.gov/submissions/CIK##########.json`. The SEC says it holds "metadata such as current name, former name, and stock exchanges and ticker symbols" and the filing history. No key is needed. [EDGAR APIs](https://www.sec.gov/search-filings/edgar-application-programming-interfaces), read 2026-11-13.
- Updated "throughout the day, in real time"; the bulk file `submissions.zip` (all filers) is republished nightly at about 3:00 a.m. ET (same page).
- `filings.recent` holds at least a year or 1,000 filings, whichever is more; larger filers have extra linked files (same page).
- **Observed fields** (live JSON read 2026-11-13, [Meta](https://data.sec.gov/submissions/CIK0001326801.json)): `cik, name, tickers, exchanges, sic, formerNames, filings` and more. Meta showed `tickers` META and METAM, and `formerNames` with one entry "Facebook Inc" from 2005-05-06 to 2021-10-27. Each filing row has `form, filingDate, reportDate, acceptanceDateTime, items` (parallel arrays).
- **Observed: `formerNames` dates are name-change dates, not ticker-change dates.** Meta's former name ends 2021-10-27, while Massive's documented FB to META event is dated 2022-06-09 ([Massive ticker events](https://massive.com/docs/rest/stocks/corporate-actions/ticker-events.md)). Do not put `formerNames` dates in `valid_from`/`valid_to`.
- **`tickers` and `exchanges` are current only.** Twitter's record ([CIK 1418091](https://data.sec.gov/submissions/CIK0001418091.json)) shows both arrays empty, `formerNames` "Twitter Inc" 2007-11-30 to 2010-01-08, and no ticker. So a delisted company loses its symbol in this API.
- `formerNames` has no reason, no successor, and no way to tell a rename from anything else. How far back it goes for old companies: not stated.
- The file `cik-lookup-data.txt` is "cumulative", so one CIK can list several names, but with no dates. [Accessing EDGAR data](https://www.sec.gov/search-filings/edgar-search-assistance/accessing-edgar-data), read 2026-11-13.

### 2.2 `company_tickers*.json`

- `company_tickers.json` ("ticker, CIK, EDGAR conformed company name associations"), `company_tickers_exchange.json` (adds exchange), `company_tickers_mf.json` (fund series and class). The SEC "periodically update[s] these files ... but do[es] not guarantee accuracy or scope." ([Accessing EDGAR data](https://www.sec.gov/search-filings/edgar-search-assistance/accessing-edgar-data).)
- Observed shape: `company_tickers.json` rows have `cik_str`, `ticker`, `title`; the exchange file adds an exchange (Nasdaq, NYSE, OTC and so on). Both are snapshots of today. Not a history. Fields of the exchange file are my reading of the row values: the reader found no field names.

### 2.3 Form 25 and 25-NSE (delisting notices)

- Form 25 is the "Notification of Removal from Listing and/or Registration under Section 12(b)". The form names the issuer, the exchange, the class of securities, and a box for the rule relied on: 12d2-2(a)(1) to (a)(4), (b) exchange rules, or (c) issuer voluntary withdrawal. [Form 25 PDF](https://www.sec.gov/files/form25.pdf), text read 2026-11-13.
- Per 17 CFR 240.12d2-2(a), as quoted in [Cornell LII](https://www.law.cornell.edu/cfr/text/17/240.12d2-2) (secondary host of the rule, not eCFR; eCFR redirected the reader): (a)(1) the class is called for redemption, maturity or retirement; (a)(2) already redeemed or paid; **(a)(3) the class "evidence[s] ... other securities in substitution", with at most an immediate cash payment (this is what a merger looks like)**; (a)(4) all rights extinguished.
- Timing: "The removal of the class of securities from listing on the exchange shall be effective 10 days after filing the Form 25"; withdrawal of registration after 90 days (Form 25, General Instructions 4 and 5). The exchange may choose a later date, at least 10 days after filing ([final rule](https://www.sec.gov/files/rules/final/34-52029.pdf), text read).
- **The Form 25 filing date is not the last trading day.** The form has no field for the last trade date or the successor. A sample issuer form (Harmony Gold, [2010](https://www.sec.gov/Archives/edgar/data/0001023514/000120561310000094/harmony_form.htm)) states no effective date, no successor and no reason beyond the voluntary-withdrawal box. Twitter's 25-NSE shows as filed 2022-10-28 ([EDGAR filing list for CIK 1418091](https://www.sec.gov/cgi-bin/browse-edgar?action=getcompany&CIK=1418091&type=25&dateb=&owner=include&count=40)), whose own description reads "removal from listing and registration of securities that matured, were redeemed, or were retired" even though Twitter was acquired. So the form-type description is generic and must not be read as the reason.
- Who files: exchanges file for exchange-initiated delistings, issuers for voluntary ones ([proposing release](https://www.sec.gov/rules-regulations/2004/06/removal-listing-registration-securities-pursuant-section-12d-securities-exchange-act-1934)). EDGAR labels the exchange's form `25-NSE` (observed on the Twitter list).
- **History depth: electronic Form 25 starts with the 2005 rule.** The final rule requires the amended Form 25 "in an electronic format ... on the EDGAR database", effective 2005-08-22, with a compliance date of 2006-04-24. Earlier delistings are not covered by this route.
- Whether the "rule provision" box is machine-readable (not only inside the document): could not verify.

### 2.4 Form 8-K events

From the [Form 8-K instructions](https://www.sec.gov/files/form8-k.pdf) (text read 2026-11-13):

- Cover page has "Title of each class / Trading Symbol(s) / Name of each exchange", so every 8-K states the symbol the company used on that filing date. It is the best dated per-document ticker evidence EDGAR has, and a person can read it for verification.
- **Item 3.01** (Notice of Delisting or Failure to Satisfy a Continued Listing Rule) covers continued-listing failures and a board's decision to withdraw a listing, and states the date. **Its Instruction 1 exempts delistings caused by redemption or by securities coming to evidence other securities in substitution** (merger-type). So a merger delisting often has no Item 3.01.
- **Item 5.03** covers charter or bylaw amendments with "the effective date of the amendment". A name change usually sits here. It says nothing about a ticker.
- **Item 2.01** (Completion of Acquisition or Disposition of Assets) is the natural document for `kind = merger`. It is text; no structured successor field.
- Due within four business days of the event (same instructions). Submissions rows have an `items` array, so a bulk scan for 3.01, 5.03 and 2.01 is possible, but the item text is prose.

### 2.5 Index files and full-text search

- Daily and quarterly indexes, 1994 Q3 to now, each with company, form, master and XBRL sorts; they list form type, company, CIK, date filed and file name. Daily bulk archives exist under `/Archives/edgar/Feed/`. ([Accessing EDGAR data](https://www.sec.gov/search-filings/edgar-search-assistance/accessing-edgar-data).) A form-type scan of the index for `25` and `25-NSE` gives every delisting notice in one pass (inferred from the above; not run).
- EDGAR full-text search covers filings "since 2001" ([FTS FAQ](https://www.sec.gov/edgar/search/efts-faq.html)). The page says nothing about an API or rate limits. Use it by hand for verification, not as a feed.

### 2.6 Terms and limits

- "No more than 10 requests per second, regardless of the number of machines used"; excess may be throttled or blocked. Information on sec.gov is public and may be copied or redistributed without permission, with credit to the SEC requested. SEC seal, logos and the EDGAR trademark names are restricted. ([SEC privacy and security policy](https://www.sec.gov/privacy.htm), read 2026-11-13.)
- Declare a user agent with a company name and contact email ([Accessing EDGAR data](https://www.sec.gov/search-filings/edgar-search-assistance/accessing-edgar-data); the page does not say it is mandatory). The same privacy page does not mention user agents; the policy says it does not permit "unclassified bots" to crawl the site, so use the documented data endpoints and indexes, not page crawling.
- The SEC FAQ gives no support for programmatic downloads and does not address storing data ([Webmaster FAQ](https://www.sec.gov/about/webmaster-frequently-asked-questions)).

### 2.7 How a user verifies a mapping against EDGAR

1. Open the filing: `https://www.sec.gov/Archives/edgar/data/<CIK>/<accession without dashes>/` (the folder form worked in this research).
2. For a delisting: read the Form 25 (rule box, exchange, class) and the filing date. Then read the 8-K or the merger document for the closing date; the last trading day comes from there or from the price series itself, not from the form.
3. For a rename: the 8-K cover page (Trading Symbol(s)) before and after, plus Item 5.03 for the charter date. Put the accession in `source_ref`.

## 3. Massive (formerly Polygon.io), the provider the repo already uses

The repo calls `https://api.polygon.io` with the key as an `apiKey` query parameter (seen in `app/src/marketdata/candles.go`, `splits.go` and `intraday_fetch.go`, host and auth style only). Whether `api.massive.com` is the new host: could not verify.

### 3.1 Ticker events

- `GET /vX/reference/tickers/{id}/events`, `id` = ticker, CUSIP or Composite FIGI. Marked "experimental". Only supported event type: `ticker_change`. Response: `results.name` and `results.events[]`, each with `date`, `type`, and `ticker_change.ticker`. ([Docs](https://massive.com/docs/rest/stocks/corporate-actions/ticker-events.md), read 2026-11-13.)
- Example in the docs: `META` on 2022-06-09 and `FB` on 2012-05-18.
- **Plans and depth:** in all Stocks plans; "Records date back to September 10, 2003"; **Basic gives 2 years, Starter and above give all history** (same page). Updated daily.
- **Semantics from the knowledge base** ([Massive KB](https://massive.com/knowledge-base/article/how-does-massive-handle-ticker-changes-and-acquisitions), read 2026-11-13):
  - Renames and other causes both appear only as `ticker_change`; "the reason is never given".
  - For an acquired or delisted symbol, the events endpoint returns 404.
  - **Some mergers leave no trace in the events endpoint** (the KB's example is RTX, which shows one event).
  - Massive does not merge a renamed company's history; "you must join them yourself".
  - With a ticker as `id`, the events are those of the entity currently using it; for an earlier holder, take the identifier from Ticker Details first.
- Whether `date` is the first day of trading under the new symbol or an announcement date: **not stated**.

### 3.2 Delisted tickers and point-in-time lookups

- `GET /v3/reference/tickers?active=false` and `GET /v3/reference/tickers/{ticker}` return `active`, `delisted_utc` ("the last date the asset was traded"), `cik`, `composite_figi`, `share_class_figi`, `primary_exchange`, `list_date`; `date=` gives who held the ticker on that day. Same plan and depth as above (all Stocks plans; Basic 2 years, since 2003-09-10). ([All tickers](https://massive.com/docs/rest/stocks/tickers/all-tickers.md) and [Ticker overview](https://massive.com/docs/rest/stocks/tickers/ticker-overview.md), read 2026-11-13.)
- `delisted_utc` "comes a few days after the last trading bar, not on it" (KB above). Do not use it as the last trading day without checking the bars.
- The `date` parameter on Ticker Details matches SEC-derived data to the filing's period of report, not the submission date (Ticker overview page). The KB's own example: META on 2022-01-03 was an ETF, not Meta Platforms.
- Merger versus delisting: **not distinguished**; both just end the series. Successor: **no field**.
- Point-in-time tickers API launched 2021-03-03; the blog example shows data from 2006, with no stated maximum lookback ([blog](https://massive.com/blog/new-point-in-time-tickers-api)). Compare the 2003 start on the docs pages; the docs win.

### 3.3 Plans, limits, licence

- The free tier: "5 requests per minute" per asset class not subscribed to; a subscribed asset class has no request limit; extra keys do not raise it ([KB: request limits](https://massive.com/knowledge-base/article/what-is-the-request-limit-for-massives-restful-apis)). Docs for the events and tickers pages say "Basic", "Starter", "Developer", "Advanced", "Business" exist. The pricing page read on 2026-11-13 listed only Stocks Advanced at USD 199 a month (individual use, "20+ years of history") and no Basic or Starter row, so what a free or cheap plan costs today: **could not verify**. Which plan the repo's key has: not checked, as keys were out of scope.
- **Licence (individual terms and market-data terms, read 2026-11-13).** [Individuals terms](https://massive.com/legal/individuals-terms-of-service) allow "your own personal applications and analyses" and "solely for your own personal, non-commercial, and non-business purposes". [Market data terms](https://massive.com/legal/market-data-terms-of-service): no sharing or redistribution of the Information; **on licence end, delete the Information (downloaded files, stored copies, caches) within 30 days, backups within 90 days**; you may keep "your own analyses, code and other results" that "do not contain Information and cannot reasonably be used to recreate it"; no training of models on it without express permission. Derived data counts as covered ("any data derived from either, in whole or in part", Individuals terms section 1).
- Consequence for this repo: ticker events and `delisted_utc` stored in `security_symbol` are Information while they stay as imported from Massive. A row that a person verified against an EDGAR filing and whose `source` is the EDGAR document is not. Whether a verified date copied from Massive then counts as "your own analysis": **a legal question I did not resolve; ask Massive support or keep SEC-sourced dates**.

## 4. OpenFIGI

- Mapping API `POST /v3/mapping` returns `figi, securityType, marketSector, exchCode, securityType2, ticker, name, shareClassFIGI, compositeFIGI, securityDescription`. Listed equities by default; `includeUnlistedEquities: true` adds unlisted and delisted ones. "Once issued, the FIGI assigned to an instrument will not change." There is **no as-of or effective-date parameter**, and the page does not say whether past tickers are returned. ([API documentation](https://www.openfigi.com/api/documentation), read 2026-11-13.)
- Limits: mapping 25 requests/min without a key (10 jobs per request), 250/min with a free key (100 jobs); search 5 or 20 per minute (same page).
- Licence: the FAQ says "no cost recovery, licensing or re-use restrictions or hidden fees" ([FAQ](https://www.openfigi.com/about/faq)). The [terms](https://www.openfigi.com/docs/terms-of-service) allow identifiers to be "freely reproduced, distributed ... built upon", provide them "AS IS", and cap liability at USD 50. They say nothing on API use, rate limits or caching, and it is unclear whether descriptions (names) are covered like identifiers.
- Use for this repo: a stable share-class key (`security`), and a cross-check that a Massive `composite_figi` still maps to the same share class. Not a source of dates.

## 5. Nasdaq

- **Symbol directory** (`nasdaqlisted.txt`, `otherlisted.txt`, public FTP `ftp.nasdaqtrader.com/symboldirectory`): current listings; fields are symbol, security name, market category, test issue, financial status, round lot (Nasdaq-listed) and ACT/CQS/Nasdaq symbol, exchange, ETF flag (other-listed). "Updated periodically through-out each day"; the page says nothing about delisted symbols or history. ([Definitions](https://www.nasdaqtrader.com/trader.aspx?id=symboldirdefs), [Symbol directory](https://www.nasdaqtrader.com/Trader.aspx?id=symbollookup), read 2026-11-13.)
- `TradingSystemAddsDeletes.txt` at `ftp.nasdaqtrader.com/dynamic/SymDir` is listed on the definitions page; its content and history depth are not described: **could not verify**.
- **Daily List** (the real change history). Spec ([PDF](https://m.nasdaqtrader.com/content/technicalSupport/specifications/dataproducts/dlcompletespec.pdf), text read): pipe-delimited rows with `Daily List Date | Effective Date | Issue Event | Symbol | Company Name | New Symbol | New Company Name | ... | Delisting Reason | ... | Separation Date | ...`; Issue Events include Name/Symbol Changes and Issue Deletions; a `Delisting Reason` list includes Acquisition/Merger, Liquidation, Form 15, Listed on NYSE, Called for Redemption and others. A variant carries Old CUSIP and CUSIP. No field is named successor.
- **Cost and access:** "Corporate action data back to 1999"; delivery by secured FTP (current month) and a secured website; ordering requires prior Nasdaq approval and a Data Feed Request Form and Nasdaq Global Data Agreement; no free access mentioned ([Daily List page](https://www.nasdaqtrader.com/Trader.aspx?id=dailylistpd)). An SEC filing from 2026-08-04 states the fee for the Daily List and Fundamental Data is "currently $3,500 per month" and that "full historical information is available starting on May 24, 1999" ([SR-NASDAQ-2026-062](https://www.sec.gov/files/rules/sro/nasdaq/2026/34-106034.pdf), text read).
- **Proposed change (status unknown).** The same filing proposes publishing, free "for any purpose (including redistribution)" on the public FTP, Issue Events (additions, suspensions, issue deletions, market class changes, financial status changes), Downgrade Reason, Financial Status and others, at least 15 minutes before the Daily List. Name and symbol changes are not in that list; Nasdaq says it will delay them 15 minutes against issuer press releases instead. The filing says the implementation date will be announced by Exchange Notice no later than 90 days after the operative date. Whether it went live, and whether any history would be free: **could not verify** (no notice found). The proposal is forward-looking and says nothing about back history.
- Nasdaq covers only Nasdaq-listed names. NYSE and Cboe names are outside it.

## 6. NYSE, NYSE American, NYSE Arca, Cboe

- **NYSE Corporate Actions for NYSE Group Listings** is a **paid** ICE/NYSE product (InfoNotices for name and symbol changes and M&A; TickerNotices for symbol changes, splits, redemptions, suspensions). Delivery by SFTP, email and HTTP; text, Excel, XML. History depth, price and redistribution terms are not stated on the product page ([product page](https://www.nyse.com/market-data/corporate-actions/corporate-actions-for-nyse-group-listings), read 2026-11-13).
- The public side: a daily corporate actions list of **upcoming** events (issuer name, symbol, anticipated date, action type, status) with no archive, and a trading-halts list that does have history. Detailed info notices need a Market Data subscription. ([Compliance page](https://www.nyse.com/regulation/corporate-actions-market-watch-proxy-compliance), read 2026-11-13.) The same page states issuers must give notice and announce a name, symbol or CUSIP change at least ten calendar days before it is effective, which is why the effective date should be stated and early.
- A search result said NYSE publishes two daily lists of issues pending delisting; the notices page read directly does not mention them. Treat as **could not verify**.
- **Cboe BZX equities** publishes a public list of issues pending suspension or delisting: issuer name, symbol, reason, suspension or transfer date, press release link. It shows only current pending items, with no archive ([Cboe page](https://www.cboe.com/us/equities/listings/listed_products/suspensions_delistings), read 2026-11-13). A "Liquidation" or "transfer" reason is stated; symbol-change history: none found.
- Neither NYSE nor Cboe offers a free historical list as far as the primary pages show. A script could snapshot the public upcoming lists daily to build history from now on, but that does not help with past years. Whether their terms allow that automated snapshotting: **could not verify**.

## 7. FINRA and OCC

- **FINRA Daily List** announces OTC corporate actions including "new and deleted issues and symbol and name changes", and "FINRA does not 'approve' corporate actions; it 'processes' them" ([FINRA investor page](https://www.finra.org/investors/insights/corporate-actions-public-companies-what-you-should-know), via search result, not fetched in full). Exchange-listed companies are handled by their exchange, so this is the OTC side only. The live list page ([otce.finra.org/otce/dailyList](https://otce.finra.org/otce/dailyList)) rendered as an "unsupported browser" notice. History depth, bulk access and terms: **could not verify**.
- **OCC contract adjustment memos** (options): per the Options Industry Council page, they are searched by company name or symbol in OCC's Information Memos, and an adjusted option symbol carries a numeral after the letters ([OIC FAQ](https://www.optionseducation.org/referencelibrary/faq/splits-mergers-spinoffs-bankruptcies), read 2026-11-13). The OCC pages returned HTTP 403 to the reader. Content, history depth and terms of use: **could not verify**. In principle a memo states the old symbol, new symbol, effective date and the merger deliverable; this is from search snippets of exchange-hosted copies and is unverified.

## 8. Not primary sources, noted only

- EODHD sells a "symbol change history" API for US exchanges (fields old symbol, new symbol, company name, effective date, per a search result from its own documentation pages; one page suggests it starts 2022-07-22). It is a paid vendor product and was not read in full: **could not verify**.
- Wikipedia, news sites and forum threads were not used.

## 9. How a user can verify one mapping end to end

Take FB to META:

1. Candidate from Massive: events for the Composite FIGI show `FB` 2012-05-18 and `META` 2022-06-09 (as in the docs example).
2. EDGAR: `formerNames` shows only the company name change (to 2021-10-27); open the 8-K for the 2021 name change and the cover page of the first 8-K after the ticker change to read the Trading Symbol. The accession goes into `source_ref`.
3. Price series: a rename must show a continuous series across the date (the repo's own `LoadSeries` join). A gap or overlap means the date or the mapping is wrong.
4. Only then set `verified = 1`.

For a delisting: Form 25 or 25-NSE (filing date, rule box), the merger 8-K (closing date), and the last candle in the repo's own data.

## Could not verify

- Whether Nasdaq's free Issue Events file went live (SR-NASDAQ-2026-062), and whether it has any back history.
- Contents and depth of `TradingSystemAddsDeletes.txt`.
- Whether the Form 25 "rule provision" box is machine-readable in EDGAR.
- Whether Massive's ticker-event `date` is the first trading day under the new symbol.
- Which Massive plan is needed today for all history at the lowest price (the pricing page showed only Advanced), and whether `api.massive.com` replaces `api.polygon.io`.
- Whether a human-verified date originally copied from Massive stays outside the Massive deletion duty after the licence ends.
- FINRA Daily List history and terms; OCC memo content, depth and terms; NYSE "pending delisting" lists; whether NYSE or Cboe terms allow automated snapshots.
- eCFR text of 12d2-2 (the reader was redirected; Cornell LII was used instead).
- Whether the SEC treats a script that declares a user agent and stays under 10 requests per second on `data.sec.gov` as outside the "unclassified bots" language (the documented APIs are meant for scripts; the policy sentence is on the privacy page).
