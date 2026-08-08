-- +goose Up

-- The library's central record. A model with a non-null parent_id is a *version*
-- of its parent: it never appears in the library grid, and grid counts, filters
-- and pagination are all over roots only. Nothing in milestone 1 creates a
-- version, but the column exists now so the list query is roots-only from the
-- start rather than being retrofitted later.
--
-- description, category_id, print_tips, source_url and custom_meta are part of
-- the domain model but not of this milestone. goose migrations are additive, and
-- category_id in particular cannot land yet: the categories table it references
-- does not exist until the categories milestone, so adding it now would mean a
-- bare bigint with no referential integrity.
CREATE TABLE models (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    parent_id  bigint REFERENCES models (id) ON DELETE CASCADE,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Partial, because every listing query this app makes is roots-only.
CREATE INDEX models_user_roots_idx
    ON models (user_id, created_at DESC, id DESC) WHERE parent_id IS NULL;

-- Named model_files rather than files because the foundation's shared migrations
-- already create a files table for its own generic file service. This app does
-- not register those routes, but the table is still there.
--
-- type is one of stl, 3mf, gcode, step, obj, image, document. It is derived from
-- the filename extension at upload and authoritative once written. There is no
-- CHECK constraint: the Go mapper is total (every extension lands on one of the
-- seven, defaulting to document) and unit-tested, and a CHECK would be a second
-- copy of the same list to keep in sync.
--
-- content_type is the browser's hint from the multipart part header, stored as
-- the hint it is. Nothing branches on it in this milestone.
CREATE TABLE model_files (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    model_id     bigint NOT NULL REFERENCES models (id) ON DELETE CASCADE,
    storage_key  text NOT NULL UNIQUE,
    filename     text NOT NULL,
    type         text NOT NULL,
    content_type text NOT NULL,
    size_bytes   bigint NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX model_files_model_idx ON model_files (model_id, id);

-- +goose Down
DROP TABLE model_files;
DROP TABLE models;
