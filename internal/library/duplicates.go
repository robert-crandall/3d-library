package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
)

// The duplicate finder answers one question: which files in this library hold
// the same bytes, so a copy can be deleted to get the disk back.
//
// The whole design rests on two facts about how this app already stores files.
//
// First, a storage key is 16 random bytes per upload, so two rows with
// identical content never share a blob. Deleting one copy can never unlink the
// blob another row still points at - the failure mode this feature would
// otherwise have to defend against does not exist here, and a test asserts it
// rather than a comment claiming it.
//
// Second, a published blob is never rewritten. publish() renames a staged temp
// file into place; Update edits metadata, SetThumbnail moves a pin, and there
// is no replace-file endpoint. So a content_hash is permanently correct once
// computed, which is what lets a rescan skip every file it has already hashed
// and makes the column a cache rather than a snapshot.

// DuplicateFile is one member of a duplicate group. It carries the model it
// belongs to because that is the only way to tell two copies apart in the UI -
// the filenames are usually the same, which is how they got downloaded twice.
type DuplicateFile struct {
	FileID    int64  `json:"fileId"`
	Filename  string `json:"filename"`
	ModelID   int64  `json:"modelId"`
	ModelName string `json:"modelName"`
}

// DuplicateGroup is a set of files with identical content.
//
// Size is on the group rather than on each file because every member has it by
// definition - equal content means equal length - and repeating it per file
// would invite the reader to wonder when the two could disagree.
type DuplicateGroup struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
	// Reclaimable is what deleting every copy but one would free.
	Reclaimable int64           `json:"reclaimable"`
	Files       []DuplicateFile `json:"files"`
}

// ScanStatus is what the duplicates page needs to know about scanning, beyond
// the groups themselves.
//
// Running, Hashed, Total and Error come from memory; Pending and ScannedAt come
// from the database. That split is deliberate. Progress within a run is only
// meaningful while the run exists, but "is this library fully hashed" has to
// survive a restart - otherwise a process that died mid-scan comes back with no
// error, an old timestamp, and the confidence to render "no duplicates" for a
// library it never finished reading.
type ScanStatus struct {
	Running bool `json:"running"`
	Hashed  int  `json:"hashed"`
	Total   int  `json:"total"`
	// Pending is how many files still need hashing: candidates whose size is
	// shared with another file and whose content_hash is still NULL. Zero is
	// what makes an empty Groups list mean "no duplicates" rather than "not
	// finished looking".
	Pending int `json:"pending"`
	// ScannedAt is when a scan last ran, taken before it chose its candidates.
	// It is a "scanned through" watermark, not a completion time: a file
	// uploaded mid-scan is not in that run's candidate set.
	ScannedAt *time.Time `json:"scannedAt,omitempty"`
	// Error names why this process's last run left work behind. It is a
	// courtesy, not load-bearing - Pending is what the UI actually branches on,
	// because this field is gone after a restart.
	Error string `json:"error,omitempty"`
}

// Duplicates is the whole duplicates page in one response.
type Duplicates struct {
	Groups []DuplicateGroup `json:"groups"`
	Status ScanStatus       `json:"status"`
}

// scanState is one user's in-flight scan. There is at most one per user and
// this is a single-user app, so a mutex is the entire concurrency model - no
// queue, no worker pool, no scheduler. The mutex guards every field, not just
// the map lookup that finds this struct.
type scanState struct {
	running bool
	hashed  int
	total   int
	err     string
}

// candidateSQL is the size prefilter, and it is the reason this feature can run
// over hundreds of gigabytes without reading them.
//
// A file can only be a duplicate of a file with the same length, so anything
// whose size is unique in this library is not a candidate and is never opened.
// The inner aggregate counts *all* of the user's files at a size, including
// ones already hashed, so a newly uploaded file that matches an old hashed one
// is still picked up.
//
// Both halves carry the user_id predicate. The outer one keeps another user's
// file from being hashed; the inner one keeps another user's file from making
// one of mine a candidate, which is the difference between "hashes nothing when
// every one of my sizes is unique" and "hashes whatever happens to collide with
// a stranger's library".
const candidateSQL = `
	  FROM model_files f JOIN models m ON m.id = f.model_id
	 WHERE m.user_id = $1 AND f.content_hash IS NULL
	   AND f.size_bytes IN (
	       SELECT f2.size_bytes FROM model_files f2 JOIN models m2 ON m2.id = f2.model_id
	        WHERE m2.user_id = $1
	        GROUP BY f2.size_bytes HAVING count(*) > 1)`

