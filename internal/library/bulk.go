package library

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// MaxBulkModels bounds one bulk action. The grid can only ever select what one
// page shows, because selection clears on paging, so this is not a UI limit -
// it is a bound on how much work one transaction does, and how long the locks
// bulk delete takes are held.
const MaxBulkModels = 200

// MaxBulkTags bounds the tags one bulk action can add. The picker lists the
// user's whole taxonomy, so this is the same kind of bound as MaxBulkModels.
const MaxBulkTags = 100

// ErrChanged means the models moved under the request: either the caller asked
// to destroy a set that no longer matches what it was shown (see BulkDelete), or
// one of them stopped being a root, or became one, while its locks were being
// taken (see lockModels). Both are retryable and neither wrote anything.
var ErrChanged = errors.New("library: the selection changed")

// DeletePreview is what a bulk delete is about to destroy: the selected models,
// the versions that will go with them, and every file between them.
type DeletePreview struct {
	Models   int `json:"models" doc:"How many of the selected models exist"`
	Versions int `json:"versions" doc:"How many versions will go with them"`
	Files    int `json:"files" doc:"How many files will be destroyed in total"`
}

// dedupe sorts and removes duplicates. Bulk requests carry ids from a UI
// selection, and every ownership check here compares a resolved count to the
// length of the request, so the request has to be a set for that comparison to
// mean anything.
// lockModels takes a row lock on every model in ids the caller owns, in the one
// order this package locks model rows in: roots by ascending id, then versions
// by ascending id.
//
// That is BulkDelete's two phases, and everything that touches more than one
// model follows it - including the bulk actions whose own statements would take
// these rows anyway, because a foreign key's key-share locks and an UPDATE's row
// locks land in whatever order the executor picked, and the order is the point.
//
// The phases are two statements and therefore two snapshots, which is the one
// thing this cannot paper over: a version detached between them is a root by the
// time the second statement looks, so neither statement locks it, and the row
// the caller is about to write is a row it does not hold. Late-locking it here
// is exactly the out-of-order acquisition the ordering exists to prevent, so
// this counts instead - a row that exists, is owned, and was not locked means
// the shape changed mid-flight, and ErrChanged sends the caller back to try
// again having written nothing. The count takes no locks, so it cannot itself
// join a cycle.
//
// Rows another user owns are not locked, because they are also not writable and
// every caller's own predicate has already turned them into a 404 or a 422.
func lockModels(ctx context.Context, tx pgx.Tx, userID int64, ids []int64) error {
	var locked int64
	for _, roots := range []bool{true, false} {
		tag, err := tx.Exec(ctx,
			`SELECT id FROM models
			  WHERE id = ANY($1) AND user_id = $2 AND (root_id IS NOT NULL) = $3
			  ORDER BY id FOR UPDATE`,
			ids, userID, roots)
		if err != nil {
			return fmt.Errorf("library: lock models: %w", err)
		}
		locked += tag.RowsAffected()
	}

	var live int64
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM models WHERE id = ANY($1) AND user_id = $2`,
		ids, userID).Scan(&live); err != nil {
		return fmt.Errorf("library: lock models: %w", err)
	}
	if live != locked {
		return ErrChanged
	}
	return nil
}

func dedupe(ids []int64) []int64 {
	out := slices.Clone(ids)
	slices.Sort(out)
	return slices.Compact(out)
}

// checkBulkIDs is the one place a bulk request's shape is judged. huma already
// rejects an empty or oversized list from the schema, so this is the backstop
// for a caller that reaches the service directly - it exists because the limit
// belongs to the service, not to the API layer that happens to advertise it.
func checkBulkIDs(ids []int64) error {
	switch {
	case len(ids) == 0:
		return fmt.Errorf("%w: select at least one model", errInvalid)
	case len(ids) > MaxBulkModels:
		return fmt.Errorf("%w: at most %d models at a time", errInvalid, MaxBulkModels)
	}
	return nil
}

// BulkAddTags adds every tag to every model, leaving the tags they already have
// alone.
//
// One statement, and it is AddModelToCollection's CTE with unnest on both
// sides. m and t resolve the two sets scoped to userID in the one place they
// are resolved, which is what makes another user's id a 404 rather than a 403;
// ins is sourced from them, so it cannot reach a row the caller does not own;
// and the counts come back out of the same statement because RowsAffected
// cannot tell "already tagged" from "nothing to insert" and those are a success
// and a failure.
//
// A short count is an error, and the transaction is what makes that
// all-or-nothing: a data-modifying CTE runs to completion whether or not the
// outer query reads it, so by the time the counts are compared the good pairs
// are already inserted and only the rollback undoes them.
//
// The models are locked first, and only so that the key-share locks the
// insert's foreign keys take land in a fixed order - see lockModels. A tag
// deleted concurrently is still a race the lock does not cover: it either lands
// before this and is missing from the count, or after and trips the constraint,
// which is the gap AddModelToCollection documents, with the same answer.
func (s *Service) BulkAddTags(ctx context.Context, userID int64, modelIDs, tagIDs []int64) error {
	models := dedupe(modelIDs)
	if err := checkBulkIDs(models); err != nil {
		return err
	}
	tags := dedupe(tagIDs)
	switch {
	case len(tags) == 0:
		return fmt.Errorf("%w: select at least one tag", errInvalid)
	case len(tags) > MaxBulkTags:
		return fmt.Errorf("%w: at most %d tags at a time", errInvalid, MaxBulkTags)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("library: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := lockModels(ctx, tx, userID, models); err != nil {
		return err
	}

	var gotModels, gotTags int
	err = tx.QueryRow(ctx,
		`WITH m AS (SELECT id FROM models WHERE id = ANY($2) AND user_id = $1),
		      t AS (SELECT id FROM tags   WHERE id = ANY($3) AND user_id = $1),
		      ins AS (
		        INSERT INTO model_tags (model_id, tag_id)
		        SELECT m.id, t.id FROM m CROSS JOIN t
		        ON CONFLICT DO NOTHING
		      )
		 SELECT (SELECT count(*) FROM m), (SELECT count(*) FROM t)`,
		userID, models, tags).Scan(&gotModels, &gotTags)
	if missingModel(err) {
		return ErrNotFound
	}
	if isMissingReference(err) {
		return fmt.Errorf("%w: unknown tag", errInvalid)
	}
	if err != nil {
		return fmt.Errorf("library: bulk add tags: %w", err)
	}
	if gotModels != len(models) {
		return ErrNotFound
	}
	if gotTags != len(tags) {
		return fmt.Errorf("%w: unknown tag", errInvalid)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("library: commit: %w", err)
	}
	return nil
}

// missingModel says the foreign key that failed was model_id rather than the
// label's. Both CTEs below read their models and insert against them in one
// statement, and the key's own check runs at read committed, so a model deleted
// in another tab in that gap raises 23503 on the models key. Reporting that as
// "unknown tag" would be a lie about the one thing the caller got right, so it
// is what it is everywhere else: the model is not there, 404.
//
// No test forces this; the window is inside a single statement, so there is no
// point to park a second connection at.
func missingModel(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503" &&
		strings.Contains(pgErr.ConstraintName, "model_id")
}

// BulkAddToCollection puts every model in one collection, skipping the ones
// already in it. Same statement as BulkAddTags with a scalar on the right.
func (s *Service) BulkAddToCollection(ctx context.Context, userID int64, modelIDs []int64, collectionID int64) error {
	models := dedupe(modelIDs)
	if err := checkBulkIDs(models); err != nil {
		return err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("library: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := lockModels(ctx, tx, userID, models); err != nil {
		return err
	}

	var gotModels, gotCollections int
	err = tx.QueryRow(ctx,
		`WITH m AS (SELECT id FROM models      WHERE id = ANY($2) AND user_id = $1),
		      c AS (SELECT id FROM collections WHERE id = $3      AND user_id = $1),
		      ins AS (
		        INSERT INTO model_collections (model_id, collection_id)
		        SELECT m.id, c.id FROM m CROSS JOIN c
		        ON CONFLICT DO NOTHING
		      )
		 SELECT (SELECT count(*) FROM m), (SELECT count(*) FROM c)`,
		userID, models, collectionID).Scan(&gotModels, &gotCollections)
	if missingModel(err) {
		return ErrNotFound
	}
	if isMissingReference(err) {
		return fmt.Errorf("%w: unknown collection", errInvalid)
	}
	if err != nil {
		return fmt.Errorf("library: bulk add to collection: %w", err)
	}
	if gotModels != len(models) {
		return ErrNotFound
	}
	if gotCollections != 1 {
		return fmt.Errorf("%w: unknown collection", errInvalid)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("library: commit: %w", err)
	}
	return nil
}

// BulkSetCategory puts every model in one category, replacing whatever each had.
//
// The category is checked in its own statement rather than as a subquery in the
// UPDATE, for the reason setCategory records: SET category_id = (SELECT ...
// WHERE user_id = $n) writes NULL for a foreign id, which silently
// uncategorizes every selected model instead of refusing.
//
// The UPDATE carries AND user_id = $1, which is what makes its rows-affected
// count an ownership proof and saves a separate SELECT over the models.
func (s *Service) BulkSetCategory(ctx context.Context, userID int64, modelIDs []int64, categoryID int64) error {
	models := dedupe(modelIDs)
	if err := checkBulkIDs(models); err != nil {
		return err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("library: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := lockModels(ctx, tx, userID, models); err != nil {
		return err
	}

	var known int
	err = tx.QueryRow(ctx,
		`SELECT 1 FROM categories WHERE id = $1 AND user_id = $2`,
		categoryID, userID).Scan(&known)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: unknown category", errInvalid)
	}
	if err != nil {
		return fmt.Errorf("library: check category: %w", err)
	}

	tag, err := tx.Exec(ctx,
		`UPDATE models SET category_id = $1 WHERE id = ANY($2) AND user_id = $3`,
		categoryID, models, userID)
	// The SELECT above takes no lock and the foreign key's is taken after it, so
	// a category deleted in that gap is visible to the check and gone by the
	// time the constraint looks. Same race, same answer as setCategory: the
	// category is not there and the models cannot have it.
	if isMissingReference(err) {
		return fmt.Errorf("%w: unknown category", errInvalid)
	}
	if err != nil {
		return fmt.Errorf("library: bulk set category: %w", err)
	}
	if tag.RowsAffected() != int64(len(models)) {
		return ErrNotFound
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("library: commit: %w", err)
	}
	return nil
}

// PreviewBulkDelete counts what deleting these models would destroy. It exists
// because the confirmation has to be truthful and the grid cannot work the
// number out: a listed model's fileCount is its own files, and deleting a root
// takes its versions and their files too.
func (s *Service) PreviewBulkDelete(ctx context.Context, userID int64, modelIDs []int64) (DeletePreview, error) {
	models := dedupe(modelIDs)
	if err := checkBulkIDs(models); err != nil {
		return DeletePreview{}, err
	}

	var out DeletePreview
	err := s.db.QueryRow(ctx,
		`WITH roots AS (
		        SELECT id FROM models
		         WHERE id = ANY($2) AND user_id = $1 AND root_id IS NOT NULL
		      ),
		      vers AS (SELECT id FROM models WHERE parent_id IN (SELECT id FROM roots))
		 SELECT (SELECT count(*) FROM roots),
		        (SELECT count(*) FROM vers),
		        (SELECT count(*) FROM model_files
		          WHERE model_id IN (SELECT id FROM roots UNION ALL SELECT id FROM vers))`,
		userID, models).Scan(&out.Models, &out.Versions, &out.Files)
	if err != nil {
		return DeletePreview{}, fmt.Errorf("library: preview bulk delete: %w", err)
	}
	if out.Models != len(models) {
		return DeletePreview{}, ErrNotFound
	}
	return out, nil
}

// BulkDelete removes every selected model, its versions, all of their file rows,
// and then their blobs.
//
// expect is what the caller was shown by PreviewBulkDelete. Deletes here are
// permanent and there is no trash, so the confirmation sentence is load-bearing:
// this rechecks the numbers under the locks, after the file rows are counted, so
// the pair compared is the pair actually about to be destroyed. A mismatch -
// the user attached a version or uploaded a file in another tab while the dialog
// was open - is ErrChanged and the transaction rolls back untouched. The model
// count is not part of expect because the ownership check below already refuses
// a set that lost a member.
//
// The locking is DeleteModel's, generalized, and the two statements stay two
// statements for the reason recorded there: folding them loses a version
// attached during the lock wait and strands its blobs.
func (s *Service) BulkDelete(ctx context.Context, userID int64, modelIDs []int64, expect DeletePreview) error {
	models := dedupe(modelIDs)
	if err := checkBulkIDs(models); err != nil {
		return err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("library: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// root_id IS NOT NULL is the indexed "is this a root" predicate, and it is
	// here for deadlock safety rather than tidiness. Phase one below locks only
	// roots and phase two only versions, and those sets cannot overlap, which is
	// what makes two concurrent bulk deletes safe:
	//
	//   * Two transactions both in phase two hold all of their own roots. A
	//     version has exactly one parent, so a version they both want implies a
	//     root they both hold, which is impossible. Their phase-two sets are
	//     therefore disjoint and neither can wait on the other.
	//   * A transaction in phase one holds only roots, so a transaction in phase
	//     two - which waits only on versions - can never be waiting on it.
	//
	// Accepting a version here breaks that: phase one would lock a version, and
	// a second delete holding that version's parent and waiting for a root the
	// first one holds closes the cycle. So the addressable set is roots, which
	// is also all the grid can select. A version id is not found here; delete it
	// on its own with DELETE /api/models/{id}.
	//
	// ORDER BY id then handles the within-phase case: two deletes over
	// overlapping selections queue for the lowest id they share and one waits.
	// SetParent goes through lockModels for the same reason, because attaching C
	// to P while this is parked on P is otherwise the same two rows in the
	// opposite order.
	//
	// One inversion is left and it is older than this: DeleteFile removes a file
	// row, and ON DELETE SET NULL on thumbnail_file_id then updates the model,
	// so it takes file-then-model where every delete here takes model-then-file.
	// Single-model DeleteModel has had that cycle since M4, so it is not new here,
	// though holding more rows for longer does widen the window. Fixing it means
	// locking the model in DeleteFile, a path this milestone does not otherwise
	// touch, so it is a follow-up.
	ids, err := collectIDs(ctx, tx,
		`SELECT id FROM models
		  WHERE id = ANY($1) AND user_id = $2 AND root_id IS NOT NULL
		  ORDER BY id FOR UPDATE`, models, userID)
	if err != nil {
		return fmt.Errorf("library: check models: %w", err)
	}
	if len(ids) != len(models) {
		return ErrNotFound
	}

	// A *second* statement, and the order is load-bearing; see DeleteModel. This
	// one's snapshot is taken after the roots are locked, so it sees every
	// version that committed while the statement above was waiting.
	versions, err := collectIDs(ctx, tx,
		`SELECT id FROM models WHERE parent_id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return fmt.Errorf("library: check versions: %w", err)
	}

	// Files are deleted explicitly rather than left to the cascade, because
	// RETURNING is how the storage keys are learnt. Files before models, so
	// thumbnail_file_id's ON DELETE SET NULL fires while the models are there.
	rows, err := tx.Query(ctx,
		`DELETE FROM model_files WHERE model_id = ANY($1) RETURNING storage_key`,
		append(slices.Clone(ids), versions...))
	if err != nil {
		return fmt.Errorf("library: delete files: %w", err)
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return fmt.Errorf("library: delete files: %w", err)
		}
		keys = append(keys, key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("library: delete files: %w", err)
	}

	if len(versions) != expect.Versions || len(keys) != expect.Files {
		return ErrChanged
	}

	// Only the roots: ON DELETE CASCADE takes the versions, whose file rows are
	// already gone.
	if _, err := tx.Exec(ctx, `DELETE FROM models WHERE id = ANY($1)`, ids); err != nil {
		return fmt.Errorf("library: delete models: %w", err)
	}

	// Blobs only once the commit is acknowledged: a commit error is ambiguous,
	// so unlinking on that path could destroy the blobs of rows that survived.
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("library: commit: %w", err)
	}

	s.removeBlobs(ctx, keys)
	return nil
}

// collectIDs runs a query returning one bigint column. Two of BulkDelete's
// statements have the same shape and the same nine lines of scanning.
func collectIDs(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]int64, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
