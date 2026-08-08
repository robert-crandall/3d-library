// Package library is this app's model store: uploading a set of files as one
// named model, and listing or fetching those models back.
//
// It replaces the foundation's generic file service rather than wrapping it.
// That service stores loose blobs keyed by id; this one stores *models* that
// own their files, which is the whole domain, so there is nothing left for the
// generic version to do and both would compete for UPLOAD_DIR.
package library

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/robert-crandall/3d-library/internal/gcode"
	"github.com/robert-crandall/3d-library/internal/thumb"
)

const (
	// MaxFileBytes is the per-file cap. 500 MB, as the epic settled.
	MaxFileBytes int64 = 500 << 20

	// MaxFiles is the number of files one model may hold. The UI queues at most
	// this many uploads; the server re-checks because the UI is not a guard.
	MaxFiles = 20

	// UploadTimeout replaces the server's own read and write deadlines for the
	// duration of an upload. It is generous on purpose: the failure modes are
	// asymmetric, since too short kills a legitimate upload while too long
	// parks one goroutine on a single-user box.
	UploadTimeout = 60 * time.Minute

	// tmpPrefix marks a blob that is still being written. A partial upload is
	// only ever visible under this prefix, never as a real file, which is what
	// lets a reader treat every other name in the directory as complete.
	tmpPrefix = ".tmp-"

	// thumbSuffix names a thumbnail beside the blob it belongs to. A suffix on
	// the existing key rather than a parallel directory: it keeps a file and
	// its thumbnail together for a backup or an rsync, and the storage key is
	// already random and collision-free so the derived name is too. It does
	// mean anything walking UPLOAD_DIR has to skip these, which the delete
	// path, the tests and the docker smoke script all do.
	thumbSuffix = ".thumb"
)

// ErrNotFound is returned when a model does not exist *or* belongs to another
// user. The two are deliberately indistinguishable: a 403 would confirm that
// somebody else's model exists at that id, so callers turn this into a 404.
var ErrNotFound = errors.New("library: not found")

// ErrTooLarge is returned when a single file exceeds the per-file cap.
var ErrTooLarge = errors.New("library: file too large")

// Service stores models and their files.
type Service struct {
	db  *pgxpool.Pool
	dir string

	// The caps are fields rather than constants so tests can shrink them and
	// exercise the real rejection paths with a few KB instead of gigabytes.
	// Production always gets the constants above via NewService.
	maxFileBytes int64
	maxFiles     int
	// maxBodyBytes bounds a whole upload request. It is the per-file cap plus
	// slop for the multipart framing, not a multiple of it: one request carries
	// one file.
	maxBodyBytes int64
}

// Options configures a Service. Every field except Dir is optional; a zero
// value takes the production default. The caps are configurable only so tests
// can exercise the real rejection paths with a few kilobytes instead of
// gigabytes - nothing in cmd/server sets them.
type Options struct {
	// Dir is the directory blobs are written to. Required.
	Dir string
	// MaxFileBytes caps a single file. 0 means MaxFileBytes.
	MaxFileBytes int64
	// MaxFiles caps how many files one model may hold. 0 means MaxFiles.
	MaxFiles int
}

// NewService checks that the directory exists and is writable, because an
// upload directory that turns out to be unusable at the first upload is a worse
// failure than one that stops the process from starting.
func NewService(pool *pgxpool.Pool, opts Options) (*Service, error) {
	dir := opts.Dir
	if dir == "" {
		return nil, errors.New("library: upload dir is required")
	}
	perFile := opts.MaxFileBytes
	if perFile <= 0 {
		perFile = MaxFileBytes
	}
	count := opts.MaxFiles
	if count <= 0 {
		count = MaxFiles
	}

	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("library: upload dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("library: upload dir %q is not a directory", dir)
	}
	probe, err := os.CreateTemp(dir, tmpPrefix+"probe-")
	if err != nil {
		return nil, fmt.Errorf("library: upload dir is not writable: %w", err)
	}
	probeName := probe.Name()
	probe.Close()
	os.Remove(probeName)

	return &Service{
		db:           pool,
		dir:          dir,
		maxFileBytes: perFile,
		maxFiles:     count,
		maxBodyBytes: maxBodyBytes(perFile),
	}, nil
}

// maxBodyBytes bounds a whole upload request. The per-file cap bounds a
// *well-formed* request, but mime/multipart skips arbitrarily many preamble
// lines before it yields the first part, so without this a client could stream
// forever without that check ever engaging. The slop covers the boundaries and
// part headers around one file.
func maxBodyBytes(perFile int64) int64 {
	return perFile + 1<<20
}

// Model is a library entry as the grid sees it. FileCount and TotalSize are
// derived on read rather than stored: they are one aggregate over an indexed
// foreign key at single-user scale, and a stored counter would be a cache to
// invalidate.
type Model struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	FileCount int       `json:"fileCount"`
	TotalSize int64     `json:"totalSize"`
	CreatedAt time.Time `json:"createdAt"`

	// ThumbnailFileID is the file whose thumbnail the tile should show, or nil
	// for the placeholder. Resolved the same way as on the detail response.
	ThumbnailFileID *int64 `json:"thumbnailFileId,omitempty"`

	// Category is the model's category, or nil when it has none. The grid tile
	// shows it as a coloured dot beside the name, so it belongs on the grid
	// response and not only on the detail one.
	Category *Category `json:"category,omitempty"`
}

