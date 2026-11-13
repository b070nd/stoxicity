# Where can a one-person tool get point-in-time analyst revisions for US stocks?

Research date: **2026-11-13**. Every page below was read on that date. Pages were read as rendered text in a browser pane or, for static files (Finnhub's `swagger.json`, the Alpha Vantage terms PDF, the LSEG brochure PDF), by downloading the file and extracting its text. Primary sources only: each provider's own pricing page, documentation and terms. Review blogs and aggregators were not used as evidence. A web-search summary was used a few times to find a primary URL and is never cited as proof. No login was used and no account was created. No paid or keyed call was made; the only API call was Alpha Vantage's documented `apikey=demo` example. The FMP site returned HTTP 403 to a plain page fetcher but loaded normally in the browser pane (no challenge was solved or bypassed). Cookie banners were left alone. This is not legal or investment advice, and a licence reading below is a reading, not a legal opinion.

## Short answer

- **Best fit for the importer: Benzinga Analyst Ratings, sold through Massive** (Massive is the repo's price provider, formerly Polygon.io). It is a separate add-on at **$99/month, "Individual use only"**, with records back to **2011-12-08**. Each record has firm, analyst, previous and new rating, previous and new price target, a date, a time and a `last_updated` timestamp. The individual terms say outright that you may "access, download, store and use" the information for your own use. Source: [Benzinga partner page](https://massive.com/partners/benzinga), [endpoint docs](https://massive.com/docs/rest/partners/benzinga/analyst-ratings), [market data terms](https://massive.com/legal/market-data-terms-of-service). The repo's price plans (Stocks Basic to Advanced) do **not** include analyst data.
- **No provider read here can justify the `known` basis for a backfill.** `known` needs `held_at`, the time *your own system* held the row. A vendor timestamp (Benzinga `last_updated`, FactSet `inputDateTime`, Zacks `obs_date`) says when the vendor had it, not when you did. Everything bought as history is `reconstructed`. `known` can only come from polling the feed yourself from day one and writing the fetch time into `held_at`.
- **Estimate revisions (EPS, revenue) per firm are not available at one-person prices.** The cheap APIs (Finnhub, FMP, Alpha Vantage, EODHD) give a consensus snapshot with 7/30/60/90-day look-backs or a single current figure, not a dated history of revisions. The one dated consensus history found is Zacks on Nasdaq Data Link (EPS only, aggregate, price hidden behind login). Per-broker point-in-time estimates are LSEG I/B/E/S and FactSet, both quote-only.
- **FMP's personal licence is a poor fit for a local database**: the personal licence says the customer "may not copy or download any content from the Services" without written approval (section 2.2.1 of its terms).
- **SEC and other free official sources: none** for estimates, targets or ratings.
- Timestamp warning for Benzinga: the docs say `time` is UTC, but both sample responses on Massive's own pages fit New York time instead (section 1). Check this against a few known events before trusting any intraday ordering.

## 1. Massive (formerly Polygon.io) with Benzinga data

**Does Massive have analyst data? Yes, as a partner add-on, not in the price plans.**

- Pricing page lists "Partner Data": Benzinga, "Real-time financial news and analyst ratings", "$99/month per dataset". [massive.com/pricing](https://massive.com/pricing)
- On the Benzinga page, six datasets are priced separately at $99/month each, each marked "Individual use only": Analyst Ratings, Analyst Insights, Earnings, Corporate Guidance, News, Bulls Bears Say. [Benzinga partner page](https://massive.com/partners/benzinga)
- The docs page for the ratings endpoint says "Included in select Benzinga expansion plans" under Plan Access. Whether a Stocks base plan is needed to buy it: **not stated** on the pages read (the "no base subscription required" line on the pricing page sits under "More Data", not "Partner Data"). [docs](https://massive.com/docs/rest/partners/benzinga/analyst-ratings)
- Price plans for stocks, for reference: Basic $0 (5 calls/minute, 2 years), Starter $29, Developer $79, Advanced $199 (monthly billing), none listing analyst data. [pricing, stocks tab](https://massive.com/pricing?product=stocks)
- Which plan the repo's key is on: **not recorded in the repo**. `.env` was deliberately not opened.

**Endpoint and fields.** `GET /benzinga/v1/ratings` ([docs](https://massive.com/docs/rest/partners/benzinga/analyst-ratings)):

- Filters: `date`, `ticker`, `last_updated`, `rating_action`, `price_target_action`, `benzinga_id`, analyst and firm ids; `limit` default 100, maximum 50000; sort default `last_updated` descending.
- Result fields: `firm`, `analyst`, `benzinga_analyst_id`, `benzinga_firm_id`, `benzinga_id` (the record id), `ticker`, `company_name`, `rating`, `previous_rating`, `rating_action`, `price_target`, `previous_price_target`, `price_target_action`, `adjusted_price_target`, `previous_adjusted_price_target`, `price_percent_change`, `currency`, `date` (YYYY-MM-DD), `time` (HH:MM:SS), `last_updated` (ISO 8601), `importance`, `notes`, two Benzinga URLs.
- `rating_action` values: downgrades, maintains, reinstates, reiterates, upgrades, assumes, initiates_coverage_on, terminates_coverage_on, removes, suspends, firm_dissolved. `price_target_action` values: raises, lowers, maintains, announces, sets.
- No EPS or revenue estimate revision fields. The separate Earnings dataset sample shows actuals and previous reported figures, not analyst estimates; the pricing blurb says "estimates and surprises", so whether it carries consensus history is **could not verify** (section 11).

**History and recency.** "Updated in real time"; "All history", "Records date back to December 8, 2011". [docs](https://massive.com/docs/rest/partners/benzinga/analyst-ratings) The partner page says "history going back to 2012" in one place and "13+ Years" in another; the docs page date is the precise one.

**What it states about when a revision became available.**

- `date`: "the calendar date when the rating was issued". `time`: "the time (formatted as 24-hour HH:MM:SS UTC) when the rating was issued". Precision: seconds. [docs](https://massive.com/docs/rest/partners/benzinga/analyst-ratings)
- `last_updated`: "when the rating was last updated in the system". It is a mutable field, not a first-seen time. No page read says how often a record is amended or whether amendments overwrite the earlier values.
- **Inconsistency in the vendor's own samples.** [Partner page sample](https://massive.com/partners/benzinga): `"time": "16:31:13"` with `"last_updated": "2025-04-11T20:31:38Z"`. [Docs sample](https://massive.com/docs/rest/partners/benzinga/analyst-ratings): `"time": "09:42:08"` with `"last_updated": "2025-05-22T13:42:30Z"`. In both, `time` is four hours behind `last_updated` by a few seconds, which fits US Eastern daylight time, not UTC. This is an inference from two samples, not a statement; **verify with live data**.
- Point-in-time guarantee: **none stated**. Survivorship-free and not-restated: **none stated**. The docs list `firm_dissolved` and `terminates_coverage_on` as actions, so dissolved firms stay as records, but nothing says delisted tickers are kept.
- `adjusted_price_target` is described as adjusted for splits and dividends, so it changes after the fact. Use `price_target` and `previous_price_target` (as issued) for the importer.

**Price, rate limit, licence.**

- $99/month (annual billing saves 20% per the page). Rate limit for partner datasets: **not stated**. For reference, Massive counts limits per asset class: unsubscribed classes get 5 requests/minute, paid ones none. [Massive KB](https://massive.com/knowledge-base/article/what-is-the-request-limit-for-massives-restful-apis)
- Terms (Massive for Individuals, last updated 2026-11-03, and Market Data Terms of the same date): licence "to access, download, store and use the Information included in your plan ... solely for your own personal, non-business and non-commercial use". Personal use "includes your own research, analysis and investment activities, using any software, tools or methods you choose". Not allowed: sharing with anyone else, building a service for other users, use for an employer, AI/ML model training. Own analyses "that do not contain Information and cannot reasonably be used to recreate it" may be kept. When the licence ends: stop using and delete the information, including stored copies and caches, "promptly, and in any event within thirty (30) days"; an unused automatic backup may linger up to 90 days. A downgrade shrinks the licence to what the new plan includes. [individuals terms](https://massive.com/legal/individuals-terms-of-service), [market data terms, sections 1, 5, 8](https://massive.com/legal/market-data-terms-of-service)
- Practical effect: a local database is explicitly allowed while you subscribe. Cancelling means the stored rows must go (the events built from them are derived data that "contain" the information, so they must go too if they can recreate it). That matters for a research tool that wants to keep event studies reproducible.

## 2. Finnhub

Sources: [pricing](https://finnhub.io/pricing), [estimates pricing](https://finnhub.io/pricing-stock-estimates), Finnhub's own [swagger.json](https://finnhub.io/static/swagger.json) (the docs pages are script-rendered, so the spec behind them was read), [terms](https://finnhub.io/terms-of-service).

- **Tiers.** Free $0, 60 calls/minute, US only. **Estimate-1 $75/month per market** (billed quarterly per the page), 300 calls/minute: Recommendation Trends, Price Target, EPS surprises 10 years, EPS and revenue estimates 10 years historical, **Upgrade/Downgrade "10 years and real-time updates"**. Estimate-2 $200/month per market (annual billing), same limit, 20+ years of upgrade/downgrade and estimates. All-In-One $3,500/month (900 market-data and 300 fundamental calls/minute), upgrade/downgrade 20 years. All plans are "Personal Use. Terms apply".
- **Free tier has no analyst revisions.** In the spec, `/stock/upgrade-downgrade`, `/stock/price-target`, `/stock/historical-price-target` and `/stock/eps-estimate` all carry a premium marker.
- **Upgrade/downgrade fields** (`UpgradeDowngrade` in the spec): `symbol`, `gradeTime` ("Upgrade/downgrade time in UNIX timestamp"), `company` ("Company/analyst who did the upgrade/downgrade"), `fromGrade`, `toGrade`, `action` (up, down, main, init, reit). Query by symbol and `from`/`to` dates; a blank symbol returns the latest changes for all.
- **Timestamp precision in practice.** The three `gradeTime` values in the spec's own sample (1567728000, 1566259200, 1564704000) are exact multiples of 86,400, that is 00:00:00 UTC. So the documented example is day-level data in a time-shaped field. I checked the arithmetic; I did not see a live response.
- **Price targets: consensus only.** `PriceTarget` has high, low, mean, median, analyst count and `lastUpdated`. The sample shows `"lastUpdated": "2023-04-06 00:00:00"`. `/stock/historical-price-target` gives a history of that consensus. There is no firm, analyst, previous or new target per revision.
- **Estimates: consensus per fiscal period**, with `epsAvg`, high, low, analyst count, `period`, `year`, `quarter`. No "as of" parameter and no revision dates in the spec. A history of consensus is not a history of revisions, and nothing states it was captured as published.
- **Point-in-time, survivorship, restatement:** not stated for any estimates endpoint. "Survivorship-bias free" is a row on the pricing page under US *market data* only.
- **Limits and terms.** The terms add "a 30 API calls/second limit on top of all plan's limit". "All data must be deleted should your subscription to that data ends." Redistribution of "data or derived results" needs written approval. Plans are "strictly for personal use"; a personal plan "can't be used by any business even internally", and you are not eligible if you will deduct the cost as a business expense. The terms say nothing about local databases beyond the delete-on-cancel rule. (Read directly from the downloaded page text.)

## 3. Financial Modeling Prep (FMP)

Sources: [pricing](https://site.financialmodelingprep.com/pricing-plans), [grades](https://site.financialmodelingprep.com/developer/docs/stable/grades), [financial estimates](https://site.financialmodelingprep.com/developer/docs/stable/financial-estimates), [TipRanks search](https://site.financialmodelingprep.com/developer/docs/stable/tipranks-search), [TipRanks point-in-time by analyst](https://site.financialmodelingprep.com/developer/docs/stable/tipranks-pit-by-analyst), [TipRanks dataset page](https://site.financialmodelingprep.com/datasets/analyst-ratings-tipranks), [terms](https://site.financialmodelingprep.com/terms-of-service) (last updated 2023-08-01).

- **Tiers** (pricing page, default view; it has Personal Use and Commercial Use tabs that I did not switch): Basic $0, 250 calls/day, 5 years; Starter $29/month ($19 billed annually), 300/minute, 5 years; Premium $69 ($49), 750/minute, 30+ years; Ultimate $139 ($99), 3,000/minute. The feature table lists "Analyst" as **Limited Coverage on Basic, Starter and Premium and Full Coverage on Ultimate only**. Bandwidth caps over a trailing 30 days: 500 MB, 20 GB, 50 GB, 150 GB.
- **Classic grades endpoint.** The docs describe grading company, previous grade, new grade, action and "date of evaluation" for a symbol. The page shows no response sample, so field names and date precision are **not verified**.
- **Estimates endpoint.** Parameters `symbol`, `period` (annual or quarter), `page`, `limit` (maximum 1000), "Currency is as Reported in Financials". The sample is one row per fiscal period (`date` of 2030-09-27 for AAPL) with revenue, EBITDA, EBIT, net income, SG&A and EPS average, high and low plus analyst counts. It is a current forward consensus, with no as-of date and no revision history.
- **TipRanks-powered feed (the only per-analyst, history-bearing FMP data).** `GET /stable/tipranks-search` returns one analyst rating per row, newest first, filterable by symbol, `expertUID`, `from`, `to`. Sample fields: `symbol`, `date` (full ISO timestamp with milliseconds, e.g. `2026-07-30T16:40:58.403Z`), `recommendationDate` (day), `expertUID`, `analystName`, `firmName`, `recommendation`, `analystAction` (initiation, upgrade, downgrade, maintained), `articleTitle`, `articleSite`, `priceTarget`, `priceTargetCurrency` (converted by default, native with `nonadjusted=true`), `url`. There is **no previous rating or previous target**; only the current target. The page states: "Ratings history only goes back 3 years. For analyst ratings older than 3 years, contact support — historical backfill requires an Enterprise plan." Whether `date` is the publication time or TipRanks' ingestion time is **not stated**.
- **"Point in time" on FMP means an as-of query, not a timing guarantee.** The point-in-time endpoints take a `date` and return each analyst's latest active call as of it ("A date parameter rewinds the snapshot"). The dataset page markets this for "backtesting and compliance". Nothing states that the rows were captured when published.
- Which FMP plan includes the TipRanks endpoints: **not found** on the pages read. Rate limit: the plan limits above.
- **Terms that matter for a local database** (section 2.2.1, personal licence): the licence is for "personal, non-business and non-commercial purposes"; the customer "may not copy or download any content from the Services except with the prior written approval of FMP". Section 6.3 requires deleting all data "including data cached" on termination. Section 2.2.2 bans showing data in software "designed for utilization by multiple individuals". The commercial-use list includes "analyzing data ... for commercial purposes ... including market research, business intelligence, or data-driven decision-making"; personal trading research is not obviously inside it, but the copy/download sentence is a direct blocker for storing rows. Written approval would be needed first.

## 4. Alpha Vantage

Sources: [documentation](https://www.alphavantage.co/documentation/), [premium page](https://www.alphavantage.co/premium/), [terms PDF](https://www.alphavantage.co/terms_of_service/), the documented demo call `function=EARNINGS_ESTIMATES&symbol=IBM&apikey=demo`.

- **Tiers.** Free: 25 requests/day. Premium monthly: $49.99 (75/minute), $99.99 (150), $149.99 (300), $199.99 (600), $249.99 (1,200); annual prices are $499 to $2,499.
- **Analyst data: one endpoint**, `EARNINGS_ESTIMATES`: "annual and quarterly EPS and revenue estimates ... along with analyst count and revision history". The demo response (42 estimates for IBM) is a snapshot per fiscal period: average, high, low, analyst count, and `eps_estimate_average_7_days_ago`, `_30_`, `_60_`, `_90_days_ago`, revision up/down counts for 7 and 30 days, revenue average, high, low and count. So "revision history" means four look-back points of the consensus, not dated revisions, and only covers the last 90 days. Whether the endpoint needs a premium key: **not verified**.
- No price targets, no rating changes, no firm or analyst names anywhere in the documentation read (I searched the page for analyst, price target, upgrade, revision, point-in-time).
- Point-in-time, survivorship, restatement: not stated for estimates. (A separate `LISTING_STATUS` endpoint offers delisted tickers "as of the latest trading day or at a specific time in history"; it concerns listings, not estimates.)
- **Terms.** "personal, non-commercial use", with a commercial-use definition that includes employment with a financial firm and any use that lets others access information. No sentence about storing or caching was found in the 1,400-word PDF. Silence is not permission.

## 5. Tiingo

Sources: [pricing](https://www.tiingo.com/about/pricing), [documentation index](https://www.tiingo.com/documentation/general/overview).

- Starter $0 (500 unique symbols/month, 50 requests/hour, 1,000/day, 5 years of fundamentals history) and Power $30/month ($300/year; 10,000/hour). Business $50/month. Licence "Internal Use Only". Fundamentals via API is an add-on through a third party.
- The documentation index lists End-of-Day, News, Crypto, Forex, Equity real-time, IEX, BOATS, Fundamentals, ETF fees, dividends, splits, websockets, utilities. **No analyst estimates, price targets or rating changes section exists.** None on the pricing page either.

## 6. EOD Historical Data (EODHD)

Sources: [pricing](https://eodhd.com/pricing), [calendar API](https://eodhd.com/financial-apis/calendar-upcoming-earnings-ipos-and-splits), [fundamentals API](https://eodhd.com/financial-apis/stock-etfs-fundamental-data-feeds), [terms](https://eodhd.com/financial-apis/terms-conditions).

- **Tiers (EUR).** Free (20 calls/day, 1 year), EOD Historical 19.99, EOD + Intraday 29.99, **Fundamentals Data Feed 59.99**, All-In-One 99.99. Paid plans: 100,000 calls/day, 1,000 requests/minute. The corporate calendar (which holds the estimate-trend endpoint) is "part of the Fundamentals Data Feed and All-In-One plans", also sold alone with the news feed.
- **Estimate data: `/api/calendar/trends`.** Consensus EPS and revenue per fiscal period (average, low, high, analyst count) plus `epsTrendCurrent`, `epsTrend7daysAgo`, `30`, `60`, `90`, and revision counts up and down over 7 and 30 days. The docs say the file "keeps the history" of periods, "about 96 records per symbol for a large US name, anchored on fiscal periods from 2017 to 2027" and "the estimate fields in it move as analysts publish". That is a living snapshot per period with four look-backs, not dated revisions.
- No per-firm revisions, no price-target history, no upgrades or downgrades found in the calendar or fundamentals docs read. The fundamentals sample shows a single `WallStreetTargetPrice` in `Highlights`. A separate "AnalystRatings" block was not found on the page text read.
- Point-in-time, survivorship, restatement: not stated.
- **Terms.** Non-professional users "are permitted to store, manipulate, and analyze the data for private, non-commercial purposes", but not to resell, redistribute or display it. This is the clearest local-storage permission among the cheap APIs besides Massive.

## 7. Nasdaq Data Link, Zacks Earnings Estimates History (ZEEH)

Sources: [ZEEH product page and documentation](https://data.nasdaq.com/databases/ZEEH), [data licence terms](https://data.nasdaq.com/termsandconditions) (the `/terms` link redirects there).

- **Product.** "North American Consensus Earnings Estimate History": "Consensus earnings estimate revision history for over 23,000 publicly traded companies going as far back as 1979." Availability "Premium"; **price: hidden behind login**. Covers US and Canadian listed *and delisted* companies. A sibling product, ZEE, is the current-estimates feed.
- **Table `ZACKS/EEH`.** Keys `m_ticker`, `per_end_date`, `per_type` (Q or A), `obs_date`. `obs_date` is "the date on which contributed estimates were changed and the consensus was revised". Values: `eps_mean_est`, median, count, high, low, standard deviation, `eps_cnt_est_rev_up`, `eps_cnt_est_rev_down`; plus `currency_code` and fiscal and calendar year and quarter. A master table `ZACKS/MT` carries `comp_cik`.
- **What it is not.** EPS only (no revenue), aggregate consensus only (no firm, no analyst, no per-firm target or rating), and EPS is Zacks' own "BNRI" definition (diluted EPS before non-recurring items), not GAAP and not each broker's own.
- **What it states about timing.** "Reporting Lag 1 day". The EEH table "is updated at 10:00 AM UTC every day with the previous day's data". Brokers' revisions arrive "through electronic data feeds which are programmatically parsed and instantly fed into the consensus database"; small boutiques emailing revisions are entered "within 24 hours of receipt"; "Corrected data is available within 48 hours of receipt". Precision is one day. Nothing says whether corrections rewrite history already delivered, so "not restated" is **not guaranteed**.
- **Terms.** Licence "to receive, use, process and store" the data, "for internal business purposes only unless otherwise detailed in the applicable Service Order"; derived data allowed for internal use if it cannot recreate or substitute the source; no redistribution, "including ... incorporation in a database" for others; no use in third-party SaaS or cloud platforms not under your exclusive control. The terms are framed around a signed Service Order. Whether an individual can buy ZEEH and on what terms: **could not verify**.

## 8. SEC and other free sources

- **SEC: none.** The data.sec.gov APIs are submissions (filing history), XBRL companyconcept, companyfacts and frames, plus two nightly bulk zips. They hold filing metadata and XBRL financial-statement facts from forms such as 10-K, 10-Q and 8-K. No analyst estimates, targets or ratings. [SEC API page](https://www.sec.gov/search-filings/edgar-application-programming-interfaces)
- Other free official sources: **none found**. Free consumer sites that show ratings and targets were not evaluated; scraping them is a terms question (compare the wikifolio note) and the history is the current page, not a dated record.

## 9. Reference only: LSEG I/B/E/S and FactSet

Public pages only. Prices are not published for either. Both are institutional contracts.

**LSEG I/B/E/S** ([broker estimates](https://www.lseg.com/en/data-catalogue/company-data/ibes-estimates/broker-estimates), [quant brochure PDF](https://www.lseg.com/content/dam/data-analytics/en_us/documents/brochures/data-for-quant-research.pdf))

- Broker-level data: estimate values, sources, announcement dates, fiscal periods, currencies, recommendation codes and text. 22,000+ companies, 950+ firms, 18,000+ analysts, "United States from 1976; Rest of the World from 1987".
- Packages (brochure): **"I/B/E/S Point-in-Time: daily snapshots of historical data using an array of dates and time stamps"**, FTP, weekly update, history as far back as 1976 (US) and 1987 (non-US). "I/B/E/S Historical": full series monthly, back to 1976. "I/B/E/S Real-Time": TIBCO messages, full history from the 1980s; SmartEstimates history begins in 2012.
- Whether price targets are a measure in the detail data, how the timestamps are defined and the field list: **not on the public pages read**.

**FactSet** ([Estimates API docs](https://developer.factset.com/api-catalog/factset-estimates-api), [Point-in-Time consensus](https://www.factset.com/marketplace/catalog/product/factset-estimates-pointintime))

- Broker detail "collected from over 800 sell-side analysts", "20+ years of broker history across more than 59,000 global companies", updated intraday. "Entitlements must be established [with] the appropriate brokerage firm". Endpoints `/detail`, `/fixed-detail`, `/rolling-detail`, `/detail-ratings`, `/consensus-ratings`. A "Target Price Object" appears in the ratings response model.
- Timing fields: `inputDateTime` "gives date and time when the data is available at the source" (added to the detail and rating endpoints); `lastModifiedDate`; `changeType` (increased, decreased or unchanged against the previous value, detail-ratings only); `updatesOnly=true` returns "the first reported estimates during this period and any subsequent changes".
- Point-in-time consensus product: "consensus data each day at local midnight", data entered afterwards "is excluded", and the database "is not adjusted for dilutions, quality assurance corrections, changes to the default currency, or broker estimates that were not available as of the consensus date". That is the strongest not-restated statement in this note. It is for consensus, and the broker-detail endpoints carry no such statement on the page read.
- Rate limit: 4 million datapoints per minute, HTTP 429 above it.

## 10. Mapping onto the importer's columns

Rules from `app/src/eventfact/analystimport.go` and `app/src/cmd/stoxicity/cmd_eventdb_analyst.go`:

- Required: `firm`, `symbol`, `kind`, `published_at`, `source_ref`. `kind` is one of upgrade, downgrade, target_raised, target_cut, estimate_raised, estimate_cut. A rating row needs `new_rating`; a target row needs `new_target` above zero and a three-letter `currency`; an estimate row needs `new_estimate`, `currency`, `period` and `metric` (eps or revenue). The kind must agree with the figures or ratings ("kind contradicts the stated ratings or figures").
- `published_at` is RFC 3339 with an offset (precision time) or a day (precision day, read as the start of that day in UTC). `available_at`, when absent, equals `published_at`. `held_at` within 72 hours after `published_at` gives `known`, otherwise or when absent `reconstructed`. `symbol` must be in the reference tickers.
- Identity key: symbol, firm, kind, day of `published_at`, period and (for estimates) metric. Two rows with the same key collapse and the later one wins. The analyst's name is not part of the key and there is no analyst column.

| Importer column | Benzinga via Massive | Finnhub upgrade/downgrade | FMP TipRanks search | Zacks ZEEH (derived) |
|---|---|---|---|---|
| firm | `firm` | `company` | `firmName` | none (a label such as "Zacks consensus" would have to be invented) |
| symbol | `ticker` | `symbol` | `symbol` | `ticker` |
| cik | none (the importer fills it from the reference tickers) | none | none | `comp_cik` in master table |
| kind | derive: `rating_action` upgrades or downgrades; `price_target_action` raises or lowers | derive: `action` up or down | derive: `analystAction`; target change not stated, so only upgrade or downgrade | derive: sign of change between two `obs_date` rows, giving estimate_raised or estimate_cut |
| published_at | `date` + `time` (zone must be verified) | `gradeTime` (day-level in the sample) | `date` (ISO with ms) or `recommendationDate` | `obs_date` (day) |
| available_at | `last_updated` is a later-or-equal bound, so it is the safe value (reasoning, not a vendor statement) | none | none | `obs_date` + 1 day is supported by the stated 1-day lag |
| held_at | none (only your own fetch log can fill it) | none | none | none |
| previous_rating / new_rating | `previous_rating` / `rating` | `fromGrade` / `toGrade` | new only (`recommendation`) | n/a |
| previous_target / new_target | `previous_price_target` / `price_target` | not available (consensus only) | new only (`priceTarget`) | n/a |
| previous_estimate / new_estimate | not available | not available | not available | prior row / `eps_mean_est` (consensus, not a firm's figure) |
| metric / period | n/a for ratings and targets | n/a | n/a | eps (Zacks BNRI) / `per_end_date` |
| currency | `currency` | none (assumed USD, unstated) | `priceTargetCurrency` | `currency_code` |
| source_ref | `benzinga_id` | none (build from symbol, time, company) | `url` | none (build from ticker and `obs_date`) |

Gaps the importer cannot close:

- **Benzinga rows that are not changes** (maintains, reiterates, initiates, assumes, terminates) have no matching kind and must be dropped before import. A row with both a rating change and a target change is two importer rows (kinds differ, so ids differ; `source_ref` is not part of the key and may repeat).
- **Analyst name** cannot be stored. Two analysts at one firm revising the same name on one day, or one firm revising twice in a day, collapse into one event under the key. Put the analyst into `firm` (for example "Firm / Analyst") if that loss matters; that is a modelling choice for the owner.
- **Finnhub** cannot fill targets, estimates or `source_ref`, and its `init` and `reit` actions have no kind. **FMP TipRanks** cannot fill any `previous_*`. **Alpha Vantage and EODHD** estimate data are consensus snapshots with look-backs; they have no firm, no dates for the revisions and nothing that maps to `published_at`.
- **Revenue estimate revisions** are not available from any provider read except the snapshot-style FMP, Alpha Vantage and EODHD fields, which cannot be dated.

### `known` versus `reconstructed`

- `known` requires `held_at`, defined in the importer as the time "your own system held the revision". No vendor field is that. Benzinga `last_updated`, FactSet `inputDateTime` and Zacks `obs_date` all describe the vendor's system. Backfilled history, from any provider, is therefore `reconstructed`.
- The route to `known`: poll the chosen feed on a schedule from now on (Benzinga allows `sort=last_updated.desc` and a `last_updated` filter), record the time each row was first seen, and write it to `held_at`. A row seen within 72 hours of `published_at` then imports as `known`. The 72-hour rule is generous for daily polling and trivially met by hourly polling.
- Which published times a vendor could justify as `available_at` for the reconstructed rows: Benzinga `last_updated` (upper bound), Zacks `obs_date` + 1 day (stated lag), FactSet `inputDateTime` (stated as availability at the source, but only through a contract). Finnhub's day-level `gradeTime` and the TipRanks `date` of unknown meaning cannot justify more than day precision.
- None of the providers states that history is frozen. Treat every backfill as possibly restated until a second pull months later is compared with the first (a diff job on the stored rows is the only way to learn it).

## 11. Could not verify

- Whether Massive's Benzinga add-on can be bought without a Stocks base plan, its rate limit, and which plan the repo's key is on (the repo does not record it).
- The time zone of Benzinga `time` (documented UTC, samples suggest US Eastern), how often records are amended, whether amendments overwrite, and whether delisted tickers are kept.
- Whether the Benzinga Earnings dataset has consensus estimate history, and what its docs say about "estimates".
- FMP: which plan unlocks the TipRanks endpoints; the response fields and date precision of the classic grades endpoint (the page shows no sample); whether `date` in the TipRanks feed is publication or ingestion time; whether the Personal Use and Commercial Use tabs on the pricing page show different prices (only the default view was read). The FMP terms page is dated 2023-08-01 while the pricing page is current.
- Finnhub: live `gradeTime` values (only the spec's sample was seen); any point-in-time statement for estimates; the Terms say nothing about local databases.
- Alpha Vantage: whether `EARNINGS_ESTIMATES` needs a premium key; the terms are silent on storage.
- EODHD: any analyst-ratings block in fundamentals (not found on the text read); point-in-time behaviour of the calendar trends.
- Zacks on Nasdaq Data Link: the price, whether an individual can subscribe, whether a Zacks price-target or rating product exists on the platform, and whether corrections rewrite history. The cookie banner was not accepted, so nothing from a logged-in view was read.
- LSEG I/B/E/S: price, field list, timestamp definitions, whether price targets are in the detail data. FactSet: price, field-level definitions of the response objects, and any restatement statement for broker detail.
- Tiingo: the terms of service were not read (the product has no analyst data, so this does not affect the answer).
- The pages were read as rendered page text; sample responses are the vendors' examples, not live calls, except the Alpha Vantage demo call.
