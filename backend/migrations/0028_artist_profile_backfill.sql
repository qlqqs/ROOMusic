-- 完成代次只用于正在构建的后台覆盖校验，不撤销既有 ready 状态，也不转换存量投影。
ALTER TABLE artist_profiles ADD COLUMN applied_generation BIGINT NOT NULL DEFAULT 0 CHECK(applied_generation>=0);

-- 身份派生使用独立清单，不能以发行 subject_kind 冒充艺人身份。
ALTER TABLE catalog_projection_phases DROP CONSTRAINT catalog_projection_phases_subject_kind_check;
ALTER TABLE catalog_projection_phases ADD CONSTRAINT catalog_projection_phases_subject_kind_check
 CHECK(subject_kind IN ('release','track','artist'));
ALTER TABLE catalog_projection_work DROP CONSTRAINT catalog_projection_work_subject_kind_check;
ALTER TABLE catalog_projection_work ADD CONSTRAINT catalog_projection_work_subject_kind_check
 CHECK(subject_kind IN ('release','track','artist'));
