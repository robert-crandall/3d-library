package library

import "testing"

// foldDuplicates is where a group stops being rows and becomes the answer the
// page renders, so its boundaries and its arithmetic are worth pinning without
// a database in the way.
//
// A weaker test would fold one group of two and call it done. That passes on an
// implementation that never advances past the first run, never drops a
// singleton, and multiplies by n instead of n-1 - the three ways this can be
// wrong.
func TestFoldDuplicates(t *testing.T) {
	row := func(hash string, size, fileID int64) duplicateRow {
		return duplicateRow{Hash: hash, Size: size, File: DuplicateFile{FileID: fileID}}
	}

	for _, tc := range []struct {
		name string
		rows []duplicateRow
		want []DuplicateGroup
	}{
		{
			name: "nothing in, nothing out",
		},
		{
			// The SQL's HAVING count(*) > 1 cannot produce this, but the rule
			// that a group needs two members belongs with the type, not only
			// with the query that happens to feed it today.
			name: "a lone file is not a duplicate",
			rows: []duplicateRow{row("aaa", 100, 1)},
		},
		{
			name: "reclaimable is every copy but one",
			rows: []duplicateRow{row("aaa", 100, 1), row("aaa", 100, 2), row("aaa", 100, 3)},
			want: []DuplicateGroup{{
				Hash: "aaa", Size: 100, Reclaimable: 200,
				Files: []DuplicateFile{{FileID: 1}, {FileID: 2}, {FileID: 3}},
			}},
		},
		{
			// Three runs, and the singleton is in the middle: an implementation
			// that skipped a short run by breaking instead of advancing would
			// lose the third group entirely.
			name: "runs end where the hash changes",
			rows: []duplicateRow{
				row("aaa", 10, 1), row("aaa", 10, 2),
				row("bbb", 20, 3),
				row("ccc", 30, 4), row("ccc", 30, 5),
			},
			want: []DuplicateGroup{
				{Hash: "aaa", Size: 10, Reclaimable: 10, Files: []DuplicateFile{{FileID: 1}, {FileID: 2}}},
				{Hash: "ccc", Size: 30, Reclaimable: 30, Files: []DuplicateFile{{FileID: 4}, {FileID: 5}}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := foldDuplicates(tc.rows)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d groups, want %d", len(got), len(tc.want))
			}
			for i, w := range tc.want {
				g := got[i]
				if g.Hash != w.Hash || g.Size != w.Size || g.Reclaimable != w.Reclaimable {
					t.Errorf("group %d = %+v, want hash %q size %d reclaimable %d",
						i, g, w.Hash, w.Size, w.Reclaimable)
				}
				if len(g.Files) != len(w.Files) {
					t.Fatalf("group %d has %d files, want %d", i, len(g.Files), len(w.Files))
				}
				for k, wf := range w.Files {
					if g.Files[k].FileID != wf.FileID {
						t.Errorf("group %d file %d = %d, want %d", i, k, g.Files[k].FileID, wf.FileID)
					}
				}
			}
		})
	}
}
