-- The investment policy, as immutable versions and an append-only list of
-- activations. A version is identified by the hash of its content; an activation
-- says that a version became the policy in force at a time, with the consent
-- that was given and what it invalidated. The activation's seq is the policy
-- epoch: plans and order confirmations record the epoch they were made under
-- and are stale once a newer one exists. Nothing here is ever updated or
-- deleted, so the policy that was in force at any past time can be read back.
CREATE TABLE policy_version (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    hash       TEXT NOT NULL UNIQUE,
    content    TEXT NOT NULL,
    created_ms INTEGER NOT NULL
);

CREATE TABLE policy_activation (
    seq           INTEGER PRIMARY KEY AUTOINCREMENT,
    version_seq   INTEGER NOT NULL REFERENCES policy_version (seq),
    prev_seq      INTEGER NOT NULL,
    at_ms         INTEGER NOT NULL,
    risk_accepted INTEGER NOT NULL,
    risk_notes    TEXT NOT NULL,
    changes       TEXT NOT NULL,
    effects       TEXT NOT NULL
);
CREATE INDEX policy_activation_at ON policy_activation (at_ms, seq);

CREATE TRIGGER policy_version_no_update BEFORE UPDATE ON policy_version
BEGIN SELECT RAISE(ABORT, 'policy_version is append-only'); END;
CREATE TRIGGER policy_version_no_delete BEFORE DELETE ON policy_version
BEGIN SELECT RAISE(ABORT, 'policy_version is append-only'); END;
CREATE TRIGGER policy_activation_no_update BEFORE UPDATE ON policy_activation
BEGIN SELECT RAISE(ABORT, 'policy_activation is append-only'); END;
CREATE TRIGGER policy_activation_no_delete BEFORE DELETE ON policy_activation
BEGIN SELECT RAISE(ABORT, 'policy_activation is append-only'); END;
