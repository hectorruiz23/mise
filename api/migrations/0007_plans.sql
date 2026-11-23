CREATE TABLE plans (
    id           INTEGER PRIMARY KEY,
    project_key  TEXT    NOT NULL REFERENCES projects(key),
    repository   TEXT    NOT NULL,
    branch       TEXT    NOT NULL,
    workflow_id  INTEGER NOT NULL CHECK (workflow_id > 0),
    approved_by  INTEGER NOT NULL REFERENCES users(id),
    approved_at  INTEGER NOT NULL,
    state        TEXT    NOT NULL CHECK (state IN ('active', 'cancelled')),
    cancelled_by INTEGER REFERENCES users(id),
    cancelled_at INTEGER,
    CHECK ((cancelled_by IS NULL) = (cancelled_at IS NULL))
);

CREATE TABLE plan_nodes (
    plan_id  INTEGER NOT NULL REFERENCES plans(id),
    task_key TEXT    NOT NULL UNIQUE REFERENCES tasks(key),
    PRIMARY KEY (plan_id, task_key)
);

CREATE TABLE plan_edges (
    plan_id   INTEGER NOT NULL REFERENCES plans(id),
    from_task TEXT    NOT NULL REFERENCES tasks(key),
    to_task   TEXT    NOT NULL REFERENCES tasks(key),
    kind      TEXT    NOT NULL CHECK (kind IN ('integration', 'deployment')),
    PRIMARY KEY (plan_id, from_task, to_task),
    CHECK (from_task <> to_task)
);

CREATE TABLE plan_proofs (
    plan_id     INTEGER NOT NULL REFERENCES plans(id),
    task_key    TEXT    NOT NULL REFERENCES tasks(key),
    delivery_id INTEGER NOT NULL,
    pr_url      TEXT    NOT NULL,
    head_sha    TEXT    NOT NULL,
    merge_sha   TEXT    NOT NULL,
    run_id      INTEGER NOT NULL,
    attempt     INTEGER NOT NULL,
    verified_at INTEGER NOT NULL,
    PRIMARY KEY (plan_id, task_key)
);

CREATE UNIQUE INDEX plan_proofs_verified_merge ON plan_proofs (merge_sha);
