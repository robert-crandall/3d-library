package library

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrDuplicate is returned when a category, tag or material name already exists
// for this user, ignoring case. It always comes from the database's unique
// index rather than a lookup before the insert: two requests can both find
// nothing and both insert, and the index is the only thing that sees both.
var ErrDuplicate = errors.New("library: duplicate name")

// maxNameLen bounds a taxonomy name. The API declares the same number, so this
// is the backstop for a caller that is not going through the schema.
const maxNameLen = 60

// colorPattern is the whole of colour validation: a six-digit hex colour, the
// form every browser and every CSS property accepts. Named colours and rgb()
// would each need their own parser here and their own escape in the template,
// for a field the UI fills from a fixed set of swatches.
var colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Category is a model's category as a model reports it.
type Category struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// CategorySummary is a category in the settings and sidebar lists, where the
// number of models using it decides both the count beside it and the sentence
// in its delete confirmation.
type CategorySummary struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Color      string `json:"color"`
	ModelCount int    `json:"modelCount"`
}

// Label is one tag or one material on a model.
//
// Tags and materials share this type because a name and an id is all either is
// on a model, and the two lists are rendered by the same chip component. They
// do not share their tables, their queries or their routes: those are separate
// vocabularies that happen to look alike, and folding them together would put a
// kind column in every predicate.
type Label struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// LabelSummary is a tag or material in a settings list, with the number of
// models using it.
type LabelSummary struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	ModelCount int    `json:"modelCount"`
}

// Counts is what the sidebar's two fixed rows show. Both are over root models,
// like every other count in this app.
type Counts struct {
	Models        int `json:"models"`
	Uncategorized int `json:"uncategorized"`
}

// Filter narrows the library list. A zero Filter is the whole library's first
// page, newest first.
//
// The fields are ANDed. Uncategorized and CategoryID are mutually exclusive in
// the UI - the sidebar is a single selection - but the server does not need to
// say so: both set at once is simply a query that matches nothing.
type Filter struct {
	CategoryID    *int64
	TagID         *int64
	Uncategorized bool
	// Query is the search term. Empty - or only whitespace - is no search.
	Query string
	// Sort is the ordering. The zero value is SortNewest.
	Sort Sort
	// Page is 1-based. Zero or negative is page 1, and a page past the end is
	// clamped to the last one.
	Page int
}

// cleanName trims and validates a taxonomy name.
func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("%w: a name is required", errInvalid)
	}
	// Runes, not bytes: the limit is a limit on what the user typed, and
	// len() would refuse 21 emoji or 30 CJK characters as "too long".
	if utf8.RuneCountInString(name) > maxNameLen {
		return "", fmt.Errorf("%w: a name may be at most %d characters", errInvalid, maxNameLen)
	}
	return name, nil
}

func cleanColor(color string) (string, error) {
	color = strings.TrimSpace(color)
	if !colorPattern.MatchString(color) {
		return "", fmt.Errorf("%w: a colour must look like #rrggbb", errInvalid)
	}
	return color, nil
}