// StartDuplicateScan begins a scan if one is not already running, and returns
// the status either way. A second POST while a scan runs is a no-op that
// reports the running scan, so a double-clicked button cannot start two.
func (s *Service) StartDuplicateScan(ctx context.Context, userID int64) (ScanStatus, error) {
	s.scanMu.Lock()
	st := s.scans[userID]
	if st == nil {
		st = &scanState{}
		s.scans[userID] = st
	}
	start := !st.running
	if start {
		st.running = true
		st.hashed, st.total, st.err = 0, 0, ""
	}
	s.scanMu.Unlock()

	if start {
		// context.WithoutCancel keeps the request's values - the logger, the
		// trace - but drops its cancellation, which is the whole point: the
		// POST returns immediately, and a scan tied to its context would be
		// cancelled the moment it did.
		go s.runDuplicateScan(context.WithoutCancel(ctx), userID, st)
	}
	return s.duplicateStatus(ctx, userID)
}

// runDuplicateScan hashes every candidate, one file at a time.
//
// Deliberately not here: cancellation (no acceptance criterion asks for it),
// scheduling (explicitly out of scope), parallel hashing (the disk is the
// bottleneck on a single-user box, not the CPU), and any persistence of
// in-flight progress. A scan interrupted by a restart needs no resume logic
// because the next one only looks at rows whose content_hash is still NULL.
func (s *Service) runDuplicateScan(ctx context.Context, userID int64, st *scanState) {
	// Taken before the candidates are chosen, because that is the moment the
	// run's view of the library is fixed. Recording the finish time instead
	// would claim coverage of files uploaded while it worked.
	startedAt := time.Now().UTC()

	failed, err := s.hashCandidates(ctx, userID, st)

	var writeErr string
	if err != nil {
		// The run never got a candidate list, so no scan happened and there is
		// no watermark to record.
		slog.ErrorContext(ctx, "library: duplicate scan failed", "user", userID, "error", err)
	} else if _, dbErr := s.db.Exec(ctx,
		`INSERT INTO library_scans (user_id, duplicates_scanned_at) VALUES ($1, $2)
		 ON CONFLICT (user_id) DO UPDATE SET duplicates_scanned_at = EXCLUDED.duplicates_scanned_at`,
		userID, startedAt); dbErr != nil {
		slog.ErrorContext(ctx, "library: record scan time", "user", userID, "error", dbErr)
		// Reported, not swallowed. Without this the run goes terminal with no
		// error and no timestamp, and the page says "nothing has been compared
		// yet" about a scan that just read the whole library.
		writeErr = "the scan finished but its result could not be recorded"
	}

	// running goes false last, after every durable effect of the run has
	// landed. The client stops polling the moment it sees this, so clearing it
	// before the watermark is written is a race the client always loses: it
	// reads "finished, never scanned" and renders it.
	s.scanMu.Lock()
	st.running = false
	switch {
	case err != nil:
		st.err = err.Error()
	case writeErr != "":
		st.err = writeErr
	case failed > 0:
		st.err = fmt.Sprintf("%d files could not be read", failed)
	}
	s.scanMu.Unlock()
}

// hashCandidates reads and hashes every candidate, returning how many could not
// be read. A read failure does not abort the run: the rest of the library still
// produces useful groups, and the unread file stays NULL so it shows up in
// Pending until somebody fixes it.
func (s *Service) hashCandidates(ctx context.Context, userID int64, st *scanState) (int, error) {
	type candidate struct {
		id  int64
		key string
	}
	rows, err := s.db.Query(ctx, `SELECT f.id, f.storage_key`+candidateSQL+` ORDER BY f.id`, userID)
	if err != nil {
		return 0, fmt.Errorf("library: duplicate candidates: %w", err)
	}
	var todo []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.key); err != nil {
			rows.Close()
			return 0, fmt.Errorf("library: duplicate candidates: %w", err)
		}
		todo = append(todo, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("library: duplicate candidates: %w", err)
	}

	s.scanMu.Lock()
	st.total = len(todo)
	s.scanMu.Unlock()

	failed := 0
	for _, c := range todo {
		sum, err := hashBlob(filepath.Join(s.dir, c.key))
		if err != nil {
			slog.WarnContext(ctx, "library: could not hash file", "file", c.id, "error", err)
			failed++
			continue
		}
		// One autocommit statement per file, with no transaction spanning the
		// read above. That is what "does not lock up the app" means here: the
		// scan never holds a row lock while doing disk I/O, so uploads, edits
		// and deletes keep working against these same rows throughout.
		//
		// content_hash IS NULL makes it idempotent, and a file deleted while
		// the scan was reading it simply updates no rows.
		if _, err := s.db.Exec(ctx,
			`UPDATE model_files SET content_hash = $1 WHERE id = $2 AND content_hash IS NULL`,
			sum, c.id); err != nil {
			slog.WarnContext(ctx, "library: could not store hash", "file", c.id, "error", err)
			failed++
			continue
		}
		s.scanMu.Lock()
		st.hashed++
		s.scanMu.Unlock()
	}
	return failed, nil
}

