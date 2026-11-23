ALTER TABLE projects ADD COLUMN required_checks TEXT NOT NULL DEFAULT '[]'
    CHECK (json_valid(required_checks) AND json_type(required_checks) = 'array');

ALTER TABLE deliveries ADD COLUMN head_sha TEXT;

CREATE TABLE submissions (
    id              INTEGER PRIMARY KEY,
    task_key        TEXT    NOT NULL REFERENCES tasks(key),
    pr_url          TEXT    NOT NULL,
    head_sha        TEXT    NOT NULL CHECK (length(head_sha) = 40),
    required_checks TEXT    NOT NULL
                            CHECK (json_valid(required_checks) AND json_type(required_checks) = 'array'),
    verdict         TEXT    NOT NULL CHECK (verdict IN ('open', 'passed', 'failed')),
    checks          TEXT    CHECK (checks IS NULL OR (json_valid(checks) AND json_type(checks) = 'array')),
    declared_at     INTEGER NOT NULL,
    delivered_at    INTEGER,
    checked_at      INTEGER,
    UNIQUE (task_key, head_sha)
);