// ModelDetail is one model with the editable metadata and the files it owns.
//
// It is a separate type from Model rather than Model with optional fields,
// because the two responses genuinely differ. Files cannot live on Model with
// `omitempty`: an empty slice would then be omitted entirely, so a model whose
// last file was just deleted would arrive with no files key at all and the
// detail page would have to guess. Without `omitempty` the grid's response
// would carry `"files": null` on every entry instead. Splitting the type says
// the true thing in both places, and keeps up to 12 KB of description and print
// tips per model off a grid that renders neither.
//
// Files is always non-nil, so an empty model sends `[]`. `nullable:"false"`
// makes the schema say so: huma types a Go slice as array-or-null by default,
// which would make every client null-check a field that cannot be null.
type ModelDetail struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	FileCount   int       `json:"fileCount"`
	TotalSize   int64     `json:"totalSize"`
	CreatedAt   time.Time `json:"createdAt"`
	Description string    `json:"description"`
	PrintTips   string    `json:"printTips"`
	SourceURL   string    `json:"sourceUrl"`
	Files       []File    `json:"files" nullable:"false"`

	// ThumbnailFileID is the file whose thumbnail represents this model, or nil
	// if none of its files has one. It is resolved: the pin if the user set
	// one, otherwise whatever the automatic rule picked.
	ThumbnailFileID *int64 `json:"thumbnailFileId,omitempty"`

	// ThumbnailAutomatic says the resolved choice came from the rule rather
	// than the user, so the detail screen knows whether to offer "back to
	// automatic". Without it a pinned file and an automatically chosen file are
	// indistinguishable in the response and the button has nothing to key on.
	ThumbnailAutomatic bool `json:"thumbnailAutomatic"`

	// Category is nil when the model is uncategorized, which the detail page
	// renders as "Uncategorized" rather than as blank.
	Category *Category `json:"category,omitempty"`

	// Tags and Materials are always non-nil, for the same reason Files is.
	Tags      []Label `json:"tags" nullable:"false"`
	Materials []Label `json:"materials" nullable:"false"`
}

// File is one uploaded file belonging to a model.
type File struct {
	ID          int64     `json:"id"`
	Filename    string    `json:"filename"`
	Type        string    `json:"type"`
	ContentType string    `json:"contentType"`
	Size        int64     `json:"size"`
	CreatedAt   time.Time `json:"createdAt"`

	// ExtractedMeta is what the slicer said about the print, for a G-code file
	// we could attribute to one. It is nil for every other file, and for a
	// G-code file whose slicer we do not recognise.
	//
	// Derived, never edited. Re-slicing produces a new file, so there is no
	// path by which this and the bytes on disk can disagree, and no need for
	// the API to accept a value for it.
	ExtractedMeta *gcode.Meta `json:"extractedMeta,omitempty"`

	// HasThumbnail says a PNG was extracted for this file and sits beside its
	// blob. The client needs it to know which rows can be pinned and which
	// rows can show a preview; the server needs it to pick the model's
	// thumbnail without reading the filesystem.
	HasThumbnail bool `json:"hasThumbnail"`
}

// staged is a file written to disk but not yet committed to the database.
type staged struct {
	tmpPath string
	key     string
	name    string
	typ     string
	ctype   string
	size    int64

	// meta and metaJSON are the same value twice: the struct goes back to the
	// client in the upload response, the bytes go to Postgres. Encoding once at
	// staging time keeps the failure - if there could ever be one - away from
	// the transaction. json.RawMessage rather than []byte so pgx encodes it as
	// jsonb rather than guessing at bytea.
	meta     *gcode.Meta
	metaJSON json.RawMessage

	// thumb is the extracted PNG, held in memory until the blob has been
	// renamed into place. It is not written during staging: stageOne works
	// under .tmp-<key>, and a sidecar written there under its final name would
	// survive an upload that fails afterwards, leaving a thumbnail beside no
	// blob. publish writes it, in the same step that makes the blob real.
	thumb []byte
}

