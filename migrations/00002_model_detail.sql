-- +goose Up

-- The metadata the model detail screen edits. All three are NOT NULL DEFAULT ''
-- rather than nullable: the edit form always submits all four editable fields,
-- so "absent" and "empty" are the same state. Nullable columns would mean
-- *string in Go and a null-vs-'' branch in the SPA to buy a distinction nothing
-- makes.
--
-- print_tips is one text column, not a table. The editor is a textarea, one tip
-- per line, and the detail panel splits on newline. A model_print_tips table
-- would be a join, an ordering column and a reorder UI for exactly the same
-- rendered output.
--
-- Still no category_id: the categories table it must reference does not exist
-- until the categories milestone, and a bare bigint with no referential
-- integrity is worse than waiting. Still no updated_at either - this milestone
-- does add a write path, but nothing reads a modification time, so the column
-- would exist only to be unread.
ALTER TABLE models
    ADD COLUMN description text NOT NULL DEFAULT '',
    ADD COLUMN print_tips  text NOT NULL DEFAULT '',
    ADD COLUMN source_url  text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE models
    DROP COLUMN description,
    DROP COLUMN print_tips,
    DROP COLUMN source_url;
