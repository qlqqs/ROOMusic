-- 去标点键只用于搜索，存量值由后台投影回填，不改写原文或身份名字键。
ALTER TABLE catalog_search_values
    ADD COLUMN loose_value TEXT CHECK (loose_value IS NULL OR octet_length(loose_value) BETWEEN 1 AND 65536),
    ADD COLUMN loose_grams TEXT[],
    ADD CONSTRAINT catalog_search_values_loose_ck CHECK ((loose_value IS NULL) = (loose_grams IS NULL));

CREATE INDEX catalog_search_values_loose_trgm_idx ON catalog_search_values USING gin(loose_value public.gin_trgm_ops) WHERE loose_value IS NOT NULL;
CREATE INDEX catalog_search_values_loose_short_idx ON catalog_search_values USING gin(loose_grams) WHERE loose_grams IS NOT NULL;
CREATE INDEX catalog_search_values_loose_exact_idx ON catalog_search_values(md5(loose_value)) WHERE loose_value IS NOT NULL;

ALTER TABLE artist_search_names
    ADD COLUMN loose_value TEXT CHECK (loose_value IS NULL OR octet_length(loose_value) BETWEEN 1 AND 65536),
    ADD COLUMN loose_grams TEXT[],
    ADD CONSTRAINT artist_search_names_loose_ck CHECK ((loose_value IS NULL) = (loose_grams IS NULL));

CREATE INDEX artist_search_names_loose_trgm_idx ON artist_search_names USING gin(loose_value public.gin_trgm_ops) WHERE loose_value IS NOT NULL;
CREATE INDEX artist_search_names_loose_short_idx ON artist_search_names USING gin(loose_grams) WHERE loose_grams IS NOT NULL;
CREATE INDEX artist_search_names_loose_exact_idx ON artist_search_names(md5(loose_value)) WHERE loose_value IS NOT NULL;