// Create stores one file and the model that owns it, in that order.
//
// A model is never created without a file. One request carries one file (the
// epic settled that: twenty 500 MB files in a single body would be a 10 GB
// request), so a client uploading several calls this once and then AddFile for
// each of the rest. Making the *first* file create the model is what keeps a
// failed first upload from leaving an empty model in the grid - there is no
// delete in this milestone to clean one up with.
//
// The ordering within the request is: stream the part to dir/.tmp-<key>, rename
// it into place, then insert. Renaming before inserting means a row never
// points at a missing or partial blob, which is the direction that matters -
// the reverse leaves the library showing an entry whose file is not there. The
// transaction is opened only once every byte is on disk, so a slow upload never
// holds a connection.
func (s *Service) Create(ctx context.Context, userID int64, name string, parts *multipart.Reader) (ModelDetail, error) {
	file, err := s.stageOnly(parts)
	if err != nil {
		removeOne(file)
		return ModelDetail{}, err
	}
	if err := s.publish(&file); err != nil {
		removeOne(file)
		return ModelDetail{}, err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		// The commit was never attempted, so the blob is unreferenced and
		// removing it is safe.
		removeOne(file)
		return ModelDetail{}, fmt.Errorf("library: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var m ModelDetail
	m.Name = name
	err = tx.QueryRow(ctx,
		`INSERT INTO models (user_id, name) VALUES ($1, $2) RETURNING id, created_at`,
		userID, name,
	).Scan(&m.ID, &m.CreatedAt)
	if err != nil {
		removeOne(file)
		return ModelDetail{}, fmt.Errorf("library: insert model: %w", err)
	}

	out, err := insertFile(ctx, tx, m.ID, file)
	if err != nil {
		removeOne(file)
		return ModelDetail{}, err
	}

	// Past this point the file is never removed. A commit can succeed on the
	// server and still return a network error, and deleting the blob of a row
	// that does exist would manufacture exactly the dangling entry this whole
	// ordering prevents. An unreferenced blob is the better end of that trade -
	// it costs disk, not correctness.
	if err := tx.Commit(ctx); err != nil {
		return ModelDetail{}, fmt.Errorf("library: commit: %w", err)
	}

	m.Files = []File{out}
	m.FileCount = 1
	m.TotalSize = out.Size
	// A new model has neither, and both are non-nil in the contract, so they
	// are set here rather than left as the zero value - `nullable:"false"`
	// describes the schema, not what Go encodes a nil slice as.
	m.Tags = []Label{}
	m.Materials = []Label{}
	// Nothing can be pinned yet - the model was created a few lines ago - so
	// this is the automatic rule over exactly one file.
	resolveThumbnail(&m, nil)
	return m, nil
}

// AddFile appends one more file to a model the caller already owns.
func (s *Service) AddFile(ctx context.Context, userID, modelID int64, parts *multipart.Reader) (File, error) {
	// Check ownership before reading the body, so somebody else's model does
	// not get to consume disk on the way to a 404.
	var count int
	err := s.db.QueryRow(ctx,
		`SELECT count(f.id) FROM models m
		   LEFT JOIN model_files f ON f.model_id = m.id
		  WHERE m.id = $1 AND m.user_id = $2
		  GROUP BY m.id`, modelID, userID).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		return File{}, ErrNotFound
	}
	if err != nil {
		return File{}, fmt.Errorf("library: check model: %w", err)
	}
	if count >= s.maxFiles {
		return File{}, fmt.Errorf("%w: a model may hold at most %d files", errInvalid, s.maxFiles)
	}

	file, err := s.stageOnly(parts)
	if err != nil {
		removeOne(file)
		return File{}, err
	}
	if err := s.publish(&file); err != nil {
		removeOne(file)
		return File{}, err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		removeOne(file)
		return File{}, fmt.Errorf("library: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// Re-check ownership inside the transaction, and count under the same lock.
	// The count above is advisory - it ran on another connection, before the
	// body was even read - so two uploads that both saw 19 files would both
	// pass it. The row lock serialises them here, which is what makes this
	// count authoritative and the cap actually a cap.
	var owned int64
	err = tx.QueryRow(ctx,
		`SELECT id FROM models WHERE id = $1 AND user_id = $2 FOR UPDATE`,
		modelID, userID).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) {
		removeOne(file)
		return File{}, ErrNotFound
	}
	if err != nil {
		removeOne(file)
		return File{}, fmt.Errorf("library: check model: %w", err)
	}

	// Counted separately rather than folded into the statement above, because
	// Postgres refuses FOR UPDATE alongside an aggregate.
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM model_files WHERE model_id = $1`, modelID).Scan(&count); err != nil {
		removeOne(file)
		return File{}, fmt.Errorf("library: count files: %w", err)
	}
	if count >= s.maxFiles {
		removeOne(file)
		return File{}, fmt.Errorf("%w: a model may hold at most %d files", errInvalid, s.maxFiles)
	}

	out, err := insertFile(ctx, tx, modelID, file)
	if err != nil {
		removeOne(file)
		return File{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return File{}, fmt.Errorf("library: commit: %w", err)
	}
	return out, nil
}

func insertFile(ctx context.Context, tx pgx.Tx, modelID int64, f staged) (File, error) {
	var out File
	// hasThumbnail is derived from the staged bytes rather than passed in
	// separately, so the row cannot claim a sidecar publish decided not to
	// write: publish clears the field when the write fails.
	hasThumb := len(f.thumb) > 0
	// A nil metaJSON is a SQL NULL, which is what every non-G-code file and
	// every unrecognised slicer stores.
	err := tx.QueryRow(ctx,
		`INSERT INTO model_files (model_id, storage_key, filename, type, content_type, size_bytes, extracted_meta, has_thumbnail)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id, created_at`,
		modelID, f.key, f.name, f.typ, f.ctype, f.size, f.metaJSON, hasThumb,
	).Scan(&out.ID, &out.CreatedAt)
	if err != nil {
		return File{}, fmt.Errorf("library: insert file: %w", err)
	}
	out.Filename, out.Type, out.ContentType, out.Size = f.name, f.typ, f.ctype, f.size
	out.ExtractedMeta = f.meta
	out.HasThumbnail = hasThumb
	return out, nil
}

// publish moves a staged blob to its final name, after which it is a real file
// that a reader may assume is complete, and writes its thumbnail sidecar.
//
// The sidecar goes here rather than in stageOne because stageOne is working on
// a temp file: writing <key>.thumb before the rename would leave a thumbnail
// behind for an upload that never completed. Writing it after the rename means
// the worst case is the reverse, a blob whose thumbnail is missing, and that
// one is recoverable - hasThumbnail is set from the sidecar below, so the row
// simply says it has none.
func (s *Service) publish(f *staged) error {
	final := filepath.Join(s.dir, f.key)
	if err := os.Rename(f.tmpPath, final); err != nil {
		return fmt.Errorf("library: rename: %w", err)
	}
	f.tmpPath = final

	if len(f.thumb) > 0 && !writeSidecar(final+thumbSuffix, f.thumb) {
		// A thumbnail is a nicety and the bytes are still on disk to re-derive
		// from. Dropping it keeps the promise that extraction can never fail an
		// upload, and clearing the field is what makes the row agree with the
		// filesystem.
		f.thumb = nil
	}
	return nil
}

// writeSidecar writes a thumbnail beside its blob, removing a partial file if
// anything goes wrong. A half-written PNG that stayed on disk would be served
// to the browser as a broken image forever, which is worse than no thumbnail.
func writeSidecar(path string, b []byte) bool {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		slog.Warn("library: create thumbnail", "error", err)
		return false
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(path)
		slog.Warn("library: write thumbnail", "error", err)
		return false
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		slog.Warn("library: close thumbnail", "error", err)
		return false
	}
	return true
}

// stageOnly reads exactly one file part and refuses anything else. A second
// part is rejected before its body is read, so a client cannot smuggle a batch
// past the per-request size cap by splitting it across parts.
func (s *Service) stageOnly(parts *multipart.Reader) (staged, error) {
	var out staged
	for {
		part, err := parts.NextPart()
		if errors.Is(err, io.EOF) {
			if out.tmpPath == "" {
				return out, fmt.Errorf("%w: an upload needs exactly one file", errInvalid)
			}
			return out, nil
		}
		if err != nil {
			// The client's body is malformed. A MaxBytesError arrives here too
			// and is mapped to 413 first, since it is a size problem rather
			// than a syntax one.
			return out, fmt.Errorf("%w: %w", errInvalid, err)
		}

		// Only a file part named "file" counts. Checking the filename as well
		// as the field name matters: a text field carries the same field name
		// with no filename, and accepting it would store a bogus empty file.
		if part.FormName() != "file" || part.FileName() == "" {
			part.Close()
			return out, fmt.Errorf("%w: expected one file part named %q", errInvalid, "file")
		}
		if out.tmpPath != "" {
			part.Close()
			return out, fmt.Errorf("%w: an upload carries exactly one file", errInvalid)
		}

		st, err := s.stageOne(part)
		part.Close()
		if st.tmpPath != "" {
			out = st
		}
		if err != nil {
			return out, err
		}
	}
}

// stageOne streams one part into its own temp file under tmpPrefix. A partial
// upload is only ever visible under that prefix, never as a real file, which is
// what lets a reader treat every other name in the directory as complete.
func (s *Service) stageOne(part *multipart.Part) (staged, error) {
	name := displayName(part.FileName())
	key, err := storageKey(name)
	if err != nil {
		return staged{}, err
	}

	tmpPath := filepath.Join(s.dir, tmpPrefix+key)
	// O_RDWR rather than O_WRONLY so the sniff below can read the head back
	// without reopening the file.
	f, err := os.OpenFile(tmpPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return staged{}, fmt.Errorf("library: create: %w", err)
	}
	st := staged{tmpPath: tmpPath, key: key, name: name, typ: fileType(name)}

	// Reading one byte past the cap is what distinguishes "exactly at the cap"
	// from "over it" without buffering anything.
	size, err := io.Copy(f, io.LimitReader(part, s.maxFileBytes+1))

	// Sniff before closing. http.DetectContentType looks at the first 512
	// bytes, and ReadAt takes an absolute offset so the write position does not
	// matter. A short read is fine: DetectContentType is defined over whatever
	// it is given.
	var head [512]byte
	n, readErr := f.ReadAt(head[:], 0)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		f.Close()
		return st, fmt.Errorf("library: sniff: %w", readErr)
	}

	// Read the slice settings out of the same open handle, for the same reason
	// the sniff does: ReadAt is absolute, so the write offset is irrelevant and
	// nothing has to be reopened.
	//
	// The guards matter more than the call. A file that failed to write, or one
	// that is about to be rejected for size, is not worth parsing - and `size`
	// is the local variable rather than st.size, which is still zero here. The
	// parse itself reads 144 KB at most however big the file is, and cannot
	// fail: gcode.Parse reports "nothing found" rather than an error, so a G-code
	// file this app does not understand still uploads.
	if st.typ == "gcode" && err == nil && size <= s.maxFileBytes {
		if meta, ok := gcode.Parse(f, size); ok {
			// Marshal here, not at insert time. A Meta that will not encode
			// must degrade to no metadata, never to a failed upload, and that
			// decision belongs where there is still somewhere to put it.
			if encoded, mErr := json.Marshal(meta); mErr == nil {
				st.meta, st.metaJSON = &meta, encoded
			} else {
				slog.Warn("library: encode slice settings", "file", name, "error", mErr)
			}
		}
	}

	// And the thumbnail, out of the same handle and under the same guards, for
	// the same reasons. thumb.Extract cannot fail either - it reports "nothing
	// found" rather than an error - so a file this app cannot make a picture of
	// still uploads. The bytes are held on st rather than written here; see
	// publish for why.
	if err == nil && size <= s.maxFileBytes {
		if png, ok := thumb.Extract(f, size, st.typ); ok {
			st.thumb = png
		}
	}

	closeErr := f.Close()
	if err != nil {
		return st, fmt.Errorf("library: write: %w", err)
	}
	if closeErr != nil {
		return st, fmt.Errorf("library: write: %w", closeErr)
	}
	if size > s.maxFileBytes {
		return st, fmt.Errorf("%w: %q exceeds the %d MB limit", ErrTooLarge, name, s.maxFileBytes>>20)
	}

	st.size = size
	// The stored content type is a statement about the bytes, never about what
	// the client claimed. The multipart part header is attacker-controlled, so
	// trusting it - even only as a fallback when the sniff comes back generic -
	// would let arbitrary bytes be stored as image/png, which is precisely the
	// claim the thumbnail milestone must not believe. The *domain* type is not
	// lost by this: fileType() derives it from the extension and is
	// authoritative, so a binary STL sniffing as application/octet-stream keeps
	// type="stl" either way.
	st.ctype = http.DetectContentType(head[:n])
	return st, nil
}

// List returns the user's root models, newest first. Versions - models with a
// parent - are excluded here rather than filtered by the caller, because every
// listing in this app is over roots.
//
// The filter's clauses are appended to one statement rather than composed from
// separate queries, so milestone 8 adds LIMIT/OFFSET and a search predicate to
// the same place. Tag membership is an EXISTS rather than a join, which keeps
// the aggregate below counting files and not file-tag pairs.
func (s *Service) List(ctx context.Context, userID int64, f Filter) ([]Model, error) {
	args := []any{userID}
	where := "m.user_id = $1 AND m.parent_id IS NULL"
	if f.CategoryID != nil {
		args = append(args, *f.CategoryID)
		where += fmt.Sprintf(" AND m.category_id = $%d", len(args))
	}
	if f.Uncategorized {
		where += " AND m.category_id IS NULL"
	}
	if f.TagID != nil {
		args = append(args, *f.TagID)
		where += fmt.Sprintf(
			" AND EXISTS (SELECT 1 FROM model_tags mt WHERE mt.model_id = m.id AND mt.tag_id = $%d)", len(args))
	}

	rows, err := s.db.Query(ctx,
		`SELECT m.id, m.name, m.created_at,
		        count(f.id), coalesce(sum(f.size_bytes), 0), m.thumbnail_file_id, m.category_id
		   FROM models m
		   LEFT JOIN model_files f ON f.model_id = m.id
		  WHERE `+where+`
		  GROUP BY m.id
		  ORDER BY m.created_at DESC, m.id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("library: list: %w", err)
	}
	defer rows.Close()

	// Non-nil so an empty library encodes as [] rather than null, which the
	// frontend would otherwise have to guard against.
	models := []Model{}
	pins := map[int64]*int64{}
	categories := map[int64]*int64{}
	for rows.Next() {
		var m Model
		var pinned, categoryID *int64
		if err := rows.Scan(&m.ID, &m.Name, &m.CreatedAt, &m.FileCount, &m.TotalSize, &pinned, &categoryID); err != nil {
			return nil, fmt.Errorf("library: list: %w", err)
		}
		pins[m.ID] = pinned
		categories[m.ID] = categoryID
		models = append(models, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: list: %w", err)
	}
	if err := s.resolveListThumbnails(ctx, models, pins); err != nil {
		return nil, err
	}
	if err := s.resolveListCategories(ctx, userID, models, categories); err != nil {
		return nil, err
	}
	return models, nil
}

// resolveListThumbnails fills in ThumbnailFileID for a whole page of models.
//
// One extra query for the grid rather than one per tile. It cannot fold into
// the aggregate above, because that query GROUPs models down to one row each
// and the thumbnail is a property of a particular file within the group.
//
// The rows it needs are the thumbnail-bearing ones, so the WHERE clause filters
// on has_thumbnail - and then selects it anyway. That looks redundant and is
// not: pickThumbnail reads the field, and synthesising true from the clause
// would work until somebody widened the query, at which point every tile in the
// library would quietly show the wrong picture.
func (s *Service) resolveListThumbnails(ctx context.Context, models []Model, pins map[int64]*int64) error {
	if len(models) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}

	rows, err := s.db.Query(ctx,
		`SELECT model_id, id, type, has_thumbnail
		   FROM model_files
		  WHERE model_id = ANY($1) AND has_thumbnail
		  ORDER BY model_id, id`, ids)
	if err != nil {
		return fmt.Errorf("library: list thumbnails: %w", err)
	}
	defer rows.Close()

	byModel := map[int64][]File{}
	for rows.Next() {
		var modelID int64
		var f File
		if err := rows.Scan(&modelID, &f.ID, &f.Type, &f.HasThumbnail); err != nil {
			return fmt.Errorf("library: list thumbnails: %w", err)
		}
		byModel[modelID] = append(byModel[modelID], f)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("library: list thumbnails: %w", err)
	}

	for i := range models {
		// Reuse the detail path's resolver rather than repeating the rule, so
		// the grid and the detail screen cannot disagree about which file is
		// the model's picture.
		d := ModelDetail{Files: byModel[models[i].ID]}
		resolveThumbnail(&d, pins[models[i].ID])
		models[i].ThumbnailFileID = d.ThumbnailFileID
	}
	return nil
}

// Get returns one model with its files, or ErrNotFound if it does not exist or
// belongs to somebody else.
func (s *Service) Get(ctx context.Context, userID, id int64) (ModelDetail, error) {
	var m ModelDetail
	var pinned, categoryID *int64
	err := s.db.QueryRow(ctx,
		`SELECT id, name, created_at, description, print_tips, source_url, thumbnail_file_id, category_id
		   FROM models WHERE id = $1 AND user_id = $2`,
		id, userID,
	).Scan(&m.ID, &m.Name, &m.CreatedAt, &m.Description, &m.PrintTips, &m.SourceURL, &pinned, &categoryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelDetail{}, ErrNotFound
	}
	if err != nil {
		return ModelDetail{}, fmt.Errorf("library: get: %w", err)
	}

	if err := s.loadFiles(ctx, &m); err != nil {
		return ModelDetail{}, err
	}
	if err := s.loadCategory(ctx, userID, &m, categoryID); err != nil {
		return ModelDetail{}, err
	}
	if err := s.loadLabels(ctx, userID, &m); err != nil {
		return ModelDetail{}, err
	}
	resolveThumbnail(&m, pinned)
	return m, nil
}

// loadFiles fills in Files and the two derived totals. Files is set to an empty
// slice first, so a model with none encodes as [] rather than null - a model
// with no files is a legal state once a file can be deleted, and the detail page
// has to render it rather than guess at a missing key.
func (s *Service) loadFiles(ctx context.Context, m *ModelDetail) error {
	rows, err := s.db.Query(ctx,
		`SELECT id, filename, type, content_type, size_bytes, created_at, extracted_meta, has_thumbnail
		   FROM model_files WHERE model_id = $1 ORDER BY id`, m.ID)
	if err != nil {
		return fmt.Errorf("library: get files: %w", err)
	}
	defer rows.Close()

	m.Files = []File{}
	m.TotalSize = 0
	for rows.Next() {
		var f File
		var meta []byte
		if err := rows.Scan(&f.ID, &f.Filename, &f.Type, &f.ContentType, &f.Size, &f.CreatedAt, &meta, &f.HasThumbnail); err != nil {
			return fmt.Errorf("library: get files: %w", err)
		}
		// Unreadable stored metadata is not a broken model. The rest of the
		// detail page is fine without it, so the file loses its settings panel
		// and the request still succeeds.
		if len(meta) > 0 {
			var parsed gcode.Meta
			if err := json.Unmarshal(meta, &parsed); err != nil {
				slog.WarnContext(ctx, "library: decode slice settings", "file", f.ID, "error", err)
			} else {
				f.ExtractedMeta = &parsed
			}
		}
		m.Files = append(m.Files, f)
		m.TotalSize += f.Size
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("library: get files: %w", err)
	}
	m.FileCount = len(m.Files)
	return nil
}

// Edits carries the model metadata the detail screen may change. Every field is
// replaced on every call: this is the whole editable surface, submitted as one
// form, so there is no partial update for optional fields to express.
//
// CategoryID is nil for "uncategorized", and TagIDs and MaterialIDs are the
// complete membership afterwards, not additions. That is what lets one request
// express setting a category, adding a tag and removing a material at once.
type Edits struct {
	Name        string
	Description string
	PrintTips   string
	SourceURL   string
	CategoryID  *int64
	TagIDs      []int64
	MaterialIDs []int64
}

// Update replaces a model's editable metadata and returns the saved result.
//
// Validation runs before the UPDATE, so a rejected edit changes nothing - the
// acceptance criterion is "does not change the record", not "shows no change".
// The three writes share one transaction for the same reason: naming a tag that
// is not yours must leave the name and description alone too, not half-apply.
func (s *Service) Update(ctx context.Context, userID, id int64, e Edits) (ModelDetail, error) {
	// Trim first, so a name of "   " is empty rather than a 3-character name.
	e.Name = strings.TrimSpace(e.Name)
	e.Description = strings.TrimSpace(e.Description)
	e.PrintTips = strings.TrimSpace(e.PrintTips)
	e.SourceURL = strings.TrimSpace(e.SourceURL)

	if e.Name == "" {
		return ModelDetail{}, fmt.Errorf("%w: a model needs a name", errInvalid)
	}
	if err := validSourceURL(e.SourceURL); err != nil {
		return ModelDetail{}, err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return ModelDetail{}, fmt.Errorf("library: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var updated int64
	err = tx.QueryRow(ctx,
		`UPDATE models SET name = $3, description = $4, print_tips = $5, source_url = $6
		  WHERE id = $1 AND user_id = $2
		RETURNING id`,
		id, userID, e.Name, e.Description, e.PrintTips, e.SourceURL,
	).Scan(&updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return ModelDetail{}, ErrNotFound
	}
	if err != nil {
		return ModelDetail{}, fmt.Errorf("library: update: %w", err)
	}

	if err := setCategory(ctx, tx, userID, id, e.CategoryID); err != nil {
		return ModelDetail{}, err
	}
	if err := replaceTags(ctx, tx, userID, id, e.TagIDs); err != nil {
		return ModelDetail{}, err
	}
	if err := replaceMaterials(ctx, tx, userID, id, e.MaterialIDs); err != nil {
		return ModelDetail{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return ModelDetail{}, fmt.Errorf("library: commit: %w", err)
	}

	// Read the saved row back rather than assembling it from the edits. The
	// category and the labels are joins now, so building the response by hand
	// would be a second copy of the loaders that could disagree with them.
	return s.Get(ctx, userID, id)
}

// validSourceURL rejects anything that is not an ordinary web link.
//
// This is a real hole, not a hypothetical one: the SPA renders the value as
// <a href={sourceUrl}>, and "javascript:alert(1)" in that attribute is script
// execution on the app's own origin. The API is the guard, because the API is
// the only thing every client goes through. A host is required as well as a
// scheme, since "https:garbage" parses with scheme https and is not a link.
func validSourceURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: source URL is not a URL", errInvalid)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: source URL must be an http:// or https:// address", errInvalid)
	}
	return nil
}

// DeleteModel removes a model, its file rows, and then its blobs.
//
// Rows before blobs, and every row in one transaction: the worst outcome of a
// crash in the middle is a blob nobody references, which costs disk, and never
// a row pointing at a file that is not there, which is the failure the library
// exists to prevent.
func (s *Service) DeleteModel(ctx context.Context, userID, id int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("library: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// FOR UPDATE is the same row lock AddFile takes, and it is what makes the
	// key list below complete. Without it, an AddFile that commits between the
	// two DELETEs has its brand-new row swept away by the models delete (via ON
	// DELETE CASCADE) with nobody holding its storage key, orphaning that blob.
	// Taking the lock first means this waits for that upload and then sees its
	// row. It costs one clause on a query the ownership check needs anyway.
	var owned int64
	err = tx.QueryRow(ctx,
		`SELECT id FROM models WHERE id = $1 AND user_id = $2 FOR UPDATE`,
		id, userID).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("library: check model: %w", err)
	}

	// Files are deleted explicitly rather than left to ON DELETE CASCADE,
	// because RETURNING is how the storage keys are learnt. The cascade stays
	// as the backstop it already was.
	//
	// Assumption, load-bearing and true only until versions exist: a model has
	// no children. models.parent_id cascades, so once M9 can create a version,
	// deleting its parent will drop the version's rows here and leave its blobs
	// on disk. Whoever builds nesting has to widen this to the subtree - and
	// widen the FOR UPDATE above with it, since the lock covers this row only.
	rows, err := tx.Query(ctx,
		`DELETE FROM model_files WHERE model_id = $1 RETURNING storage_key`, id)
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

	if _, err := tx.Exec(ctx, `DELETE FROM models WHERE id = $1`, id); err != nil {
		return fmt.Errorf("library: delete model: %w", err)
	}

	// Blobs are removed only once the commit has been acknowledged. A commit
	// error is ambiguous - it may have succeeded on the server and lost the
	// reply - so unlinking on that path could delete the blobs of rows that
	// survived. Create follows the same rule in the other direction.
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("library: commit: %w", err)
	}

	s.removeBlobs(ctx, keys)
	return nil
}