// hashBlob streams one blob through SHA-256. Streamed rather than read whole
// because a file here can be 500 MB and there is no reason to hold one.
func hashBlob(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// Duplicates returns the groups and the scan status.
//
// The three reads share one REPEATABLE READ snapshot. Under READ COMMITTED they
// could straddle a hash landing between them: groups reads empty, the scan
// stores the last pair's hashes, pending reads zero, and the page confidently
// renders "no duplicate files" for a library that has some. A read-only
// transaction over three small queries costs nothing at this scale and makes
// that impossible rather than unlikely.
func (s *Service) Duplicates(ctx context.Context, userID int64) (Duplicates, error) {
	// The in-memory half is read *before* the snapshot opens, and the order is
	// the whole point. runDuplicateScan writes its watermark and only then
	// clears running, so a run that is already finished here is a run whose
	// watermark is already committed - and any snapshot taken after this moment
	// therefore sees it. Reading running after the snapshot instead lets the
	// two straddle the end of a run: the snapshot predates the watermark, the
	// flag postdates it, and the client is handed "finished, never scanned" and
	// stops polling.
	s.scanMu.Lock()
	status := ScanStatus{}
	if st := s.scans[userID]; st != nil {
		status.Running, status.Hashed, status.Total, status.Error = st.running, st.hashed, st.total, st.err
	}
	s.scanMu.Unlock()

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return Duplicates{}, fmt.Errorf("library: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	groups, err := duplicateGroups(ctx, tx, userID)
	if err != nil {
		return Duplicates{}, err
	}

	var pending int
	if err := tx.QueryRow(ctx, `SELECT count(*)`+candidateSQL, userID).Scan(&pending); err != nil {
		return Duplicates{}, fmt.Errorf("library: pending candidates: %w", err)
	}

	var scannedAt *time.Time
	err = tx.QueryRow(ctx,
		`SELECT duplicates_scanned_at FROM library_scans WHERE user_id = $1`, userID).Scan(&scannedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Duplicates{}, fmt.Errorf("library: scan time: %w", err)
	}

	status.Pending, status.ScannedAt = pending, scannedAt
	return Duplicates{Groups: groups, Status: status}, nil
}

// duplicateStatus is the status alone, for the POST's reply.
func (s *Service) duplicateStatus(ctx context.Context, userID int64) (ScanStatus, error) {
	d, err := s.Duplicates(ctx, userID)
	if err != nil {
		return ScanStatus{}, err
	}
	return d.Status, nil
}

// duplicateGroups reads the flat rows and folds them.
//
// Grouped by hash alone rather than by (size, hash): equal hash already implies
// equal size for the files that produced them, and a SHA-256 collision is not a
// failure mode this system makes real.
//
// HAVING count(*) > 1 is why nothing else has to maintain groups. A group
// reduced to one file by a delete stops matching and simply is not returned, so
// there is no delete-time bookkeeping and a stale hash left on a surviving
// singleton is harmless.
func duplicateGroups(ctx context.Context, tx pgx.Tx, userID int64) ([]DuplicateGroup, error) {
	rows, err := tx.Query(ctx,
		`SELECT f.content_hash, f.size_bytes, f.id, f.filename, m.id, m.name
		   FROM model_files f JOIN models m ON m.id = f.model_id
		  WHERE m.user_id = $1 AND f.content_hash IS NOT NULL
		    AND f.content_hash IN (
		        SELECT f2.content_hash FROM model_files f2 JOIN models m2 ON m2.id = f2.model_id
		         WHERE m2.user_id = $1 AND f2.content_hash IS NOT NULL
		         GROUP BY f2.content_hash HAVING count(*) > 1)
		  ORDER BY f.content_hash, f.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("library: duplicate groups: %w", err)
	}
	defer rows.Close()

	var flat []duplicateRow
	for rows.Next() {
		var r duplicateRow
		if err := rows.Scan(&r.Hash, &r.Size, &r.File.FileID, &r.File.Filename,
			&r.File.ModelID, &r.File.ModelName); err != nil {
			return nil, fmt.Errorf("library: duplicate groups: %w", err)
		}
		flat = append(flat, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: duplicate groups: %w", err)
	}
	return foldDuplicates(flat), nil
}

// duplicateRow is one row of the groups query, before folding.
type duplicateRow struct {
	Hash string
	Size int64
	File DuplicateFile
}

// foldDuplicates turns the ordered flat rows into groups. Pure, so the
// arithmetic and the boundaries are testable without a database.
//
// A run of one is dropped. The query cannot produce one, but the rule belongs
// with the type that defines what a group is rather than only in the SQL - and
// it is the shape of the answer AC5 asks for, so it is worth a unit test.
func foldDuplicates(rows []duplicateRow) []DuplicateGroup {
	var groups []DuplicateGroup
	for i := 0; i < len(rows); {
		j := i
		for j < len(rows) && rows[j].Hash == rows[i].Hash {
			j++
		}
		if n := j - i; n > 1 {
			files := make([]DuplicateFile, 0, n)
			for _, r := range rows[i:j] {
				files = append(files, r.File)
			}
			groups = append(groups, DuplicateGroup{
				Hash: rows[i].Hash,
				Size: rows[i].Size,
				// What deleting every copy but one would free.
				Reclaimable: rows[i].Size * int64(n-1),
				Files:       files,
			})
		}
		i = j
	}
	return groups
}
