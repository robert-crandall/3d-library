-- +goose Up

-- Versions are one level deep. Milestone 1 wrote parent_id with the rule already
-- in its comment, but nothing enforced it: a plain self-reference is happy to
-- build a chain. This makes the database refuse one.
--
-- root_id is the model's own id when it is a root, and NULL when it is a version.
-- So only roots are referenceable, and pointing parent_id at root_id is the whole
-- constraint:
--
--   * a version's parent must exist and must be a root, because a version's
--     root_id is NULL and NULL is not a key anything can reference;
--   * a root is unconstrained, because a NULL parent_id makes the foreign key
--     check no-op under MATCH SIMPLE;
--   * a root that already has versions cannot become a version itself. Doing so
--     would null its root_id, and its children's references would dangle, which
--     ON UPDATE NO ACTION (the default) refuses;
--   * a model cannot be its own parent, for the same reason: setting parent_id
--     nulls root_id, and the row would then reference a key it just removed.
--
-- Chosen over a trigger because a trigger has to read the other row and hold a
-- lock while it does, and getting that right is more code than a constraint that
-- already does it. A partial unique index cannot back a foreign key, so the
-- generated column is what makes this expressible declaratively.
ALTER TABLE models ADD COLUMN root_id bigint
    GENERATED ALWAYS AS (CASE WHEN parent_id IS NULL THEN id ELSE NULL END) STORED;

-- The referenced side of a foreign key must be unique. NULLs are not equal to
-- each other, so every version's NULL coexists here happily.
CREATE UNIQUE INDEX models_root_id_idx ON models (root_id);

ALTER TABLE models DROP CONSTRAINT models_parent_id_fkey;

-- ON DELETE CASCADE is carried over from the original constraint deliberately:
-- deleting a parent still takes its versions with it, which is what the delete
-- path expects and what the confirmation dialog promises.
ALTER TABLE models ADD CONSTRAINT models_parent_root_fkey
    FOREIGN KEY (parent_id) REFERENCES models (root_id) ON DELETE CASCADE;

-- +goose Down

ALTER TABLE models DROP CONSTRAINT models_parent_root_fkey;
DROP INDEX models_root_id_idx;
ALTER TABLE models DROP COLUMN root_id;
ALTER TABLE models ADD CONSTRAINT models_parent_id_fkey
    FOREIGN KEY (parent_id) REFERENCES models (id) ON DELETE CASCADE;
