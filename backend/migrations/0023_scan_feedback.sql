-- 扫描反馈只对显式启用的新扫描生效，历史记录不回填。
ALTER TABLE scan_runs
    ADD COLUMN feedback_version SMALLINT CHECK (feedback_version = 1),
    ADD COLUMN phase TEXT CHECK (phase IN ('counting','reading_tags','organizing','saving')),
    ADD COLUMN progress_bp INTEGER CHECK (progress_bp BETWEEN 0 AND 10000),
    ADD COLUMN phase_completed BIGINT CHECK (phase_completed BETWEEN 0 AND 9007199254740991),
    ADD COLUMN phase_total BIGINT CHECK (phase_total BETWEEN 0 AND 9007199254740991),
    ADD COLUMN progress_updated_at TIMESTAMPTZ,
    ADD COLUMN progress_seq BIGINT CHECK (progress_seq BETWEEN 0 AND 9007199254740991),
    ADD COLUMN files_counted BIGINT CHECK (files_counted BETWEEN 0 AND 9007199254740991),
    ADD COLUMN files_total BIGINT CHECK (files_total BETWEEN 0 AND 9007199254740991),
    ADD COLUMN files_processed BIGINT CHECK (files_processed BETWEEN 0 AND 9007199254740991),
    ADD COLUMN count_complete BOOLEAN,
    ADD COLUMN files_changed BOOLEAN,
    ADD COLUMN roots_total INTEGER CHECK (roots_total >= 0),
    ADD COLUMN current_root_index INTEGER CHECK (current_root_index >= 1 AND current_root_index <= roots_total),
    ADD COLUMN current_root_id UUID,
    ADD COLUMN current_root_name TEXT,
    ADD COLUMN report_state TEXT CHECK (report_state IN ('pending','ready','partial','unavailable')),
    ADD CONSTRAINT scan_feedback_success_progress_ck CHECK (feedback_version IS NULL OR status = 'succeeded' OR progress_bp < 10000);

CREATE TABLE scan_reports (
    scan_run_id UUID PRIMARY KEY REFERENCES scan_runs(id) ON DELETE CASCADE,
    schema_version SMALLINT NOT NULL CHECK (schema_version = 1),
    finalized_at TIMESTAMPTZ NOT NULL,
    files_counted BIGINT NOT NULL CHECK (files_counted BETWEEN 0 AND 9007199254740991),
    files_total BIGINT CHECK (files_total BETWEEN 0 AND 9007199254740991),
    files_processed BIGINT NOT NULL CHECK (files_processed BETWEEN 0 AND 9007199254740991),
    count_complete BOOLEAN NOT NULL,
    files_changed BOOLEAN NOT NULL,
    albums_added BIGINT NOT NULL CHECK (albums_added BETWEEN 0 AND 9007199254740991),
    albums_updated BIGINT NOT NULL CHECK (albums_updated BETWEEN 0 AND 9007199254740991),
    albums_missing BIGINT NOT NULL CHECK (albums_missing BETWEEN 0 AND 9007199254740991),
    tracks_added BIGINT NOT NULL CHECK (tracks_added BETWEEN 0 AND 9007199254740991),
    tracks_updated BIGINT NOT NULL CHECK (tracks_updated BETWEEN 0 AND 9007199254740991),
    tracks_missing BIGINT NOT NULL CHECK (tracks_missing BETWEEN 0 AND 9007199254740991),
    diagnostics_recorded INTEGER NOT NULL CHECK (diagnostics_recorded BETWEEN 0 AND 100),
    diagnostics_may_be_truncated BOOLEAN NOT NULL,
    missing_reconciled BOOLEAN NOT NULL
);

CREATE TABLE scan_catalog_changes (
    scan_run_id UUID NOT NULL REFERENCES scan_runs(id) ON DELETE CASCADE,
    entity_kind TEXT NOT NULL CHECK (entity_kind IN ('track','release','album')),
    entity_id UUID NOT NULL,
    existed_before BOOLEAN NOT NULL,
    present_before BOOLEAN NOT NULL,
    present_after BOOLEAN NOT NULL,
    change_mask INTEGER NOT NULL CHECK (change_mask > 0),
    first_changed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_changed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    classification TEXT CHECK (classification IN ('added','updated','missing')),
    PRIMARY KEY (scan_run_id, entity_kind, entity_id)
);

CREATE TABLE scan_added_releases (
    scan_run_id UUID NOT NULL REFERENCES scan_runs(id) ON DELETE CASCADE,
    release_id UUID NOT NULL,
    group_id_at_creation UUID NOT NULL,
    added_at TIMESTAMPTZ NOT NULL,
    title_at_creation TEXT NOT NULL,
    artist_at_creation TEXT NOT NULL,
    PRIMARY KEY (scan_run_id, release_id)
);
CREATE INDEX scan_added_releases_page_index ON scan_added_releases(scan_run_id, added_at DESC, release_id DESC);
