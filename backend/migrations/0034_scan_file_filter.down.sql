DELETE FROM library_root_operations WHERE operation_type IN ('exclude','unexclude');
ALTER TABLE library_root_operations DROP CONSTRAINT library_root_operations_operation_type_check;
ALTER TABLE library_root_operations ADD CONSTRAINT library_root_operations_operation_type_check
    CHECK (operation_type IN ('create','disable','restore'));
ALTER TABLE scan_runs DROP COLUMN skipped_files;
DROP TABLE scan_held_sources;
DROP TABLE library_root_exclusions;