// DeleteFile removes one file from a model, leaving the model in place. A model
// with no files left is a legal state: it is what makes a half-finished upload
// repairable without deleting the whole entry.
func (s *Service) DeleteFile(ctx context.Context, userID, modelID, fileID int64) error {
	// One statement, no transaction: a single DELETE is already atomic, and the
	// join is what enforces ownership, so another user's file is not found
	// rather than forbidden.
	var key string
	err := s.db.QueryRow(ctx,
		`DELETE FROM model_files f USING models m
		  WHERE f.id = $1 AND f.model_id = $2 AND m.id = f.model_id AND m.user_id = $3
		RETURNING f.storage_key`,
		fileID, modelID, userID).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("library: delete file: %w", err)
	}

	s.removeBlobs(ctx, []string{key})
	return nil
}

// Open returns an open handle to one file's blob, with the row that describes
// it, or ErrNotFound if the file does not exist or belongs to somebody else.
// The caller closes the handle.
func (s *Service) Open(ctx context.Context, userID, modelID, fileID int64) (*os.File, File, error) {
	var f File
	var key string
	err := s.db.QueryRow(ctx,
		`SELECT f.id, f.filename, f.type, f.content_type, f.size_bytes, f.created_at, f.storage_key
		   FROM model_files f JOIN models m ON m.id = f.model_id
		  WHERE f.id = $1 AND f.model_id = $2 AND m.user_id = $3`,
		fileID, modelID, userID,
	).Scan(&f.ID, &f.Filename, &f.Type, &f.ContentType, &f.Size, &f.CreatedAt, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, File{}, ErrNotFound
	}
	if err != nil {
		return nil, File{}, fmt.Errorf("library: open: %w", err)
	}

	// A row whose blob is missing is a 500, not a 404. The row exists, so the
	// database and the disk disagree, and that is a server fault the caller
	// cannot fix - reporting it as "not found" would hide the one inconsistency
	// this package is built to prevent.
	fh, err := os.Open(filepath.Join(s.dir, key))
	if err != nil {
		return nil, File{}, fmt.Errorf("library: open blob %q: %w", key, err)
	}
	return fh, f, nil
}

