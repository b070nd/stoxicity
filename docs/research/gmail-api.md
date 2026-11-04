# Gmail API: what Google's documentation says

Read on 2026-11-04 from Google's own pages (through a web-fetch agent, so quotes
are as that tool returned them). `app/src/channel/gmail.go` and `gmailauth.go`
are written to these facts. Where a page is silent it says NOT CONFIRMED and the
code does not depend on it.

## Sending

- `POST https://gmail.googleapis.com/gmail/v1/users/{userId}/messages/send`,
  `userId` may be `me`. Body: `{"raw": "<message>"}` with
  `Content-Type: application/json`.
- "MIME email messages compliant with RFC 2822 and encoded as base64URL
  strings" (sending guide). The response is a Message (`id`, `threadId`, ...).
- Scopes accepted by this method: `mail.google.com`, `gmail.modify`,
  `gmail.compose`, `gmail.send`. `gmail.send` alone is enough. It is listed as a
  **sensitive** scope ("Send email on your behalf"), not a restricted one.
- NOT CONFIRMED: whether `=` padding is allowed in `raw` (the official Python
  sample pads, the Java sample does not); an API message size limit; API quotas.
  A personal account is told it may be limited after "more than 500 emails sent
  in a day".

## OAuth for a desktop app (loopback)

- Authorization endpoint `https://accounts.google.com/o/oauth2/v2/auth`;
  parameters `client_id`, `redirect_uri` (`http://127.0.0.1:port`),
  `response_type=code`, `scope`, `state`, `code_challenge`,
  `code_challenge_method` (`S256` or `plain`). `access_type=offline` and
  `prompt=consent` are documented on the web-server page.
- Token endpoint `https://oauth2.googleapis.com/token`. Code exchange:
  `client_id`, `client_secret` (marked optional), `code`, `code_verifier`,
  `grant_type=authorization_code`, `redirect_uri`. Refresh: `client_id`,
  `client_secret` (optional), `grant_type=refresh_token`, `refresh_token`.
- The code verifier is 43 to 128 characters; the S256 challenge is the base64url
  (no padding) SHA-256 of it. The page says to always use PKCE.
- "The manual copy/paste option, also referred to as an out of band (OOB)
  redirect method, is no longer supported." Loopback is the recommended flow for
  macOS and Linux desktop apps.
- "Refresh tokens are always returned for installed applications."
- NOT CONFIRMED: whether Google rejects a desktop-client token request without
  `client_secret` in practice; the JSON shape and HTTP status of `invalid_grant`
  (the code treats the `error` field as authoritative and any 2xx as success).

## How long a refresh token lives

- Testing status, external user type, any scope beyond name, email and profile:
  the refresh token "expir[es] in 7 days". Test users are capped at 100.
- Other reasons a token stops working: revoked; "not been used for six months";
  "changed passwords and the refresh token contains Gmail scopes"; the account
  has too many live tokens (100 per account and client; creating one more
  invalidates the oldest without warning); an admin restricts the service.
- An unverified app (sensitive scope, no verification) shows a warning and is
  capped at 100 new users over the project's life; during development you can
  continue with "Advanced > Go to {Project Name} (unsafe)". Whether the owner's
  own account counts toward the cap is NOT CONFIRMED.
- Google states no fixed lifetime for tokens of a project that is *In
  production*.

## Pages read

developers.google.com/workspace/gmail/api/reference/rest/v1/users.messages/send
and .../users.messages, .../guides/sending, .../auth/scopes,
.../troubleshoot-authentication-authorization;
developers.google.com/identity/protocols/oauth2 (Refresh token expiration),
.../native-app, .../web-server; support.google.com/cloud/answer/15549945 and
7454865; support.google.com/mail/answer/22839 and 6584.
