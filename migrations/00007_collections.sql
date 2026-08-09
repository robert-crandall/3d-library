-- +goose Up

-- A collection is a named bag of models for a multi-part build - the six parts
-- of a Voron, the things being printed as gifts. Many-to-many, so a model can
-- be in several at once and being in one takes it out of nothing.
--
-- Same shape as tags and categories, and for the same reasons: the name is
-- stored as typed because it is a heading the user reads, uniqueness is
-- case-insensitive per user through a functional index, and the insert path
-- does not pre-check - it inserts and turns a unique violation into a 422.
--
-- description is NOT NULL DEFAULT '' rather than nullable. It is optional in
-- the UI, and "" and NULL would mean the same thing, so only one of them should
-- be representable.
--
-- No created_at, for the reason 00005 gives: nothing sorts or shows it, and
-- names are unique per user, so ordering by lower(name) is already stable.
CREATE TABLE collections (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        text NOT NULL,
    description text NOT NULL DEFAULT ''
);

-- Leading with user_id makes this the index that serves "this user's
-- collections" as well as the uniqueness rule, so there is no second one.
CREATE UNIQUE INDEX collections_user_name_idx ON collections (user_id, lower(name));

-- Membership. Deliberately REFERENCES models (id) and not models (root_id),
-- even though every count and every view in this app is over roots only.
--
-- Pointing at root_id would make the rule "a collection holds roots" a
-- constraint, which is normally the right instinct - but root_id is generated
-- from parent_id, so attaching a model as a version nulls it, and the
-- membership rows would dangle. ON UPDATE NO ACTION refuses, which means any
-- model that is in a collection could never become a version. That is a working
-- feature refusing a legal action to enforce a rule that is about reading.
--
-- So the roots-only rule stays where it already is for tags: in the count's
-- FILTER and in the list's WHERE. A version keeps its collections the same way
-- it keeps its tags - they exist, and they do not appear in a grid that has
-- never shown versions.
--
-- The primary key is the pair, so adding the same model twice is a conflict
-- rather than a duplicate row. The reverse index exists because the sidebar
-- counts models per collection and the grid filters by one, both collection
-- first. Both columns cascade, and these rows name nothing on disk, so
-- DeleteModel is unchanged.
CREATE TABLE model_collections (
    model_id      bigint NOT NULL REFERENCES models (id) ON DELETE CASCADE,
    collection_id bigint NOT NULL REFERENCES collections (id) ON DELETE CASCADE,
    PRIMARY KEY (model_id, collection_id)
);

CREATE INDEX model_collections_collection_idx ON model_collections (collection_id);

-- +goose Down
DROP TABLE model_collections;
DROP TABLE collections;