// OpenThumbnail returns an open handle to one file's thumbnail PNG, or
// ErrNotFound if the file does not exist, belongs to somebody else, or has no
// thumbnail. The caller closes the handle.
func (s *Service) OpenThumbnail(ctx context.Context, userID, modelID, fileID int64) (*os.File, error) {
	var key string
	var has bool
	err := s.db.QueryRow(ctx,
		`SELECT f.storage_key, f.has_thumbnail
		   FROM model_files f JOIN models m ON m.id = f.model_id
		  WHERE f.id = $1 AND f.model_id = $2 AND m.user_id = $3`,
		fileID, modelID, userID,
	).Scan(&key, &has)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("library: open thumbnail: %w", err)
	}
	// A file with no thumbnail is a 404: nothing was ever written for it, so
	// there is no inconsistency to report, and the client asked for a resource
	// that does not exist.
	if !has {
		return nil, ErrNotFound
	}

	fh, err := os.Open(filepath.Join(s.dir, key+thumbSuffix))
	if errors.Is(err, os.ErrNotExist) {
		// Not a 500, unlike a missing blob. A blob is the only copy of what the
		// user uploaded and losing one is data loss; a thumbnail is derived
		// from that blob and can be made again, so its absence is a picture
		// that is not there - which is what 404 means and what the grid already
		// draws a placeholder for. The log is how I find out, and it fires per
		// request, so a publish path that quietly stopped writing sidecars is
		// loud enough without returning a server error to every tile.
		//
		// It also covers the ordinary race where a delete commits between the
		// SELECT above and this Open: the file really is gone by then, and 404
		// is the truth rather than a consolation.
		slog.WarnContext(ctx, "library: thumbnail sidecar missing", "key", key, "file_id", fileID)
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("library: open thumbnail %q: %w", key, err)
	}
	return fh, nil
}

