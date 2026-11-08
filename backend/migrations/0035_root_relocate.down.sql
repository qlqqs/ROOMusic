DELETE FROM library_root_operations WHERE operation_type='relocate';
ALTER TABLE library_root_operations DROP CONSTRAINT library_root_operations_operation_type_check;
ALTER TABLE library_root_operations ADD CONSTRAINT library_root_operations_operation_type_check
    CHECK (operation_type IN ('create','disable','restore','exclude','unexclude'));
