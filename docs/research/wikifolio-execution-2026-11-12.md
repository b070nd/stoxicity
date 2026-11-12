# Can a wikifolio be traded programmatically?

Research date: **2026-11-12**. Every page below was read on that date through an automated page reader (summaries of the page text, not a browser). Primary sources only (wikifolio.com, its terms and licence PDF, the wikifolio Trading API spec, Lang & Schwarz pages). Secondary sources were used only to find primary ones and are labelled "unofficial, unverified". No login was used. No page blocked the reader. The Swagger UI page showed only a title because it needs JavaScript, so the OpenAPI JSON behind it was read instead (section 3). This is not legal or investment advice.

## Short answer

- wikifolio does run an **official Trading API** with documented endpoints (spec at `trading-api.wikifolio.com`). Keys are not self-service as far as any primary source shows.
- The general terms say automated interactions "for example, via API" need **prior written consent** of wikifolio. So the API is allowed only with consent, and the consent route is the written agreement.
- Scraping, bots on the website or the unofficial web interface are forbidden by the same clause.
- Nothing in the primary sources states the API terms (limits, fees, who may get keys). Those must be asked.

## 1. What is a wikifolio, legally and mechanically?

- A wikifolio is "a fictitious virtual portfolio". Trades in it are virtual. [FAQ](https://www.wikifolio.com/en/int/faq), read 2026-11-12.
- The trader is a "licensor" who signs a licence agreement with wikifolio Financial Technologies AG (Vienna). The trader places virtual orders "via the virtual trading application" on wikifolio.com. [Licence agreement, 2022-04-12, clause 2 a](https://wikifoliostorage.blob.core.windows.net/prod-cms-media/629464/2022-04-12-license-agreement-for-the-publication-of-virtual-portfolios.pdf), read 2026-11-12.
- The trader grants wikifolio a worldwide, perpetual right to use the portfolio and orders, including for certificates (clause 3 a).
- Investors buy a **certificate**. Lang & Schwarz AG issues "an open-ended index certificate that tracks your wikifolio 1:1". [How to create](https://www.wikifolio.com/en/int/how-to-create), read 2026-11-12. Lang & Schwarz is "issuer and market maker". [L&S partner page](https://wikifolio.com/en/int/about-wikifolio/partner/lang-schwarz), read 2026-11-12.
- Lang & Schwarz TradeCenter AG & Co. KG provides the trading prices and trades the certificates off-exchange. Selected certificates are also on BX Swiss (same partner page). The FAQ says certificates trade in euros in Stuttgart and in Swiss francs in Bern.
- The issuer hedges: it "must hedge" its risk positions and may trade the securities itself or match client orders. The trader's orders are not real orders in the market. [FAQ](https://www.wikifolio.com/en/int/faq).
- Lang & Schwarz TradeCenter acts as index sponsor and can value a wikifolio when a security has no price for a long time. [Price rule page](https://www.wikifolio.com/en/int/legal/ltsu), read 2026-11-12.
- What the trader controls: the strategy and the virtual orders, within the investment universe. What the trader does not control: the notional reference portfolio. wikifolio or Lang & Schwarz "shall be the sole arbiters" of it and may lock a wikifolio "at any time without giving any reasons" (clause 2 b).
- The trader cannot move real money through the wikifolio. Real money enters only when investors buy the certificate.

## 2. How a trader places orders

- Orders are placed in the trader area on wikifolio.com ("trade section of your wikifolio"). The FAQ does not mention a mobile app. The how-to-create page does not either. App support: not found in a primary source.
- Order types in the web interface: **quote order**, **limit order**, **stop limit order**. [FAQ](https://www.wikifolio.com/en/int/faq).
  - Quote order: the price is requested from Lang & Schwarz and "is valid for 3 seconds". The trader must confirm it.
  - Limit order: executes at the set price or better.
  - Stop limit: becomes active after the stop level is crossed.
- The API has the same family: limit buy/sell, stop-limit buy/sell, and quote buy/sell (section 3).
- Trading hours (FAQ table, all UTC+01:00): for stocks, funds, ETPs, Lang & Schwarz certificates and leverage products, Mon to Fri 07:30 to 23:00, Sat 10:00 to 13:00, Sun 17:00 to 19:00. Other issuers (HSBC, SocGen, Vontobel) have their own hours. The FAQ lists closed days (Christmas, New Year, Easter, 1 May) and a 14:00 stop on 30 December. Liquidity is highest in exchange hours.
- Quote provider: Lang & Schwarz TradeCenter. Licence clause 2 b limits the universe to instruments for which it "continuously forwards bid and ask prices".
- Liquidity limits, position-size limits, minimum holding periods and trade-frequency limits: **not found in a primary source**. The only size-like rule found is a tag rule: the "Actively diversified" tag needs at least ten securities and none above 20%. That is a label, not a trading limit.
- A limit order on a security with no price for a long time is deleted automatically (price rule page).

## 3. Official public API?

**An official Trading API exists, but it is gated.**

- Spec (OpenAPI 3.0, version 1.0, contact wikifolio Financial Technologies AG, office@wikifolio.com): [swagger.json](https://trading-api.wikifolio.com/swagger/v1/swagger.json), UI at [index.html](https://trading-api.wikifolio.com/swagger/index.html). Read 2026-11-12.
- Authentication: `POST /sessions` with two headers, `clientApiKey` and `userApiKey`. It returns a `sessionToken`, sent in a `sessionToken` header on all other calls. `DELETE /sessions` ends the session.
- Endpoints under `/v1`:
  - `POST /limitorders`, `GET/PUT/DELETE /limitorders/{orderId}`
  - `POST /quotes` (quote request), `POST /quoteorders` (needs a prior quote), `GET /quoteorders/{orderId}`
  - `GET /wikifolios` (max 100 per page), `GET /wikifolios/{symbol}`, `GET /wikifolios/{symbol}/underlyings` (max 1000 per page), `GET /wikifolios/{symbol}/orders`
- Limit order types: BuyLimit, BuyStopLimit, SellLimit, SellStopLimit. Quote order types: Buy, Sell. Limit orders take `validUntilDate` (default end of day). Order responses include `executionPrice`. Wikifolio details include cash balance, total value, bid/ask, base currency and positions.
- Order responses are asynchronous (HTTP 202), with status values such as Waiting, Executed, Rejected, Error.
- The spec states **no rate limits, no fees, no terms and no way to obtain the keys**.
- How to get keys: **not found in a primary source**. The only description is unofficial and unverified: a GitHub wrapper README says to email office@wikifolio.com, answer questions about the purpose, and sign an agreement. The same README says wikifolio "does not like" full automation and "can block your account" ([henrydatei/wikifolio-trading-api](https://github.com/henrydatei/wikifolio-trading-api), unofficial, unverified). Treat that as a lead to check, not as fact.
- The FAQ, how-to-create and how-it-works pages do not mention an API. Nearest statement in the FAQ: the "Rule-based" style tag says "Trades can be executed in this wikifolio with the help of software." This shows software-assisted trading is a recognised style. It is not a permission.

## 4. Terms of use on automated access

Source: [General Terms and Conditions (Terms of Use)](https://www.wikifolio.com/en/int/gtc), read 2026-11-12. The page is marked "as of July 3rd 2016, extended March 7th 2022". **The page has no clause numbers**, only unnumbered headings. The relevant text sits in the Terms of Use section.

- "A use by automatic and/or automated reading or another similar method is prohibited."
- Automated interactions "for example, via API" are "only permitted after prior written consent of the Company."
- The Disclaimer section also says "Market manipulation and any attempt for this purpose are prohibited."
- Licence agreement, clause 1 a: the personal section is reserved "exclusively for the licensor themselves". The trader must protect the password and not transfer the access. Sharing a login with a bot service could breach this. (Reading, not a legal opinion.)
- Licence Annex B (code of conduct): follow the Market Abuse Regulation, no inside information, and do not exploit the delay between a virtual order and the publication or reproduction of the strategy for certificates.
- Governing law: the terms of use say Austrian law and the Commercial Court of Vienna. The licence agreement says German law and Frankfurt am Main. They differ.
- Result: scraping, bots on the website and unofficial interfaces are forbidden. Automated use through the API is possible only with prior written consent.

## 5. Instruments and currencies

- German-language wikifolios: stocks, ETPs, funds, investment certificates and leverage products. "Trading is limited to exchange-traded products." [FAQ](https://www.wikifolio.com/en/int/faq).
- English-language wikifolios: stocks, funds and ETPs only. So leverage products are excluded there.
- The trader may narrow the universe at creation. It is fixed once the certificate is issued.
- Size of the universe: the FAQ and how-to-create page give different figures (over 585,000 tradable securities versus over 634,000 products). Full list: [Investment Universe spreadsheet](https://wikifoliostorage.blob.core.windows.net/prod-documents/Investment_Universe.de.xlsx) (not opened).
- Currencies: the API wikifolio object has a `baseCurrency` field. The FAQ does not state a base currency or how non-euro securities are converted. Certificates trade in EUR (Stuttgart) and CHF (Bern). Currency handling inside the portfolio: **not found in a primary source**.

## 6. Fees and execution price

From the [FAQ](https://www.wikifolio.com/en/int/faq) and [how it works](https://wikifolio.com/en/int/how-to-invest/how-it-works), read 2026-11-12:

- Certificate fee: "in most cases" 0.95% per year, calculated daily. Asset manager wikifolios 0.5% to 1.5%. Paradise Model Portfolios 0.1%.
- Performance fee on classic wikifolios: 5% to 30%, only above a new high watermark. The trader's share is up to 50% of it. Licence Annex A: 30% for EUR 10,000 to 50,000 invested, 40% up to 125,000, 50% above.
- No transaction fees inside the wikifolio and no issue fees. The investor's bank or broker may charge fees for buying the certificate.
- Spreads: they depend on the securities held and are tightest in exchange hours. No numbers found.
- Execution price: quote orders use the Lang & Schwarz price valid for 3 seconds. Which price a limit or stop-limit order executes against: **not found in a primary source**. The price rule page says a security without prices can be valued "with a deduction of up to 100%".

## 7. Requirements to open and publish

From the [FAQ](https://www.wikifolio.com/en/int/faq), [how to create](https://www.wikifolio.com/en/int/how-to-create) and the licence agreement:

- Registration is free. Minimum age 18. The trader must give correct identity data and a display name that wikifolio confirms (licence clause 1 a). wikifolio may refuse registration.
- Test status: visible only to the trader. A private trader may run eight published wikifolios plus one in Test.
- To publish: master data, trader profile, security groups, at least one trade, verified email.
- To become investable: identity verification (ID, address, phone), at least 21 days old, at least ten watchlistings totalling EUR 2,500, and an issuance conversation by phone for the first wikifolio.
- Real money: a trader needs at least EUR/CHF 5,000 in the own certificate for the "self-invested" tag, with proof every 90 days. EUR/CHF 10,000 is needed for a success bonus.
- Trader profiles "may not contain investment recommendations or profit promises" (FAQ).
- Regulatory constraints on the trader: Market Abuse Regulation and no inside information (Annex B). A licensing status for the trader (advice or asset management): **not found in a primary source**. No regulator page (BaFin, FMA) was found that describes it.

## 8. Unofficial clients (unofficial, unverified)

Found only through search results. Not opened in depth and not tested. Using any of them may breach the "automated reading" clause in section 4 and can get an account blocked.

- Node.js package `wikifolio` and [RienNeVaPlus/wikifolio](https://github.com/RienNeVaPlus/wikifolio): described as an unofficial API for the website, with a warning to pause between calls.
- [henrydatei/wikifolio-api](https://github.com/henrydatei/wikifolio-api): described as a Python wrapper for the unofficial API.
- [henrydatei/wikifolio-trading-api](https://github.com/henrydatei/wikifolio-trading-api): a Python wrapper for the official Trading API (needs the two keys). This one uses the official interface, but the key and consent rules still apply.

## What this means for automated execution from an external signal engine

**Clearly permitted**

- Generating signals outside wikifolio and entering the orders **by hand** in the wikifolio web interface.
- Using the "Rule-based" style tag for software-assisted trading, as long as the orders are not sent through an unconsented automated route. (The tag text shows the style exists. Whether the manual entry must be human is not stated.)

**Clearly not permitted**

- Browser automation, scraping or any "automated reading" of wikifolio.com.
- Using the unofficial web interfaces listed in section 8 for trading.
- Sharing the trader login with a service (licence clause 1 a).
- Using the official API without written consent from wikifolio.

**Needs a written answer from wikifolio**

- Use of the official Trading API from an external signal engine. Write to office@wikifolio.com (the contact in the API spec). Suggested question:

  > "I run, or plan to run, a wikifolio under my own name. My own software produces trade signals. May I send the resulting orders to my wikifolio through your Trading API (limit and quote orders) without a person confirming each order? If yes, please confirm in writing: (1) that this counts as the 'prior written consent' for automated interactions in your General Terms; (2) how to get the client and user API keys and what agreement applies; (3) rate limits, maximum order frequency and any liquidity or size limits; (4) whether the API is available while the wikifolio is in Test status; (5) fees or conditions for API use; (6) what happens to a wikifolio if the API is used in a way you do not allow."

- A second, smaller question: whether a human click per batch ("review and confirm in my own tool, then send") is treated as automated or as user-triggered.

## Not verified

- Who may get API keys, the agreement terms, rate limits, fees and any ban conditions. No primary page states them. The email-and-agreement route is from an unofficial README only.
- Whether the Trading API works for Test status or only investable wikifolios. The spec lists a `WikifolioStatus` field (Test, Published, Investable and others) but does not say which statuses accept orders.
- Whether the API is the same as the one the website uses, and whether "prior written consent" in the terms is the same as the key agreement.
- Whether a mobile app can place orders. No primary page mentions one.
- Liquidity limits, position limits, minimum holding periods, trade-frequency rules, minimum order size. Not stated in the pages read.
- Execution price rules for limit and stop-limit orders, and actual spread sizes.
- Currency handling and base currency. Only the API field name `baseCurrency` was seen.
- The contents of the Investment Universe spreadsheet. Not opened.
- Lang & Schwarz base prospectus and final terms for the wikifolio certificates. Only search snippets were seen. The current documents on ls-tc.de were not read, so issuer legal detail (termination rights, index rules) is unconfirmed.
- Regulator pages (BaFin, FMA) and the regulatory status of the trader. Not found.
- The licence PDF is dated 2022-04-12 and the terms of use 2016/2022. A newer version may exist. Some licence text still carries an older date in its page header (2018-11-15).
- The reader summarises pages with a small model. Quotes are as reported by it and were not checked against raw HTML, except the licence PDF, which was read from extracted text.