// SetThumbnail pins a file as the model's thumbnail, or clears the pin when
// fileID is nil so the automatic rule takes over again.
//
// The UPDATE carries the whole rule rather than trusting a prior SELECT: the
// subquery is what makes "the file belongs to this model and has a thumbnail"
// true at write time instead of a moment before it.
//
// It narrows the window rather than closing it. A read-committed subquery does
// not lock the row it found, so a delete committing between the subquery and
// the foreign key's own check turns the write into a 23503. Taking a lock to
// prevent that would be machinery for a race whose right answer is already
// known: the file is gone, the pin is refused, and 422 says so. Mapping the
// error is the whole fix.
func (s *Service) SetThumbnail(ctx context.Context, userID, modelID int64, fileID *int64) (ModelDetail, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE models SET thumbnail_file_id = $3
		  WHERE id = $1 AND user_id = $2
		    AND ($3::bigint IS NULL OR EXISTS (
		          SELECT 1 FROM model_files f
		           WHERE f.id = $3 AND f.model_id = $1 AND f.has_thumbnail))`,
		modelID, userID, fileID)
	// "23503" spelled out rather than pulling in github.com/jackc/pgerrcode for
	// one constant. It is a SQLSTATE, so it is as stable as the name would be.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return ModelDetail{}, fmt.Errorf("%w: that file is not one of this model's files with a thumbnail", errInvalid)
	}
	if err != nil {
		return ModelDetail{}, fmt.Errorf("library: set thumbnail: %w", err)
	}

	// No rows updated means one of two different answers, and the UPDATE cannot
	// tell them apart. A second query decides: if the model is not there it is
	// a 404, and if it is, the file was the problem and that is the caller's
	// mistake to fix.
	if tag.RowsAffected() == 0 {
		var exists bool
		err := s.db.QueryRow(ctx,
			`SELECT true FROM models WHERE id = $1 AND user_id = $2`, modelID, userID).Scan(&exists)
		if errors.Is(err, pgx.ErrNoRows) {
			return ModelDetail{}, ErrNotFound
		}
		if err != nil {
			return ModelDetail{}, fmt.Errorf("library: set thumbnail: %w", err)
		}
		return ModelDetail{}, fmt.Errorf("%w: that file is not one of this model's files with a thumbnail", errInvalid)
	}

	return s.Get(ctx, userID, modelID)
}

// ignored: the rows are already gone, so the only cost is disk that nothing
// references, and there is nothing useful to tell the caller who asked for a
// delete that did happen.
func (s *Service) removeBlobs(ctx context.Context, keys []string) {
	for _, key := range keys {
		if err := os.Remove(filepath.Join(s.dir, key)); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.WarnContext(ctx, "library: orphaned blob", "key", key, "error", err)
		}
		// The sidecar goes with the blob it describes. Most files have none, so
		// ErrNotExist is the normal outcome and is not worth logging.
		if err := os.Remove(filepath.Join(s.dir, key+thumbSuffix)); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.WarnContext(ctx, "library: orphaned thumbnail", "key", key, "error", err)
		}
	}
}

// removeOne deletes a blob that is not referenced by any committed row. It is
// only ever called before COMMIT is attempted; see Create.
func removeOne(f staged) {
	if f.tmpPath != "" {
		os.Remove(f.tmpPath)
		// Only meaningful once publish has run and tmpPath is the final name,
		// but running it either way is one syscall against a name that does not
		// exist, and the alternative is a flag saying which phase we are in.
		os.Remove(f.tmpPath + thumbSuffix)
	}
}

// pickThumbnail chooses the file whose thumbnail stands for the model, or nil.
//
// The order is the epic's: an image the user uploaded beats a render the slicer
// embedded, because a photograph of the finished print is what they went to the
// trouble of adding. Within a type it is the oldest file, which is stable - a
// later upload does not silently move the tile - and files arrive ordered by
// id, so the first match wins with no sort.
//
// This resolves in Go and not in SQL because there are four call sites and two
// of them never read the model back from the database: Create builds its
// response by hand, and Update returns what it wrote. A SQL rule would be right
// on the list and the detail read and wrong on both of those.
func pickThumbnail(files []File) *int64 {
	for _, want := range []string{"image", "3mf", "gcode"} {
		for i := range files {
			if files[i].Type == want && files[i].HasThumbnail {
				return &files[i].ID
			}
		}
	}
	return nil
}

// resolveThumbnail settles a model's thumbnail from its pin and its files.
//
// The pin is passed separately rather than read off m, because m's field holds
// the *resolved* answer: scanning the stored value into it and then overwriting
// it would make "the user pinned this" and "the rule chose this" the same state
// halfway through, and the client needs to tell them apart.
//
// A pin that no longer has a thumbnail falls back to the rule instead of
// blanking the tile. That is reachable without anything going wrong: pin a
// file, and a future milestone that regenerates thumbnails could leave the pin
// pointing at a file that no longer has one.
func resolveThumbnail(m *ModelDetail, pinned *int64) {
	if pinned != nil {
		for i := range m.Files {
			if m.Files[i].ID == *pinned && m.Files[i].HasThumbnail {
				m.ThumbnailFileID = pinned
				m.ThumbnailAutomatic = false
				return
			}
		}
	}
	m.ThumbnailFileID = pickThumbnail(m.Files)
	m.ThumbnailAutomatic = true
}

// errInvalid marks the errors that are the client's fault, so the API layer can
// map them to 422 without matching on strings.
//
// The text is a prefix on every message built from it, and it is shown to the
// user, so it says "request" rather than "upload": these are now also a bad
// category colour and an unknown tag, neither of which is an upload.
var errInvalid = errors.New("library: invalid request")

// fileTypes maps a lowercase extension to the vocabulary the epic settled. The
// mapping is total: anything unlisted is a document, which is why the database
// carries no CHECK constraint.
var fileTypes = map[string]string{
	"stl": "stl", "3mf": "3mf",
	"gcode": "gcode", "gco": "gcode", "g": "gcode", "bgcode": "gcode",
	"step": "step", "stp": "step",
	"obj": "obj",
	"png": "image", "jpg": "image", "jpeg": "image", "gif": "image",
	"webp": "image", "bmp": "image", "svg": "image",
}

// fileType derives a file's type from its extension. The extension is
// authoritative once written: nothing re-derives it later, so a rename in the
// library never silently reclassifies a file.
func fileType(filename string) string {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(filename), "."))
	if t, ok := fileTypes[ext]; ok {
		return t
	}
	return "document"
}

// storageKey is random, not derived from the filename, so a client-supplied
// name can never contribute a path segment or collide with another upload.
func storageKey(filename string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("library: rand: %w", err)
	}
	return hex.EncodeToString(b[:]) + sanitizeExt(filename), nil
}

// sanitizeExt returns a safe ".ext" (possibly empty) for a client filename. It
// exists only so a human browsing UPLOAD_DIR can tell blobs apart; nothing
// reads it back.
func sanitizeExt(filename string) string {
	ext := strings.ToLower(path.Ext(filename))
	if ext == "" {
		return ""
	}
	ext = ext[1:]
	if ext == "" || len(ext) > 8 {
		return ""
	}
	for _, r := range ext {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return "." + ext
}

// maxFilenameBytes bounds the stored display name. 255 is the usual filesystem
// limit, so no real filename is affected, but a hand-rolled client can send a
// name up to Go's multipart header limit and filename is a text column.
const maxFilenameBytes = 255

// displayName reduces a client-supplied filename to its base name. Browsers
// send just a base name, but a hand-rolled client can send anything, so both
// separators are normalized and path.Base is used rather than filepath.Base,
// which is a no-op on backslashes when the server runs on Linux. The result is
// forced to valid UTF-8 because Postgres rejects raw bytes in a text column.
func displayName(filename string) string {
	name := path.Base(strings.ReplaceAll(filename, `\`, "/"))
	if name == "" || name == "." || name == ".." || name == "/" {
		return "upload"
	}
	name = strings.ToValidUTF8(name, "")
	if len(name) > maxFilenameBytes {
		cut := maxFilenameBytes
		for cut > 0 && !utf8.RuneStart(name[cut]) {
			cut--
		}
		name = name[:cut]
	}
	if name == "" {
		return "upload"
	}
	return name
}
