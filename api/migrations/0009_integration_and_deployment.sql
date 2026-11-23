ALTER TABLE projects ADD COLUMN auto_merge INTEGER NOT NULL DEFAULT 1
    CHECK (auto_merge IN (0, 1));

CREATE TABLE environments (
    id          INTEGER PRIMARY KEY,
    project_key TEXT    NOT NULL REFERENCES projects(key),
    name        TEXT    NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 40),
    url         TEXT    NOT NULL CHECK (length(trim(url)) BETWEEN 1 AND 500),
    UNIQUE (project_key, name)
);

CREATE TABLE plan_deployments (
    plan_id     INTEGER NOT NULL REFERENCES plans(id),
    task_key    TEXT    NOT NULL REFERENCES tasks(key),
    environment TEXT    NOT NULL,
    revision    TEXT    NOT NULL
                        CHECK (length(revision) = 40
                               AND revision NOT GLOB '*[^0-9a-f]*'),
    observed_at INTEGER NOT NULL,
    PRIMARY KEY (plan_id, task_key, environment)
);

ALTER TABLE plan_proofs ADD COLUMN origin TEXT NOT NULL DEFAULT 'verification'
    CHECK (origin IN ('verification', 'merge'));

CREATE TABLE merge_attempts (
    task_key     TEXT    NOT NULL REFERENCES tasks(key),
    pr_url       TEXT    NOT NULL,
    outcome      TEXT    NOT NULL CHECK (outcome IN ('merged', 'refused', 'failed')),
    reason       TEXT    NOT NULL CHECK (length(trim(reason)) > 0),
    merge_sha    TEXT,
    attempted_at INTEGER NOT NULL,
    PRIMARY KEY (task_key, pr_url)
);
