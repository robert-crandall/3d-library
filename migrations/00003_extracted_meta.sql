-- +goose Up

-- What the slicer said about the print, parsed out of the G-code comments at
-- upload time.
--
-- Nullable, with no default, because three states have to be told apart and two
-- of them are not "empty": a G-code file we read settings from, a file we could
-- not attribute to a slicer, and every file uploaded before this migration
-- existed. All three read as NULL except the first, and the detail screen shows
-- a panel only for the first.
--
-- jsonb rather than columns. Every field in here is derived - re-slicing the
-- model produces a new file and a new row, and nothing is ever edited by hand -
-- so the usual reason to give a value its own column, that something writes to
-- it, does not apply. The set of fields is also still moving: each slicer
-- spells things differently and adding support for another one should not be a
-- migration. Nothing filters or sorts on these values today, so there is no
-- index either; a jsonb_path_ops index can be added by whichever milestone
-- first wants to search by layer height.
--
-- No backfill. The blobs are still on disk and could be re-read, but a backfill
-- job is a second code path to write and test for a library that currently
-- holds a handful of files, and re-uploading a file fills it in. If a real
-- library ever needs it, a one-shot command is a better answer than a migration
-- that reads the filesystem.
ALTER TABLE model_files
    ADD COLUMN extracted_meta jsonb;

-- +goose Down
ALTER TABLE model_files
    DROP COLUMN extracted_meta;
