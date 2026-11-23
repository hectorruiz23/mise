CREATE TABLE dependency_plans (
    first_task TEXT PRIMARY KEY REFERENCES tasks(key),
    next_task TEXT NOT NULL UNIQUE REFERENCES tasks(key),
    approved_by INTEGER NOT NULL REFERENCES users(id),
    approved_at INTEGER NOT NULL,
    repository TEXT NOT NULL,
    branch TEXT NOT NULL,
    workflow_id INTEGER NOT NULL CHECK (workflow_id > 0),
    previous_delivery_id INTEGER NOT NULL,
    proof TEXT CHECK (proof IS NULL OR json_valid(proof)),
    cancelled_by INTEGER REFERENCES users(id),
    cancelled_at INTEGER,
    CHECK (first_task <> next_task),
    CHECK ((cancelled_by IS NULL) = (cancelled_at IS NULL)),
    CHECK (proof IS NULL OR cancelled_at IS NULL)
);

CREATE UNIQUE INDEX dependency_plans_verified_merge
ON dependency_plans (repository, json_extract(proof, '$.merge_sha'))
WHERE proof IS NOT NULL;
