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
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

// Dir reports where blobs are written. Tests read it to check what landed.
func (s *Service) Dir() string { return s.dir }

// Model is a library entry. FileCount and TotalSize are derived on read rather
// than stored: they are one aggregate over an indexed foreign key at
// single-user scale, and a stored counter would be a cache to invalidate.
type Model struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	FileCount int       `json:"fileCount"`
	TotalSize int64     `json:"totalSize"`
	CreatedAt time.Time `json:"createdAt"`
	Files     []File    `json:"files,omitempty"`
}

// File is one uploaded file belonging to a model.
type File struct {
	ID          int64     `json:"id"`
	Filename    string    `json:"filename"`
	Type        string    `json:"type"`
	ContentType string    `json:"contentType"`
	Size        int64     `json:"size"`
	CreatedAt   time.Time `json:"createdAt"`
}

// staged is a file written to disk but not yet committed to the database.
type staged struct {
	tmpPath string
	key     string
	name    string
	typ     string
	ctype   string
	size    int64
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
func (s *Service) Create(ctx context.Context, userID int64, name string, parts *multipart.Reader) (Model, error) {
	file, err := s.stageOnly(parts)
	if err != nil {
		removeOne(file)
		return Model{}, err
	}
	if err := s.publish(&file); err != nil {
		removeOne(file)
		return Model{}, err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		// The commit was never attempted, so the blob is unreferenced and
		// removing it is safe.
		removeOne(file)
		return Model{}, fmt.Errorf("library: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var m Model
	m.Name = name
	err = tx.QueryRow(ctx,
		`INSERT INTO models (user_id, name) VALUES ($1, $2) RETURNING id, created_at`,
		userID, name,
	).Scan(&m.ID, &m.CreatedAt)
	if err != nil {
		removeOne(file)
		return Model{}, fmt.Errorf("library: insert model: %w", err)
	}

	out, err := insertFile(ctx, tx, m.ID, file)
	if err != nil {
		removeOne(file)
		return Model{}, err
	}

	// Past this point the file is never removed. A commit can succeed on the
	// server and still return a network error, and deleting the blob of a row
	// that does exist would manufacture exactly the dangling entry this whole
	// ordering prevents. An unreferenced blob is the better end of that trade -
	// it costs disk, not correctness.
	if err := tx.Commit(ctx); err != nil {
		return Model{}, fmt.Errorf("library: commit: %w", err)
	}

	m.Files = []File{out}
	m.FileCount = 1
	m.TotalSize = out.Size
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

	// Re-check ownership inside the transaction. The count above is advisory -
	// it ran on another connection - and this is the one that actually stops a
	// file being attached to somebody else's model.
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
	err := tx.QueryRow(ctx,
		`INSERT INTO model_files (model_id, storage_key, filename, type, content_type, size_bytes)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at`,
		modelID, f.key, f.name, f.typ, f.ctype, f.size,
	).Scan(&out.ID, &out.CreatedAt)
	if err != nil {
		return File{}, fmt.Errorf("library: insert file: %w", err)
	}
	out.Filename, out.Type, out.ContentType, out.Size = f.name, f.typ, f.ctype, f.size
	return out, nil
}

// publish moves a staged blob to its final name, after which it is a real file
// that a reader may assume is complete.
func (s *Service) publish(f *staged) error {
	final := filepath.Join(s.dir, f.key)
	if err := os.Rename(f.tmpPath, final); err != nil {
		return fmt.Errorf("library: rename: %w", err)
	}
	f.tmpPath = final
	return nil
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
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return staged{}, fmt.Errorf("library: create: %w", err)
	}
	st := staged{tmpPath: tmpPath, key: key, name: name, typ: fileType(name)}

	// Reading one byte past the cap is what distinguishes "exactly at the cap"
	// from "over it" without buffering anything.
	size, err := io.Copy(f, io.LimitReader(part, s.maxFileBytes+1))
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
	st.ctype = part.Header.Get("Content-Type")
	if st.ctype == "" {
		st.ctype = "application/octet-stream"
	}
	return st, nil
}

// List returns the user's root models, newest first. Versions - models with a
// parent - are excluded here rather than filtered by the caller, because every
// listing in this app is over roots.
func (s *Service) List(ctx context.Context, userID int64) ([]Model, error) {
	rows, err := s.db.Query(ctx,
		`SELECT m.id, m.name, m.created_at,
		        count(f.id), coalesce(sum(f.size_bytes), 0)
		   FROM models m
		   LEFT JOIN model_files f ON f.model_id = m.id
		  WHERE m.user_id = $1 AND m.parent_id IS NULL
		  GROUP BY m.id
		  ORDER BY m.created_at DESC, m.id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("library: list: %w", err)
	}
	defer rows.Close()

	// Non-nil so an empty library encodes as [] rather than null, which the
	// frontend would otherwise have to guard against.
	models := []Model{}
	for rows.Next() {
		var m Model
		if err := rows.Scan(&m.ID, &m.Name, &m.CreatedAt, &m.FileCount, &m.TotalSize); err != nil {
			return nil, fmt.Errorf("library: list: %w", err)
		}
		models = append(models, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: list: %w", err)
	}
	return models, nil
}

// Get returns one model with its files, or ErrNotFound if it does not exist or
// belongs to somebody else.
func (s *Service) Get(ctx context.Context, userID, id int64) (Model, error) {
	var m Model
	err := s.db.QueryRow(ctx,
		`SELECT id, name, created_at FROM models WHERE id = $1 AND user_id = $2`,
		id, userID,
	).Scan(&m.ID, &m.Name, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Model{}, ErrNotFound
	}
	if err != nil {
		return Model{}, fmt.Errorf("library: get: %w", err)
	}

	rows, err := s.db.Query(ctx,
		`SELECT id, filename, type, content_type, size_bytes, created_at
		   FROM model_files WHERE model_id = $1 ORDER BY id`, id)
	if err != nil {
		return Model{}, fmt.Errorf("library: get files: %w", err)
	}
	defer rows.Close()

	m.Files = []File{}
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.Filename, &f.Type, &f.ContentType, &f.Size, &f.CreatedAt); err != nil {
			return Model{}, fmt.Errorf("library: get files: %w", err)
		}
		m.Files = append(m.Files, f)
		m.TotalSize += f.Size
	}
	if err := rows.Err(); err != nil {
		return Model{}, fmt.Errorf("library: get files: %w", err)
	}
	m.FileCount = len(m.Files)
	return m, nil
}

// removeOne deletes a blob that is not referenced by any committed row. It is
// only ever called before COMMIT is attempted; see Create.
func removeOne(f staged) {
	if f.tmpPath != "" {
		os.Remove(f.tmpPath)
	}
}

// errInvalid marks the errors that are the client's fault, so the API layer can
// map them to 422 without matching on strings.
var errInvalid = errors.New("library: invalid upload")

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
