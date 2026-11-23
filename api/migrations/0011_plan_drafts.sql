CREATE TABLE plan_drafts (
    id             INTEGER PRIMARY KEY,
    project_key    TEXT    NOT NULL REFERENCES projects(key),
    author_id      INTEGER NOT NULL REFERENCES users(id),
    repository     TEXT    NOT NULL,
    branch         TEXT    NOT NULL,
    workflow_id    INTEGER NOT NULL CHECK (workflow_id > 0),
    outcome        TEXT    NOT NULL CHECK (length(trim(outcome)) BETWEEN 1 AND 2000),
    scenario       TEXT    NOT NULL CHECK (length(trim(scenario)) > 0),
    visual_review  INTEGER NOT NULL DEFAULT 0 CHECK (visual_review IN (0, 1)),
    visible_change TEXT,
    reveal_key     TEXT,
    version        INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    state          TEXT    NOT NULL DEFAULT 'draft' CHECK (state IN ('draft', 'approved', 'discarded')),
    approved_by    INTEGER REFERENCES users(id),
    approved_at    INTEGER,
    plan_id        INTEGER REFERENCES plans(id),
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    CHECK (updated_at >= created_at),
    CHECK ((state = 'approved') = (approved_at IS NOT NULL AND plan_id IS NOT NULL)),
    CHECK (approved_at IS NULL OR approved_by IS NOT NULL),
    CHECK (visual_review = 0 OR (visible_change IS NOT NULL AND reveal_key IS NOT NULL))
);

CREATE TABLE plan_draft_tasks (
    draft_id    INTEGER NOT NULL REFERENCES plan_drafts(id),
    key         TEXT    NOT NULL CHECK (length(trim(key)) BETWEEN 1 AND 16),
    position    INTEGER NOT NULL CHECK (position >= 0),
    title       TEXT    NOT NULL CHECK (length(trim(title)) BETWEEN 1 AND 200),
    notes       TEXT    CHECK (notes IS NULL OR length(trim(notes)) BETWEEN 1 AND 50000),
    acceptance  TEXT    CHECK (acceptance IS NULL OR length(trim(acceptance)) BETWEEN 1 AND 10000),
    size        TEXT    CHECK (size IS NULL OR size IN ('S', 'M', 'L')),
    task_key    TEXT    REFERENCES tasks(key),
    PRIMARY KEY (draft_id, key),
    UNIQUE (draft_id, position)
);

CREATE TABLE plan_draft_edges (
    draft_id INTEGER NOT NULL REFERENCES plan_drafts(id),
    from_key TEXT    NOT NULL,
    to_key   TEXT    NOT NULL,
    kind     TEXT    NOT NULL CHECK (kind IN ('integration', 'deployment')),
    PRIMARY KEY (draft_id, from_key, to_key),
    CHECK (from_key <> to_key)
);
