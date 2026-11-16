DROP TABLE steward_reviews;
UPDATE steward_plan_items SET decision='declined' WHERE decision='rejected_by_review';
ALTER TABLE steward_plan_items DROP CONSTRAINT steward_plan_items_decision_check;
ALTER TABLE steward_plan_items ADD CONSTRAINT steward_plan_items_decision_check CHECK (decision IN ('pending','accepted','declined'));
ALTER TABLE steward_user_settings DROP COLUMN review_model;
