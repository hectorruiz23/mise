ALTER TABLE projects ADD COLUMN auto_dispatch INTEGER NOT NULL DEFAULT 0
    CHECK (auto_dispatch IN (0, 1));
ALTER TABLE projects ADD COLUMN dispatch_limit INTEGER NOT NULL DEFAULT 3
    CHECK (dispatch_limit > 0);
