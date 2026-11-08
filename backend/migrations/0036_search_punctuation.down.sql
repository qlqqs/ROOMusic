-- 回退仅删除去标点搜索派生，保留原规范键、原文与身份。
DROP INDEX artist_search_names_loose_exact_idx;
DROP INDEX artist_search_names_loose_short_idx;
DROP INDEX artist_search_names_loose_trgm_idx;
ALTER TABLE artist_search_names
    DROP CONSTRAINT artist_search_names_loose_ck,
    DROP COLUMN loose_grams,
    DROP COLUMN loose_value;

DROP INDEX catalog_search_values_loose_exact_idx;
DROP INDEX catalog_search_values_loose_short_idx;
DROP INDEX catalog_search_values_loose_trgm_idx;
ALTER TABLE catalog_search_values
    DROP CONSTRAINT catalog_search_values_loose_ck,
    DROP COLUMN loose_grams,
    DROP COLUMN loose_value;
