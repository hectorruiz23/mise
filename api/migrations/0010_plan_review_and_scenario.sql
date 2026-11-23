ALTER TABLE plans ADD COLUMN scenario TEXT NOT NULL DEFAULT '';
ALTER TABLE plans ADD COLUMN visible_change TEXT;
ALTER TABLE plans ADD COLUMN visual_review INTEGER NOT NULL DEFAULT 0
    CHECK (visual_review IN (0, 1));
ALTER TABLE plans ADD COLUMN completed_at INTEGER;

ALTER TABLE plan_nodes ADD COLUMN reveals INTEGER NOT NULL DEFAULT 0
    CHECK (reveals IN (0, 1));
ALTER TABLE plan_nodes ADD COLUMN correction INTEGER NOT NULL DEFAULT 0
    CHECK (correction IN (0, 1));
ALTER TABLE plan_nodes ADD COLUMN origin_evidence TEXT;

CREATE UNIQUE INDEX plan_nodes_single_reveal ON plan_nodes (plan_id) WHERE reveals = 1;
CREATE UNIQUE INDEX plan_nodes_single_correction ON plan_nodes (plan_id, origin_evidence)
    WHERE origin_evidence IS NOT NULL;

CREATE TABLE plan_scenarios (
    plan_id      INTEGER NOT NULL REFERENCES plans(id),
    task_key     TEXT    NOT NULL REFERENCES tasks(key),
    head_sha     TEXT    NOT NULL
                         CHECK (length(head_sha) = 40
                                AND head_sha NOT GLOB '*[^0-9a-f]*'),
    command      TEXT    NOT NULL CHECK (length(trim(command)) > 0),
    outcome      TEXT    NOT NULL CHECK (outcome IN ('passed', 'failed')),
    evidence_url TEXT,
    notes        TEXT,
    recorded_at  INTEGER NOT NULL,
    PRIMARY KEY (plan_id, task_key, head_sha)
);

CREATE TABLE plan_approvals (
    plan_id     INTEGER NOT NULL REFERENCES plans(id),
    task_key    TEXT    NOT NULL REFERENCES tasks(key),
    head_sha    TEXT    NOT NULL
                        CHECK (length(head_sha) = 40
                               AND head_sha NOT GLOB '*[^0-9a-f]*'),
    reviewer    TEXT    NOT NULL CHECK (length(trim(reviewer)) > 0),
    approved_at INTEGER NOT NULL,
    PRIMARY KEY (plan_id, task_key, head_sha)
);
