-- +goose Up

-- Whether a thumbnail was extracted for this file at upload time.
--
-- A boolean rather than a nullable one, because unlike extracted_meta there is
-- no third state to tell apart: either a PNG sits beside the blob or it does
-- not, and a file uploaded before this migration has none. False is the honest
-- answer for all of them, so the column is NOT NULL with a default and every
-- existing row is correct the moment the migration runs.
--
-- The thumbnail itself is not in here. It is a few kilobytes of PNG that is
-- only ever served whole, by a handler that already streams the blob it sits
-- beside, so it lives on disk as <storage key>.thumb and this column is the
-- index into that. Keeping it out of Postgres also means a restore of the
-- database and a restore of UPLOAD_DIR stay one decision rather than two.
--
-- No backfill. The blobs are on disk and could be re-read, but that is a second
-- code path for a library holding a handful of files, and re-uploading refreshes
-- it. Same posture as extracted_meta, on purpose.
ALTER TABLE model_files
    ADD COLUMN has_thumbnail boolean NOT NULL DEFAULT false;

-- The file the user pinned as the model's thumbnail.
--
-- Nullable, and null is the normal state: it means "let the server choose", and
-- the server picks the first of an image, a 3MF's embedded render, or a
-- G-code's embedded render. A value here is an override, so the choice survives
-- uploading a new file that would otherwise have won the automatic ordering.
--
-- ON DELETE SET NULL rather than a trigger or an application check. Deleting
-- the pinned file has to leave the model showing something, and the something
-- is whatever the automatic rule picks next; SET NULL says exactly that in one
-- word, and it is the only formulation that cannot be skipped by a code path
-- that forgot to look. It does mean models must be deleted files-first, which
-- is what DeleteModel already does.
--
-- No constraint that the file belongs to the model. It cannot be expressed
-- without a composite foreign key, which would mean a redundant unique index on
-- model_files (id, model_id) carried forever for a rule the only writer already
-- enforces with a WHERE clause. The setter checks it and returns 422.
ALTER TABLE models
    ADD COLUMN thumbnail_file_id bigint REFERENCES model_files (id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE models
    DROP COLUMN thumbnail_file_id;

ALTER TABLE model_files
    DROP COLUMN has_thumbnail;
