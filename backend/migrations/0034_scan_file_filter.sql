CREATE TABLE library_root_exclusions (
    root_id       UUID NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    relative_path TEXT NOT NULL CHECK (relative_path <> '' AND relative_path NOT LIKE '/%'
                                       AND octet_length(relative_path) <= 4096),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (root_id, relative_path)
);
CREATE TABLE scan_held_sources (
    scan_run_id   UUID NOT NULL REFERENCES scan_runs(id) ON DELETE CASCADE,
    root_id       UUID NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('file','directory','cue_sheet')),
    relative_path TEXT NOT NULL,
    PRIMARY KEY (scan_run_id, root_id, kind, relative_path)
);
ALTER TABLE scan_runs ADD COLUMN skipped_files BIGINT CHECK (skipped_files BETWEEN 0 AND 9007199254740991);
ALTER TABLE library_root_operations DROP CONSTRAINT library_root_operations_operation_type_check;
ALTER TABLE library_root_operations ADD CONSTRAINT library_root_operations_operation_type_check
    CHECK (operation_type IN ('create','disable','restore','exclude','unexclude'));
