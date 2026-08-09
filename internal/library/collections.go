package library

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// maxDescriptionLen bounds a collection's description. The API declares the
// same number; this is the backstop for a caller not going through the schema.
// It is a subtitle under a heading, not a body of text - the model's own
// description is the long one.
const maxDescriptionLen = 500

// CollectionSummary is a collection everywhere the app shows one: the sidebar
// row, the settings list, and the heading over its view.
//
// It carries the description because the sidebar store is the only thing that
// ever reads a collection, so the grid's heading comes from the same list the
// sidebar renders. That is why there is no GET /api/collections/{id}.
//
// ModelCount is over root models, like every other count here, and it is also
// the number the delete confirmation says out loud.
type CollectionSummary struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ModelCount  int    `json:"modelCount"`
}

// cleanDescription trims a collection description and bounds it. Unlike a name,
// empty is allowed: the description is optional, and "" is how that is stored.
func cleanDescription(description string) (string, error) {
	description = strings.TrimSpace(description)
	if utf8.RuneCountInString(description) > maxDescriptionLen {
		return "", fmt.Errorf("%w: a description can be at most %d characters", errInvalid, maxDescriptionLen)
	}
	return description, nil
}

// ListCollections returns the user's collections with the number of root models
// in each, ordered by name.
//
// The count joins models only when parent_id IS NULL, so a version that is in a
// collection is not counted - the same shape as ListTags, and for the same
// reason: nothing about a version appears in the grid, so nothing about a
// version should appear in a number describing the grid.
func (s *Service) ListCollections(ctx context.Context, userID int64) ([]CollectionSummary, error) {
	rows, err := s.db.Query(ctx,
		`SELECT c.id, c.name, c.description, count(m.id)
		   FROM collections c
		   LEFT JOIN model_collections mc ON mc.collection_id = c.id
		   LEFT JOIN models m ON m.id = mc.model_id AND m.parent_id IS NULL
		  WHERE c.user_id = $1
		  GROUP BY c.id
		  ORDER BY lower(c.name)`, userID)
	if err != nil {
		return nil, fmt.Errorf("library: list collections: %w", err)
	}
	defer rows.Close()

	out := []CollectionSummary{}
	for rows.Next() {
		var c CollectionSummary
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.ModelCount); err != nil {
			return nil, fmt.Errorf("library: list collections: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: list collections: %w", err)
	}
	return out, nil
}

// CreateCollection adds a collection. The description is optional.
func (s *Service) CreateCollection(ctx context.Context, userID int64, name, description string) (CollectionSummary, error) {
	name, err := cleanName(name)
	if err != nil {
		return CollectionSummary{}, err
	}
	description, err = cleanDescription(description)
	if err != nil {
		return CollectionSummary{}, err
	}

	c := CollectionSummary{Name: name, Description: description}
	err = s.db.QueryRow(ctx,
		`INSERT INTO collections (user_id, name, description) VALUES ($1, $2, $3) RETURNING id`,
		userID, name, description,
	).Scan(&c.ID)
	if isDuplicate(err) {
		return CollectionSummary{}, fmt.Errorf("%w: a collection called %q already exists", ErrDuplicate, name)
	}
	if err != nil {
		return CollectionSummary{}, fmt.Errorf("library: create collection: %w", err)
	}
	return c, nil
}

// UpdateCollection renames a collection and sets its description.
//
// Renaming onto an existing name is a duplicate, not a merge, for the reason
// UpdateCategory gives: merging would have to decide what happens to the models
// in both, and that is a bulk operation this milestone does not have.
func (s *Service) UpdateCollection(ctx context.Context, userID, id int64, name, description string) (CollectionSummary, error) {
	name, err := cleanName(name)
	if err != nil {
		return CollectionSummary{}, err
	}
	description, err = cleanDescription(description)
	if err != nil {
		return CollectionSummary{}, err
	}

	c := CollectionSummary{Name: name, Description: description}
	err = s.db.QueryRow(ctx,
		`UPDATE collections SET name = $3, description = $4
		  WHERE id = $1 AND user_id = $2
		RETURNING id, (SELECT count(*) FROM model_collections mc
		               JOIN models m ON m.id = mc.model_id AND m.parent_id IS NULL
		               WHERE mc.collection_id = collections.id)`,
		id, userID, name, description,
	).Scan(&c.ID, &c.ModelCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return CollectionSummary{}, ErrNotFound
	}
	if isDuplicate(err) {
		return CollectionSummary{}, fmt.Errorf("%w: a collection called %q already exists", ErrDuplicate, name)
	}
	if err != nil {
		return CollectionSummary{}, fmt.Errorf("library: update collection: %w", err)
	}
	return c, nil
}