// isDuplicate reports whether err is Postgres refusing a second row with the
// same name. 23505 is unique_violation; the three tables have one unique index
// each, so there is no need to look at which constraint fired.
func isDuplicate(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isMissingReference reports whether err is a foreign key pointing at a row
// that is no longer there. 23503 is foreign_key_violation.
//
// This is reachable, unlike the same check on the way in. Reading the tag and
// then writing the join row are two steps: the SELECT that sources the insert
// takes no lock, and the key-share lock the foreign key needs is taken after
// it. A tag deleted in the gap - one tab saving a model while another deletes a
// tag - is visible to the SELECT and gone by the time the constraint looks.
// Locking the whole taxonomy for the duration of a save would be a large answer
// to a small question; the right answer is already known, which is that the tag
// is gone and the save cannot have it, so map the error and say so.
func isMissingReference(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// ListCategories returns the user's categories with the number of root models
// in each, ordered by name.
func (s *Service) ListCategories(ctx context.Context, userID int64) ([]CategorySummary, error) {
	rows, err := s.db.Query(ctx,
		`SELECT c.id, c.name, c.color,
		        count(m.id) FILTER (WHERE m.parent_id IS NULL)
		   FROM categories c
		   LEFT JOIN models m ON m.category_id = c.id
		  WHERE c.user_id = $1
		  GROUP BY c.id
		  ORDER BY lower(c.name)`, userID)
	if err != nil {
		return nil, fmt.Errorf("library: list categories: %w", err)
	}
	defer rows.Close()

	out := []CategorySummary{}
	for rows.Next() {
		var c CategorySummary
		if err := rows.Scan(&c.ID, &c.Name, &c.Color, &c.ModelCount); err != nil {
			return nil, fmt.Errorf("library: list categories: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: list categories: %w", err)
	}
	return out, nil
}

// CreateCategory adds a category.
func (s *Service) CreateCategory(ctx context.Context, userID int64, name, color string) (CategorySummary, error) {
	name, err := cleanName(name)
	if err != nil {
		return CategorySummary{}, err
	}
	color, err = cleanColor(color)
	if err != nil {
		return CategorySummary{}, err
	}

	c := CategorySummary{Name: name, Color: color}
	err = s.db.QueryRow(ctx,
		`INSERT INTO categories (user_id, name, color) VALUES ($1, $2, $3) RETURNING id`,
		userID, name, color,
	).Scan(&c.ID)
	if isDuplicate(err) {
		return CategorySummary{}, fmt.Errorf("%w: a category called %q already exists", ErrDuplicate, name)
	}
	if err != nil {
		return CategorySummary{}, fmt.Errorf("library: create category: %w", err)
	}
	return c, nil
}

// UpdateCategory renames a category and sets its colour.
//
// Renaming onto a name that already exists is a duplicate, not a merge. Merging
// two categories would have to decide what happens to the models in both, which
// is a bulk operation this milestone does not have.
func (s *Service) UpdateCategory(ctx context.Context, userID, id int64, name, color string) (CategorySummary, error) {
	name, err := cleanName(name)
	if err != nil {
		return CategorySummary{}, err
	}
	color, err = cleanColor(color)
	if err != nil {
		return CategorySummary{}, err
	}

	c := CategorySummary{Name: name, Color: color}
	err = s.db.QueryRow(ctx,
		`UPDATE categories SET name = $3, color = $4
		  WHERE id = $1 AND user_id = $2
		RETURNING id, (SELECT count(*) FROM models m
		                WHERE m.category_id = categories.id AND m.parent_id IS NULL)`,
		id, userID, name, color,
	).Scan(&c.ID, &c.ModelCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return CategorySummary{}, ErrNotFound
	}
	if isDuplicate(err) {
		return CategorySummary{}, fmt.Errorf("%w: a category called %q already exists", ErrDuplicate, name)
	}
	if err != nil {
		return CategorySummary{}, fmt.Errorf("library: update category: %w", err)
	}
	return c, nil
}

// DeleteCategory removes a category. Its models stay, uncategorized: that is
// the ON DELETE SET NULL on models.category_id, not anything this does.
func (s *Service) DeleteCategory(ctx context.Context, userID, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM categories WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("library: delete category: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListTags returns the user's tags with the number of root models carrying each.
func (s *Service) ListTags(ctx context.Context, userID int64) ([]LabelSummary, error) {
	return s.listLabels(ctx, userID,
		`SELECT t.id, t.name, count(m.id)
		   FROM tags t
		   LEFT JOIN model_tags mt ON mt.tag_id = t.id
		   LEFT JOIN models m ON m.id = mt.model_id AND m.parent_id IS NULL
		  WHERE t.user_id = $1
		  GROUP BY t.id
		  ORDER BY lower(t.name)`, "list tags")
}

// CreateTag adds a tag.
func (s *Service) CreateTag(ctx context.Context, userID int64, name string) (LabelSummary, error) {
	return s.createLabel(ctx, userID, name,
		`INSERT INTO tags (user_id, name) VALUES ($1, $2) RETURNING id`, "tag")
}

// UpdateTag renames a tag.
func (s *Service) UpdateTag(ctx context.Context, userID, id int64, name string) (LabelSummary, error) {
	return s.updateLabel(ctx, userID, id, name,
		`UPDATE tags SET name = $3 WHERE id = $1 AND user_id = $2
		 RETURNING id, (SELECT count(*) FROM model_tags mt
		                  JOIN models m ON m.id = mt.model_id AND m.parent_id IS NULL
		                 WHERE mt.tag_id = tags.id)`, "tag")
}

// DeleteTag removes a tag, and with it every model's use of it.
func (s *Service) DeleteTag(ctx context.Context, userID, id int64) error {
	return s.deleteLabel(ctx, userID, id, `DELETE FROM tags WHERE id = $1 AND user_id = $2`, "tag")
}

// ListMaterials returns the user's materials with the number of root models
// using each.
func (s *Service) ListMaterials(ctx context.Context, userID int64) ([]LabelSummary, error) {
	return s.listLabels(ctx, userID,
		`SELECT mt.id, mt.name, count(m.id)
		   FROM materials mt
		   LEFT JOIN model_materials mm ON mm.material_id = mt.id
		   LEFT JOIN models m ON m.id = mm.model_id AND m.parent_id IS NULL
		  WHERE mt.user_id = $1
		  GROUP BY mt.id
		  ORDER BY lower(mt.name)`, "list materials")
}

// CreateMaterial adds a material.
func (s *Service) CreateMaterial(ctx context.Context, userID int64, name string) (LabelSummary, error) {
	return s.createLabel(ctx, userID, name,
		`INSERT INTO materials (user_id, name) VALUES ($1, $2) RETURNING id`, "material")
}

// UpdateMaterial renames a material.
func (s *Service) UpdateMaterial(ctx context.Context, userID, id int64, name string) (LabelSummary, error) {
	return s.updateLabel(ctx, userID, id, name,
		`UPDATE materials SET name = $3 WHERE id = $1 AND user_id = $2
		 RETURNING id, (SELECT count(*) FROM model_materials mm
		                  JOIN models m ON m.id = mm.model_id AND m.parent_id IS NULL
		                 WHERE mm.material_id = materials.id)`, "material")
}

// DeleteMaterial removes a material. Nothing puts a seeded one back.
func (s *Service) DeleteMaterial(ctx context.Context, userID, id int64) error {
	return s.deleteLabel(ctx, userID, id, `DELETE FROM materials WHERE id = $1 AND user_id = $2`, "material")
}

// The four helpers below hold the scanning and error mapping that tags and
// materials do identically. The SQL itself stays at the call site: it is the
// part that differs, and a table name assembled from a variable is the one thing
// this file must never do.

func (s *Service) listLabels(ctx context.Context, userID int64, query, what string) ([]LabelSummary, error) {
	rows, err := s.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("library: %s: %w", what, err)
	}
	defer rows.Close()

	out := []LabelSummary{}
	for rows.Next() {
		var l LabelSummary
		if err := rows.Scan(&l.ID, &l.Name, &l.ModelCount); err != nil {
			return nil, fmt.Errorf("library: %s: %w", what, err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: %s: %w", what, err)
	}
	return out, nil
}

func (s *Service) createLabel(ctx context.Context, userID int64, name, query, what string) (LabelSummary, error) {
	name, err := cleanName(name)
	if err != nil {
		return LabelSummary{}, err
	}

	l := LabelSummary{Name: name}
	err = s.db.QueryRow(ctx, query, userID, name).Scan(&l.ID)
	if isDuplicate(err) {
		return LabelSummary{}, fmt.Errorf("%w: a %s called %q already exists", ErrDuplicate, what, name)
	}
	if err != nil {
		return LabelSummary{}, fmt.Errorf("library: create %s: %w", what, err)
	}
	return l, nil
}

func (s *Service) updateLabel(ctx context.Context, userID, id int64, name, query, what string) (LabelSummary, error) {
	name, err := cleanName(name)
	if err != nil {
		return LabelSummary{}, err
	}

	l := LabelSummary{Name: name}
	err = s.db.QueryRow(ctx, query, id, userID, name).Scan(&l.ID, &l.ModelCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return LabelSummary{}, ErrNotFound
	}
	if isDuplicate(err) {
		return LabelSummary{}, fmt.Errorf("%w: a %s called %q already exists", ErrDuplicate, what, name)
	}
	if err != nil {
		return LabelSummary{}, fmt.Errorf("library: update %s: %w", what, err)
	}
	return l, nil
}

func (s *Service) deleteLabel(ctx context.Context, userID, id int64, query, what string) error {
	tag, err := s.db.Exec(ctx, query, id, userID)
	if err != nil {
		return fmt.Errorf("library: delete %s: %w", what, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Counts returns the sidebar's two fixed numbers.
//
// One query with a FILTER rather than two round trips, because the two numbers
// are always shown together and a client that saw them disagree would have no
// way to tell which was stale.
func (s *Service) Counts(ctx context.Context, userID int64) (Counts, error) {
	var c Counts
	err := s.db.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE category_id IS NULL)
		   FROM models WHERE user_id = $1 AND parent_id IS NULL`, userID,
	).Scan(&c.Models, &c.Uncategorized)
	if err != nil {
		return Counts{}, fmt.Errorf("library: counts: %w", err)
	}
	return c, nil
}

// loadCategory fills in a model's category, leaving it nil when the model is
// uncategorized.
func (s *Service) loadCategory(ctx context.Context, userID int64, m *ModelDetail, categoryID *int64) error {
	m.Category = nil
	if categoryID == nil {
		return nil
	}
	var c Category
	// user_id is in the statement even though the write path already refuses to
	// point a model at someone else's category. Owner scoping that depends on
	// another query having been correct is not owner scoping, and this is a read
	// that reaches a second table on a user-supplied model id.
	err := s.db.QueryRow(ctx,
		`SELECT id, name, color FROM categories WHERE id = $1 AND user_id = $2`, *categoryID, userID,
	).Scan(&c.ID, &c.Name, &c.Color)
	if errors.Is(err, pgx.ErrNoRows) {
		// The foreign key makes this unreachable; if it ever happens the model
		// reads as uncategorized rather than failing to load at all.
		return nil
	}
	if err != nil {
		return fmt.Errorf("library: load category: %w", err)
	}
	m.Category = &c
	return nil
}

// loadLabels fills in a model's tags and materials. Both are set to empty
// slices first, so a model with none encodes as [] rather than null.
func (s *Service) loadLabels(ctx context.Context, userID int64, m *ModelDetail) error {
	var err error
	if m.Tags, err = s.labelsFor(ctx, userID, m.ID,
		`SELECT t.id, t.name FROM tags t
		   JOIN model_tags mt ON mt.tag_id = t.id
		  WHERE mt.model_id = $1 AND t.user_id = $2 ORDER BY lower(t.name)`, "tags"); err != nil {
		return err
	}
	if m.Materials, err = s.labelsFor(ctx, userID, m.ID,
		`SELECT mt.id, mt.name FROM materials mt
		   JOIN model_materials mm ON mm.material_id = mt.id
		  WHERE mm.model_id = $1 AND mt.user_id = $2 ORDER BY lower(mt.name)`, "materials"); err != nil {
		return err
	}
	return nil
}

func (s *Service) labelsFor(ctx context.Context, userID, modelID int64, query, what string) ([]Label, error) {
	rows, err := s.db.Query(ctx, query, modelID, userID)
	if err != nil {
		return nil, fmt.Errorf("library: load %s: %w", what, err)
	}
	defer rows.Close()

	out := []Label{}
	for rows.Next() {
		var l Label
		if err := rows.Scan(&l.ID, &l.Name); err != nil {
			return nil, fmt.Errorf("library: load %s: %w", what, err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: load %s: %w", what, err)
	}
	return out, nil
}

// resolveListCategories fills in Category for a whole page of models.
//
// One extra query for the grid rather than one per tile, the same shape as
// resolveListThumbnails. It cannot join into the list query, which GROUPs
// models down to one row each: adding the category's columns there would mean
// adding them to the GROUP BY too, and the aggregate is already doing enough.
func (s *Service) resolveListCategories(ctx context.Context, userID int64, models []Model, categoryIDs map[int64]*int64) error {
	wanted := []int64{}
	seen := map[int64]bool{}
	for _, id := range categoryIDs {
		if id != nil && !seen[*id] {
			seen[*id] = true
			wanted = append(wanted, *id)
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	rows, err := s.db.Query(ctx,
		`SELECT id, name, color FROM categories WHERE id = ANY($1) AND user_id = $2`, wanted, userID)
	if err != nil {
		return fmt.Errorf("library: list categories for models: %w", err)
	}
	defer rows.Close()

	byID := map[int64]Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Color); err != nil {
			return fmt.Errorf("library: list categories for models: %w", err)
		}
		byID[c.ID] = c
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("library: list categories for models: %w", err)
	}

	for i := range models {
		id := categoryIDs[models[i].ID]
		if id == nil {
			continue
		}
		if c, ok := byID[*id]; ok {
			models[i].Category = &c
		}
	}
	return nil
}

// setCategory points a model at a category the caller owns, or at nothing.
//
// The ownership check is a separate SELECT rather than a subquery in the
// UPDATE, because `SET category_id = (SELECT ... WHERE user_id = $n)` would
// write NULL for another user's id: the model would quietly become
// uncategorized instead of the request being refused.
func setCategory(ctx context.Context, tx pgx.Tx, userID, modelID int64, categoryID *int64) error {
	if categoryID != nil {
		var exists int
		err := tx.QueryRow(ctx,
			`SELECT 1 FROM categories WHERE id = $1 AND user_id = $2`, *categoryID, userID,
		).Scan(&exists)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: unknown category", errInvalid)
		}
		if err != nil {
			return fmt.Errorf("library: check category: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE models SET category_id = $2 WHERE id = $1`, modelID, categoryID); err != nil {
		if isMissingReference(err) {
			return fmt.Errorf("%w: unknown category", errInvalid)
		}
		return fmt.Errorf("library: set category: %w", err)
	}
	return nil
}

// replaceTags makes the model's tags exactly ids.
//
// Delete then insert, because this is a replacement: without the delete,
// re-saving a model with the tags it already has would violate model_tags'
// primary key.
//
// The insert filters on user_id and then checks how many rows it wrote, which
// is what stops one user attaching another's tag. Selecting the ids back first
// would be the same two queries with a race in between.
func replaceTags(ctx context.Context, tx pgx.Tx, userID, modelID int64, ids []int64) error {
	return replaceLabels(ctx, tx, userID, modelID, ids,
		`DELETE FROM model_tags WHERE model_id = $1`,
		`INSERT INTO model_tags (model_id, tag_id)
		 SELECT $1, t.id FROM tags t WHERE t.id = ANY($2) AND t.user_id = $3`,
		"tag")
}

// replaceMaterials makes the model's materials exactly ids.
func replaceMaterials(ctx context.Context, tx pgx.Tx, userID, modelID int64, ids []int64) error {
	return replaceLabels(ctx, tx, userID, modelID, ids,
		`DELETE FROM model_materials WHERE model_id = $1`,
		`INSERT INTO model_materials (model_id, material_id)
		 SELECT $1, m.id FROM materials m WHERE m.id = ANY($2) AND m.user_id = $3`,
		"material")
}

func replaceLabels(ctx context.Context, tx pgx.Tx, userID, modelID int64, ids []int64, del, ins, what string) error {
	if _, err := tx.Exec(ctx, del, modelID); err != nil {
		return fmt.Errorf("library: clear %ss: %w", what, err)
	}

	// Deduplicate, so a body naming the same tag twice is one row rather than a
	// primary key violation, and so the count below compares like with like.
	unique := make([]int64, 0, len(ids))
	seen := map[int64]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	if len(unique) == 0 {
		return nil
	}

	tag, err := tx.Exec(ctx, ins, modelID, unique, userID)
	if isMissingReference(err) {
		return fmt.Errorf("%w: unknown %s", errInvalid, what)
	}
	if err != nil {
		return fmt.Errorf("library: set %ss: %w", what, err)
	}
	if int(tag.RowsAffected()) != len(unique) {
		return fmt.Errorf("%w: unknown %s", errInvalid, what)
	}
	return nil
}
