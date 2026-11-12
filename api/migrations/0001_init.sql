-- Mise v2 schema, applied forward-only.
-- Timestamps are Unix seconds (INTEGER); booleans are INTEGER 0/1.
-- No triggers and no ON DELETE CASCADE: business rules live in Go.

CREATE TABLE users (
    id          INTEGER PRIMARY KEY,
    email       TEXT    NOT NULL UNIQUE
                        CHECK (length(trim(email)) BETWEEN 3 AND 320),
    name        TEXT    NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 200),
    kind        TEXT    NOT NULL CHECK (kind IN ('human', 'agent')),
    token_hash  TEXT    NOT NULL UNIQUE
                        CHECK (length(token_hash) = 64
                               AND token_hash NOT GLOB '*[^0-9a-f]*'),
    active      INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    created_at  INTEGER NOT NULL
);

CREATE TABLE sessions (
    id          TEXT    PRIMARY KEY
                        CHECK (length(id) = 64
                               AND id NOT GLOB '*[^0-9a-f]*'),
    user_id     INTEGER NOT NULL REFERENCES users(id),
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    revoked_at  INTEGER,
    CHECK (expires_at > created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE TABLE signup_requests (
    id            INTEGER PRIMARY KEY,
    email         TEXT    NOT NULL
                          CHECK (length(trim(email)) BETWEEN 3 AND 320),
    secret_hash   TEXT    NOT NULL
                          CHECK (length(secret_hash) = 64
                                 AND secret_hash NOT GLOB '*[^0-9a-f]*'),
    requested_at  INTEGER NOT NULL,
    approved_at   INTEGER,
    consumed_at   INTEGER,
    user_id       INTEGER REFERENCES users(id),
    CHECK (consumed_at IS NULL
           OR (approved_at IS NOT NULL AND user_id IS NOT NULL)),
    CHECK (approved_at IS NULL OR approved_at >= requested_at),
    CHECK (consumed_at IS NULL OR consumed_at >= approved_at)
);

CREATE TABLE projects (
    key              TEXT    PRIMARY KEY
                             CHECK (length(trim(key)) BETWEEN 1 AND 16),
    name             TEXT    NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
    next_task_number INTEGER NOT NULL DEFAULT 1 CHECK (next_task_number > 0),
    created_at       INTEGER NOT NULL
);

CREATE TABLE idempotency_records (
    key            TEXT    PRIMARY KEY CHECK (length(key) BETWEEN 1 AND 128),
    actor_id       INTEGER NOT NULL REFERENCES users(id),
    request_digest TEXT    NOT NULL CHECK (length(trim(request_digest)) > 0),
    response_body  TEXT    NOT NULL,
    status_code    INTEGER NOT NULL CHECK (status_code BETWEEN 100 AND 599),
    created_at     INTEGER NOT NULL
);

CREATE TABLE tasks (
    key             TEXT    PRIMARY KEY
                            CHECK (length(trim(key)) BETWEEN 1 AND 32),
    project_key     TEXT    NOT NULL REFERENCES projects(key),
    title           TEXT    NOT NULL
                            CHECK (length(trim(title)) BETWEEN 1 AND 200),
    notes           TEXT    CHECK (notes IS NULL
                                   OR length(trim(notes)) BETWEEN 1 AND 50000),
    acceptance      TEXT    CHECK (acceptance IS NULL
                                   OR length(trim(acceptance)) BETWEEN 1 AND 10000),
    state           TEXT    NOT NULL DEFAULT 'backlog'
                            CHECK (state IN
                                   ('backlog', 'ready', 'in_progress',
                                    'blocked', 'review', 'done')),
    priority        TEXT    NOT NULL DEFAULT 'normal'
                            CHECK (priority IN ('low', 'normal', 'high')),
    size            TEXT    CHECK (size IS NULL OR size IN ('S', 'M', 'L')),
    created_by      INTEGER NOT NULL REFERENCES users(id),
    version         INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    closed_at       INTEGER,
    archived_at     INTEGER CHECK (archived_at IS NULL),
    idempotency_key TEXT    NOT NULL UNIQUE REFERENCES idempotency_records(key),
    CHECK (updated_at >= created_at),
    CHECK ((state = 'done') = (closed_at IS NOT NULL))
);

CREATE TABLE task_movements (
    id           INTEGER PRIMARY KEY,
    task_key     TEXT    NOT NULL REFERENCES tasks(key),
    from_state   TEXT    CHECK (from_state IS NULL
                                OR from_state IN
                                   ('backlog', 'ready', 'in_progress',
                                    'blocked', 'review', 'done')),
    to_state     TEXT    NOT NULL
                         CHECK (to_state IN
                                ('backlog', 'ready', 'in_progress',
                                 'blocked', 'review', 'done')),
    actor_id     INTEGER NOT NULL REFERENCES users(id),
    reason       TEXT    CHECK (reason IS NULL OR length(trim(reason)) > 0),
    task_version INTEGER NOT NULL CHECK (task_version > 0),
    at           INTEGER NOT NULL,
    UNIQUE (task_key, task_version)
);

CREATE TABLE comments (
    id         INTEGER PRIMARY KEY,
    task_key   TEXT    NOT NULL REFERENCES tasks(key),
    actor_id   INTEGER NOT NULL REFERENCES users(id),
    body       TEXT    NOT NULL CHECK (length(trim(body)) BETWEEN 1 AND 10000),
    created_at INTEGER NOT NULL
);

CREATE TABLE leases (
    id            INTEGER PRIMARY KEY,
    task_key      TEXT    NOT NULL REFERENCES tasks(key),
    agent_id      INTEGER NOT NULL REFERENCES users(id),
    created_at    INTEGER NOT NULL,
    expires_at    INTEGER NOT NULL,
    released_at   INTEGER,
    closed_reason TEXT    CHECK (closed_reason IS NULL
                                 OR closed_reason IN
                                    ('expired', 'released', 'returned',
                                     'done', 'backlog')),
    CHECK (expires_at > created_at),
    CHECK ((released_at IS NULL) = (closed_reason IS NULL)),
    CHECK (released_at IS NULL OR released_at >= created_at),
    UNIQUE (id, task_key, agent_id)
);

CREATE TABLE deliveries (
    id         INTEGER PRIMARY KEY,
    task_key   TEXT    NOT NULL REFERENCES tasks(key),
    lease_id   INTEGER,
    summary    TEXT    NOT NULL CHECK (length(trim(summary)) BETWEEN 1 AND 10000),
    evidence   TEXT    NOT NULL
                       CHECK (json_valid(evidence)
                              AND json_type(evidence) = 'array'
                              AND json_array_length(evidence) BETWEEN 1 AND 20),
    created_at INTEGER NOT NULL,
    actor_id   INTEGER NOT NULL REFERENCES users(id),
    FOREIGN KEY (lease_id, task_key, actor_id)
        REFERENCES leases(id, task_key, agent_id)
);

-- At most one unreleased lease per task. Liveness by time is a separate check.
CREATE UNIQUE INDEX leases_live ON leases(task_key) WHERE released_at IS NULL;

CREATE INDEX tasks_state_idx ON tasks(state);
CREATE INDEX tasks_project_key_idx ON tasks(project_key);
CREATE INDEX task_movements_task_at_idx ON task_movements(task_key, at);
CREATE INDEX deliveries_task_key_idx ON deliveries(task_key);
CREATE INDEX comments_task_key_idx ON comments(task_key);
CREATE INDEX leases_task_key_idx ON leases(task_key);
CREATE INDEX sessions_user_id_idx ON sessions(user_id);
CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);
CREATE INDEX signup_requests_email_idx ON signup_requests(email);

INSERT INTO projects (key, name, next_task_number, created_at) VALUES
    ('MI', 'Mise',      1, CAST(strftime('%s', 'now') AS INTEGER)),
    ('PO', 'Portfolio', 1, CAST(strftime('%s', 'now') AS INTEGER)),
    ('DE', 'Despensa',  1, CAST(strftime('%s', 'now') AS INTEGER));
