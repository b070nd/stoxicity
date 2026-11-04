package channel

// The email channel sends alerts and digests as Markdown rendered to plain
// text, with the route to open as a link.
//
// Configuration comes from the environment, never from config.yaml:
//
//	STOXICITY_SMTP_HOST, STOXICITY_SMTP_PORT   settings, in .env
//	STOXICITY_SMTP_USER, STOXICITY_SMTP_PASSWORD  credentials, in .env.local
//	STOXICITY_MAIL_FROM, STOXICITY_MAIL_TO     addresses, in .env.local
//
// How to authenticate to Gmail (an app password over SMTP, or the Gmail API
// with OAuth) is not decided; see docs/configuration.md. Whatever is chosen,
// the channel must refuse to send when a credential is missing, say which
// variable is empty without printing any value, and drop articles the
// recipient's tenant may not see (sources.Registry.VisibleTo).
