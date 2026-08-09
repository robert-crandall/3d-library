-- +goose Up

-- The SHA-256 of the file's bytes, lowercase hex, written only by the duplicate
-- scan. Nullable, and it stays NULL forever for a file whose size is unique -
-- that is the point of the feature. Hashing every upload would read hundreds of
-- gigabytes to answer a question about the handful of files that share a size.
--
-- Safe to treat as a cache rather than a snapshot: a published blob is never
-- rewritten (publish() renames a staged temp file into place, Update edits
-- metadata, SetThumbnail moves a pin, and there is no replace-file endpoint), so
-- a hash is permanently correct once computed and a rescan never re-reads a file
-- it has already hashed.
--
-- No CHECK on the length. The only writer is hex.EncodeToString of a
-- sha256.Sum256, so a constraint would restate the type of a function.
ALTER TABLE model_files
    ADD COLUMN content_hash text;

-- Partial because every read of this column is "the files that have one" - the
-- groups query and its HAVING subquery both carry content_hash IS NOT NULL. The
-- NULL rows are the majority and are never looked up by hash.
CREATE INDEX model_files_content_hash_idx
    ON model_files (content_hash) WHERE content_hash IS NOT NULL;

-- When the duplicate scan last read every candidate. One row per user, upserted
-- at the end of a complete run.
--
-- A table rather than a variable because the hashes are durable: the groups
-- survive a restart, and a screen showing three-day-old groups while claiming it
-- has never scanned is lying.
--
-- It cannot be derived from the file rows either. A library where every size is
-- unique hashes nothing, so a max() over some per-row hashed_at would report the
-- *previous* scan's time - and "I scanned and found nothing" is exactly the case
-- this column exists to report.
--
-- The value stored is the time candidate selection began, not the time the run
-- finished. A file uploaded mid-scan is not in the candidate set, so the start
-- time is the honest description of what the run covered.
CREATE TABLE library_scans (
    user_id              bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    duplicates_scanned_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE library_scans;
DROP INDEX model_files_content_hash_idx;
ALTER TABLE model_files DROP COLUMN content_hash;
