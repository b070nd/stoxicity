-- Shared request budgets, across tenants, workers and CLI restarts. Operational
-- counters only; no provider keys, response bodies or financial facts live here.
CREATE TABLE provider_quota (
    provider TEXT PRIMARY KEY,
    used INTEGER NOT NULL DEFAULT 0 CHECK (used >= 0),
    requests INTEGER NOT NULL DEFAULT 0 CHECK (requests >= 0),
    period_ns INTEGER NOT NULL DEFAULT 0,
    reset_ms INTEGER NOT NULL DEFAULT 0,
    until_ms INTEGER NOT NULL DEFAULT 0,
    status INTEGER NOT NULL DEFAULT 0
);
