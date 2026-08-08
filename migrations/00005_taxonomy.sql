-- +goose Up

-- A model's category, at most one, and the sidebar's primary grouping.
--
-- The name is stored as the user typed it, because the design capitalises
-- "Functional" and "Tools & jigs" and a normalised name would have to be
-- un-normalised for display. Uniqueness is then case-insensitive through a
-- functional unique index rather than through citext or a check in Go. citext
-- needs CREATE EXTENSION, which the app's role may not have on a managed
-- Postgres, and it changes comparison semantics for every future query on the
-- column; a check in Go is a race, because two requests can both find nothing
-- and both insert. The index is the constraint, so the insert path does not
-- pre-check at all: it inserts and turns a unique violation into a 422.
--
-- color is a #rrggbb string validated in Go. It is user data rendered into an
-- inline style attribute, which is why it is not in the app's palette: app.css
-- names the *application's* colours, and one of these is whatever the user
-- picked for "Miniatures".
CREATE TABLE categories (
    id      bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name    text NOT NULL,
    color   text NOT NULL
);

CREATE UNIQUE INDEX categories_user_name_idx ON categories (user_id, lower(name));

-- Tags and materials are the same table twice, deliberately not one table with a
-- kind column. They are unrelated vocabularies that happen to have the same
-- shape: a tag is how the user describes a model, a material is what it prints
-- in, and nothing ever wants both in one list. A shared table would mean every
-- query carrying a kind predicate, and a unique index that has to include kind
-- to stop "PLA" the tag colliding with "PLA" the material.
--
-- No created_at on any of the three: nothing sorts or shows it, and names are
-- unique per user, so ordering by lower(name) is already stable.
CREATE TABLE tags (
    id      bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name    text NOT NULL
);

CREATE UNIQUE INDEX tags_user_name_idx ON tags (user_id, lower(name));

CREATE TABLE materials (
    id      bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name    text NOT NULL
);

CREATE UNIQUE INDEX materials_user_name_idx ON materials (user_id, lower(name));

-- The column 00001 deliberately left out, now that there is a table to point at.
--
-- ON DELETE SET NULL is the whole of "deleting a category leaves its models
-- uncategorized rather than cascading". Like thumbnail_file_id, it is the one
-- formulation no code path can forget to apply.
ALTER TABLE models
    ADD COLUMN category_id bigint REFERENCES categories (id) ON DELETE SET NULL;

-- Partial for the same reason models_user_roots_idx is: a version never appears
-- in the grid, so both the filtered list and the sidebar's per-category counts
-- are over roots only.
CREATE INDEX models_category_idx ON models (category_id) WHERE parent_id IS NULL;

-- Tag and material membership. The primary key is the pair, so a model cannot
-- carry the same tag twice and no extra unique index is needed; the reverse
-- index exists because the sidebar counts models per tag and the grid filters by
-- one, both of which read tag-first.
--
-- Both cascade from models, which is why DeleteModel does not change: it deletes
-- model_files first because those rows name blobs on disk, then the model, and
-- these rows hold nothing but the pair.
CREATE TABLE model_tags (
    model_id bigint NOT NULL REFERENCES models (id) ON DELETE CASCADE,
    tag_id   bigint NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (model_id, tag_id)
);

CREATE INDEX model_tags_tag_idx ON model_tags (tag_id);

CREATE TABLE model_materials (
    model_id    bigint NOT NULL REFERENCES models (id) ON DELETE CASCADE,
    material_id bigint NOT NULL REFERENCES materials (id) ON DELETE CASCADE,
    PRIMARY KEY (model_id, material_id)
);

CREATE INDEX model_materials_material_idx ON model_materials (material_id);

-- Every account starts with the five common filaments.
--
-- The trigger point has to be account creation, and this app has no hook there:
-- registration lives in the foundation module's auth package, has two paths
-- (password and Google), and offers no callback to run Go code on either. A
-- trigger is the only place that sees both, fires exactly once per account, and
-- works on a fresh install, where no user exists when this migration runs and a
-- backfill alone would seed nobody.
--
-- These are ordinary rows once created. Renaming or deleting one is the user's
-- business, and nothing puts it back.
-- +goose StatementBegin
CREATE FUNCTION seed_default_materials() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO materials (user_id, name)
    VALUES (NEW.id, 'PLA'), (NEW.id, 'PETG'), (NEW.id, 'ABS'), (NEW.id, 'ASA'), (NEW.id, 'TPU');
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER users_seed_default_materials
    AFTER INSERT ON users
    FOR EACH ROW EXECUTE FUNCTION seed_default_materials();

-- The trigger covers every account made from here on; this covers the ones that
-- already exist. ON CONFLICT because the two overlap for nobody today but would
-- if this migration were ever re-run against a partially seeded database.
INSERT INTO materials (user_id, name)
SELECT u.id, m.name
FROM users u
CROSS JOIN (VALUES ('PLA'), ('PETG'), ('ABS'), ('ASA'), ('TPU')) AS m (name)
ON CONFLICT DO NOTHING;

-- +goose Down
DROP TRIGGER users_seed_default_materials ON users;
DROP FUNCTION seed_default_materials();

ALTER TABLE models
    DROP COLUMN category_id;

DROP TABLE model_materials;
DROP TABLE model_tags;
DROP TABLE materials;
DROP TABLE tags;
DROP TABLE categories;
