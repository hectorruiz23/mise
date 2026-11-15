ALTER TABLE projects ADD COLUMN is_public INTEGER NOT NULL DEFAULT 0
    CHECK (is_public IN (0, 1));

ALTER TABLE projects ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;

ALTER TABLE projects ADD COLUMN description TEXT
    CHECK (description IS NULL OR length(trim(description)) BETWEEN 1 AND 500);

ALTER TABLE projects ADD COLUMN repo_url TEXT
    CHECK (repo_url IS NULL OR (length(repo_url) <= 500 AND repo_url LIKE 'http%'));

ALTER TABLE projects ADD COLUMN demo_url TEXT
    CHECK (demo_url IS NULL OR (length(demo_url) <= 500 AND demo_url LIKE 'http%'));

ALTER TABLE projects ADD COLUMN public_state TEXT
    CHECK (public_state IS NULL OR public_state IN ('idea', 'in_progress', 'done'));