// DeleteCollection removes a collection. Its models stay: the cascade is on
// model_collections, which holds nothing but the pair.
//
// No row locking, unlike DeleteModel. That one has to read blob storage keys
// out of rows before the cascade takes them, so a row it cannot see in its
// snapshot leaves a file on disk forever. Nothing here is read before it is
// deleted and nothing on disk depends on it, so the cascade being complete is
// the whole requirement, and it is complete by definition.
func (s *Service) DeleteCollection(ctx context.Context, userID, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM collections WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("library: delete collection: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AddModelToCollection puts a model in a collection. Adding one that is already
// there succeeds and changes nothing.
//
// One statement, because two answers have to come out of it and RowsAffected
// gives neither: ON CONFLICT DO NOTHING reports zero rows both for "already a
// member" and for "there was nothing to insert", and those are a 204 and a 404.
// So pair is the ownership check and the insert's source, and selecting from it
// afterwards says whether the pair existed at all. A data-modifying CTE runs to
// completion whether or not the outer query reads it, so ins fires regardless.
//
// Both ids are scoped to userID in the one place they are resolved, which is
// what makes another user's model a 404 rather than a 403 - there is no branch
// that could tell the difference and choose to say so.
func (s *Service) AddModelToCollection(ctx context.Context, userID, modelID, collectionID int64) error {
	var ok int
	err := s.db.QueryRow(ctx,
		`WITH pair AS (
		    SELECT m.id AS model_id, c.id AS collection_id
		      FROM models m
		      JOIN collections c ON c.id = $3 AND c.user_id = $1
		     WHERE m.id = $2 AND m.user_id = $1
		 ), ins AS (
		    INSERT INTO model_collections (model_id, collection_id)
		    SELECT model_id, collection_id FROM pair
		    ON CONFLICT DO NOTHING
		 )
		 SELECT 1 FROM pair`,
		userID, modelID, collectionID).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	// The SELECT in pair takes no lock, and the foreign key's key-share lock is
	// taken after it, so a collection or model deleted in that gap - one tab
	// adding while another deletes - is visible to pair and gone by the time
	// the constraint looks. Same race isMissingReference was written for on the
	// model save path. The answer is already known: the thing is not there.
	if isMissingReference(err) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("library: add model to collection: %w", err)
	}
	return nil
}

// RemoveModelFromCollection takes a model out of a collection. The model itself
// is untouched: a collection is a bag, not a container.
//
// Removing a membership that is not there is a 404, like every other delete
// here, so a page showing a stale membership cannot report success for a write
// that did nothing. The join to both owning rows is what makes another user's
// collection indistinguishable from one that does not exist.
func (s *Service) RemoveModelFromCollection(ctx context.Context, userID, modelID, collectionID int64) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM model_collections mc
		  USING models m, collections c
		  WHERE mc.model_id = $2 AND mc.collection_id = $3
		    AND m.id = mc.model_id AND m.user_id = $1
		    AND c.id = mc.collection_id AND c.user_id = $1`,
		userID, modelID, collectionID)
	if err != nil {
		return fmt.Errorf("library: remove model from collection: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// loadCollections reads the collections a model belongs to, for its detail page.
//
// Not filtered to roots: this answers "what is this model in", and a version
// that was put in a collection is in it. The roots-only rule belongs to the
// counts and the grid, which is where it is applied.
//
// Scoped by c.user_id the way loadLabels is. The add CTE is the only writer of
// model_collections and it requires both the model and the collection to be
// the caller's, so a cross-owner row is unreachable - this matches the shape of
// the query next to it rather than defending against a case that can happen.
func (s *Service) loadCollections(ctx context.Context, userID, modelID int64) ([]Label, error) {
	rows, err := s.db.Query(ctx,
		`SELECT c.id, c.name
		   FROM collections c
		   JOIN model_collections mc ON mc.collection_id = c.id
		  WHERE mc.model_id = $1 AND c.user_id = $2
		  ORDER BY lower(c.name)`, modelID, userID)
	if err != nil {
		return nil, fmt.Errorf("library: load collections: %w", err)
	}
	defer rows.Close()

	out := []Label{}
	for rows.Next() {
		var l Label
		if err := rows.Scan(&l.ID, &l.Name); err != nil {
			return nil, fmt.Errorf("library: load collections: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: load collections: %w", err)
	}
	return out, nil
}

// collectionExists reports whether the collection is this user's.
//
// This is what makes a collection filter on the list a 404 rather than an empty
// page, which categoryId and tagId do not do. Two things need it: another
// user's collection has to 404, and there is no other endpoint that could say
// so; and the grid has to tell "this collection is empty" from "there is no
// such collection", because only the first gets an empty state inviting you to
// add models. An empty 200 answers neither.
func (s *Service) collectionExists(ctx context.Context, userID, collectionID int64) error {
	var ok int
	err := s.db.QueryRow(ctx,
		`SELECT 1 FROM collections WHERE id = $1 AND user_id = $2`, collectionID, userID).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("library: collection exists: %w", err)
	}
	return nil
}
