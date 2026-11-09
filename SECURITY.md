# Security policy

## Reporting a vulnerability

Please report security problems privately, not in public issues or pull requests.

- Use GitHub's private reporting: the repository's **Security** tab, then **Report a vulnerability** (https://github.com/b070nd/stoxicity/security/advisories/new).

Include what you found, how to reproduce it, the version (`stoxicity version`) and what you think the impact is. Do not include real API keys, tokens, broker or account numbers, statements or portfolio data. A short description and fake values are enough.

We aim to confirm receipt within a few working days, tell you whether we consider it a vulnerability, and agree a disclosure date with you. Our default is to publish a fix and an advisory within 90 days of the report, sooner if the fix is simple. We credit reporters who want credit.

This is a volunteer project. These are goals, not guarantees.

## Supported versions

There is no tagged release yet. Only the latest commit on `main` receives security fixes. Once releases exist, only the latest release will.

## Scope

In scope: the `stoxicity` and `stoxicity-tui` programs, their configuration and environment handling (`.env`, `.env.local`), the local databases and backups they write, the git hooks and CI check that keep real data out of the repository, and the order tiers that decide whether an order may reach a broker (see [docs/tiers.md](docs/tiers.md) and [docs/orders.md](docs/orders.md)).

Out of scope: weaknesses in third-party data providers, brokers and feeds, a compromised host or user account running the tool, and anything that needs the attacker to already control the configuration files or the data folder.

## Good-faith research

If you make a good-faith effort to follow this policy, avoid harming other users, and give us reasonable time to fix an issue before disclosure, we will not pursue or support action against you for your research.

## For operators

- Keep credentials in `.env.local` (mode 600), never in `.env` or in the repository.
- Keep the data and backup folders outside the source folder (see [docs/data-location.md](docs/data-location.md)).
- Turn on the git hooks with `make hooks` so a commit or push that contains databases, statements, prices or credentials is refused.
