ALTER TABLE plans ADD COLUMN target_environment TEXT NOT NULL DEFAULT '';

ALTER TABLE merge_attempts ADD COLUMN delivery_id INTEGER REFERENCES deliveries(id);
