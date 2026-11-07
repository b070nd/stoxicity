package tui

// The shell draws section navigation, news/watch/exchange filters, source
// meters, and one breadcrumb/title line. Tab cycles sections; F6 gives the
// header the arrows. Ctrl+Tab and the digits switch watch and keep the screen.
// Loading a watch's screen marks its hits as seen (Backend.MarkSeen).
//
// Build makes the body of each route. Every detail screen has the same layout
// rules: a summary on top, the timeline (bars per day, then events), the tag
// cloud, and tables whose rows open other routes.
//
//	nav.Home     the News feed, newest first, with an activity timeline
//	nav.Stocks   stocks seen in relevant news, by mentions, with materiality and tone
//	nav.Stock    summary, tone and materiality by week, timeline, event kinds,
//	             top domains, topics, articles
//	nav.Topics   all topics as a tag cloud and a table with the lift over the
//	             window before
//	nav.Topic    its stocks, domains, timeline, related topics, top articles
//	nav.Domains  relevant against total per domain
//	nav.Domain   one domain's output, precision against your labels, timeline
//	nav.Day      one day's digest
//	nav.Search   results of a Google-style query, with domain and event counts
//	nav.Sources  source health: last success, last error, circuit level
//	nav.Watches  the tenant's watches with unread counts; enter shows one
//
// Creating, editing and muting watches means editing their YAML files in
// tenants/<tenant>/watches (`stoxicity watch check` validates them); the
// interface only reads them.
