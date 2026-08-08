package thumb

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/robert-crandall/3d-library/internal/gcode"
)

// The fixtures keep their source file's real head and real tail with the
// extrusion body elided, so they are small enough to commit. Their own length
// is therefore a lie: at 15-48 KB every one of them is under the 144 KB at
// which the two windows collapse into a single whole-file read, and a test that
// passed the shortened length would prove the opposite of what it claims - that
// a block the real file hides past 16 KB is visible. logicalSize is the length
// the fixture is presented at, and virtual puts the bytes back at the two ends
// of a file that long.
var logicalSize = map[string]int64{
	// The three sizes below are the real upstream files', so the byte offsets
	// the assertions rely on are the offsets a user's file would have.
	"orcaslicer_1.5.gcode":      278218,
	"prusaslicer_2.4.gcode":     263086,
	"prusaslicer_2.9_qoi.gcode": 253459,
	// This one is already a construction - a real SuperSlicer file with a real
	// block moved to its end - so its length is chosen rather than measured.
	// Anything over 144 KB works; what matters is that the block ends up in the
	// tail window instead of a whole-file read.
	"superslicer_footer.gcode": 5 << 20,
}

const elision = "; ---- body elided for the test fixture ----"

// virtual reads a fixture and presents it at its logical length, with the head
// bytes at 0 and the tail bytes at the end.
func virtual(t *testing.T, name string) (io.ReaderAt, int64) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	size, ok := logicalSize[name]
	if !ok {
		t.Fatalf("no logical size recorded for %s", name)
	}
	head, tail, found := strings.Cut(string(raw), elision)
	if !found {
		t.Fatalf("%s has no elision marker", name)
	}
	if int64(len(head)+len(tail)) > size {
		t.Fatalf("%s is longer than the size it claims", name)
	}
	return &spliced{size: size, segs: []segment{
		{off: 0, data: []byte(head)},
		{off: size - int64(len(tail)), data: []byte(tail)},
	}}, size
}

type segment struct {
	off  int64
	data []byte
}

type read struct {
	off, n int64
}

// spliced is a virtual file: real bytes at known offsets, zeroes everywhere
// else, and a log of every read made through it.
type spliced struct {
	size  int64
	segs  []segment
	reads []read
}

func (s *spliced) ReadAt(p []byte, off int64) (int, error) {
	s.reads = append(s.reads, read{off: off, n: int64(len(p))})
	if off >= s.size {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > s.size-off {
		n = int(s.size - off)
	}
	for i := range p[:n] {
		p[i] = '\n'
	}
	for _, seg := range s.segs {
		lo, hi := seg.off, seg.off+int64(len(seg.data))
		for i := 0; i < n; i++ {
			at := off + int64(i)
			if at >= lo && at < hi {
				p[i] = seg.data[at-lo]
			}
		}
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func decodeSize(t *testing.T, b []byte) (int, int) {
	t.Helper()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("output does not decode: %v", err)
	}
	if format != "png" {
		t.Fatalf("output is %s, want png", format)
	}
	return cfg.Width, cfg.Height
}

// TestExtractGCode runs the real slicer files at their real length. The
// expected sizes are the point: prusaslicer_2.4 has a 400x300 block that the
// real file puts at bytes 3,544-19,824, so the 16 KB window cuts it in half and
// the 64x64 at 323-3,536 is what comes out. A test that asserted only "some
// thumbnail" would pass while silently returning the truncated one, and a test
// run against the shortened fixture would return 400x300 and prove nothing.
func TestExtractGCode(t *testing.T) {
	for _, tc := range []struct {
		file        string
		want        bool
		wantW       int
		wantH       int
		whatItCosts string
	}{
		{
			file: "orcaslicer_1.5.gcode", want: true, wantW: 300, wantH: 300,
			whatItCosts: "OrcaSlicer writes one 300x300 PNG at byte 342, well inside the window",
		},
		{
			file: "prusaslicer_2.4.gcode", want: true, wantW: 64, wantH: 64,
			whatItCosts: "the larger 400x300 straddles the 16 KB edge, so the small one wins",
		},
		{
			file: "prusaslicer_2.9_qoi.gcode", want: false,
			whatItCosts: "three QOI blocks fill the window; the only PNG is at byte 147,341",
		},
		{
			file: "superslicer_footer.gcode", want: true, wantW: 300, wantH: 300,
			whatItCosts: "the block is at the end of the file, so the tail window found it",
		},
	} {
		t.Run(tc.file, func(t *testing.T) {
			r, size := virtual(t, tc.file)
			got, ok := Extract(r, size, "gcode")
			if ok != tc.want {
				t.Fatalf("Extract = %v, want %v (%s)", ok, tc.want, tc.whatItCosts)
			}
			if !tc.want {
				if got != nil {
					t.Errorf("returned %d bytes alongside false", len(got))
				}
				return
			}
			w, h := decodeSize(t, got)
			if w != tc.wantW || h != tc.wantH {
				t.Errorf("got %dx%d, want %dx%d (%s)", w, h, tc.wantW, tc.wantH, tc.whatItCosts)
			}
		})
	}
}

// TestExtractGCodeReadsOnlyTwoWindows is the real guard on the bounded read.
// Extract's output would look identical if it called io.ReadAll, and the
// failure would arrive as an out-of-memory on a user's 800 MB print. The
// virtual file carries a thumbnail marker whose payload runs past the 16 KB
// edge, so an implementation that "reads a little more when the marker looks
// incomplete" fails here rather than passing quietly.
func TestExtractGCodeReadsOnlyTwoWindows(t *testing.T) {
	const size = 4 << 30

	var head bytes.Buffer
	head.WriteString("; generated by PrusaSlicer 2.4.0 on 2021-09-03 at 16:52:36 UTC\n")
	head.WriteString("; thumbnail begin 400x300 999999\n")
	for head.Len() < gcode.HeadBytes*2 {
		head.WriteString("; iVBORw0KGgoAAAANSUhEUgAAAZAAAABkCAYAAAC0tYLAAAAAAXNSR0IArs4c6QAAAAlwSFlzAAAL\n")
	}

	big := &spliced{size: size, segs: []segment{{off: 0, data: head.Bytes()}}}
	if _, ok := Extract(big, size, "gcode"); ok {
		t.Fatal("extracted a thumbnail from a block that is cut off at the window edge")
	}

	if len(big.reads) != 2 {
		t.Fatalf("made %d reads, want 2: %v", len(big.reads), big.reads)
	}
	var total int64
	for _, r := range big.reads {
		total += r.n
	}
	if total != gcode.HeadBytes+gcode.TailBytes {
		t.Errorf("read %d bytes, want %d", total, gcode.HeadBytes+gcode.TailBytes)
	}
	if big.reads[0].off != 0 {
		t.Errorf("first read at %d, want 0", big.reads[0].off)
	}
	if big.reads[1].off != size-gcode.TailBytes {
		t.Errorf("second read at %d, want %d", big.reads[1].off, size-gcode.TailBytes)
	}
}

// TestGCodeRanksByDeclaredSize pins the choice between two blocks that both
// decode. The real fixtures cannot cover this: PrusaSlicer's larger block is cut
// off by the window, so the small one wins by default and the ranking never
// runs. Here both are whole and the larger is written second, so an
// implementation that takes the first block it finds returns 8x8.
func TestGCodeRanksByDeclaredSize(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("; generated by PrusaSlicer 2.6.0\n")
	writeBlock(t, &buf, 8, 8)
	writeBlock(t, &buf, 48, 32)
	buf.WriteString("G1 X0 Y0\n")

	got, ok := Extract(bytes.NewReader(buf.Bytes()), int64(buf.Len()), "gcode")
	if !ok {
		t.Fatal("Extract returned false")
	}
	if w, h := decodeSize(t, got); w != 48 || h != 32 {
		t.Errorf("got %dx%d, want 48x32: the larger block must win regardless of file order", w, h)
	}
}

// writeBlock emits a thumbnail block in PrusaSlicer's format: a begin marker
// carrying WxH and the base64 length, the payload wrapped at 78 characters
// behind "; ", then an end marker.
func writeBlock(t *testing.T, w io.Writer, width, height int) {
	t.Helper()
	var png bytes.Buffer
	if err := encodePNG(&png, width, height); err != nil {
		t.Fatal(err)
	}
	enc := base64.StdEncoding.EncodeToString(png.Bytes())
	fmt.Fprintf(w, ";\n; thumbnail begin %dx%d %d\n", width, height, len(enc))
	for len(enc) > 78 {
		fmt.Fprintf(w, "; %s\n", enc[:78])
		enc = enc[78:]
	}
	fmt.Fprintf(w, "; %s\n; thumbnail end\n;\n", enc)
}

func encodePNG(w io.Writer, width, height int) error {
	return png.Encode(w, image.NewNRGBA(image.Rect(0, 0, width, height)))
}

// TestExtract3MF covers both real archives and the ranking trap. In
// bambustudio.3mf the two entries that are not the model's picture are
// deliberately the two largest - pick_1.png is 11,580 bytes and
// plate_no_light_1.png is 35,334, against plate_1.png's 4,064 - so an
// implementation that simply takes the biggest PNG returns the wrong image and
// this test says which one it got.
func TestExtract3MF(t *testing.T) {
	for _, tc := range []struct {
		file  string
		want  bool
		wantW int
		wantH int
		note  string
	}{
		{"prusaslicer.3mf", true, 256, 256, "PrusaSlicer writes a single Metadata/thumbnail.png"},
		{"bambustudio.3mf", true, 300, 300, "plate_1.png, not the larger pick_1.png or plate_no_light_1.png"},
		{"nothumb.3mf", false, 0, 0, "a real OrcaSlicer project export with no PNG in it"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			f, err := os.Open("testdata/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			fi, err := f.Stat()
			if err != nil {
				t.Fatal(err)
			}

			got, ok := Extract(f, fi.Size(), "3mf")
			if ok != tc.want {
				t.Fatalf("Extract = %v, want %v (%s)", ok, tc.want, tc.note)
			}
			if !tc.want {
				return
			}
			w, h := decodeSize(t, got)
			if w != tc.wantW || h != tc.wantH {
				t.Errorf("got %dx%d, want %dx%d (%s)", w, h, tc.wantW, tc.wantH, tc.note)
			}
		})
	}
}

// TestExtractImage covers the third source and the two things about it that are
// easy to get wrong: a JPEG has to come out as PNG, and a type the library
// classifies as an image but Go cannot decode has to be a quiet no rather than
// an error.
func TestExtractImage(t *testing.T) {
	for _, tc := range []struct {
		file string
		want bool
		note string
	}{
		{"render.png", true, "a PNG with an alpha channel"},
		{"photo.jpg", true, "a JPEG, which must still come out as PNG"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			f, err := os.Open("testdata/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			fi, _ := f.Stat()

			got, ok := Extract(f, fi.Size(), "image")
			if ok != tc.want {
				t.Fatalf("Extract = %v, want %v (%s)", ok, tc.want, tc.note)
			}
			if _, format, err := image.DecodeConfig(bytes.NewReader(got)); err != nil || format != "png" {
				t.Errorf("output format %q err %v, want png (%s)", format, err, tc.note)
			}
		})
	}

	// SVG is "image" to the library's extension table but is not in the decoder
	// set. The upload must still succeed with no thumbnail.
	if _, ok := Extract(strings.NewReader("<svg xmlns='http://www.w3.org/2000/svg'/>"), 41, "image"); ok {
		t.Error("claimed a thumbnail for an SVG")
	}
}

// TestExtractRefusesOtherTypes states the contract the upload path relies on:
// an STL is not a thumbnail source, and asking is free.
func TestExtractRefusesOtherTypes(t *testing.T) {
	f, err := os.Open("testdata/render.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, _ := f.Stat()

	// The same bytes that succeed as an image must be refused as an STL: the
	// type decides, not the content, so nothing re-sniffs a 500 MB mesh.
	for _, kind := range []string{"stl", "step", "obj", "document", ""} {
		if _, ok := Extract(f, fi.Size(), kind); ok {
			t.Errorf("extracted a thumbnail for type %q", kind)
		}
	}
	if _, ok := Extract(f, 0, "image"); ok {
		t.Error("extracted a thumbnail from a zero-length file")
	}
}

// TestFitScalesDown pins the resize rule. Slicer thumbnails are already small,
// so the branch that matters most is the one that leaves them alone - a 64x64
// pushed through a resampler comes back softer for no reason.
func TestFitScalesDown(t *testing.T) {
	for _, tc := range []struct {
		w, h         int
		wantW, wantH int
		whatItCosts  string
	}{
		{64, 64, 64, 64, "already inside the box, so it must not be touched"},
		{maxEdge, maxEdge, maxEdge, maxEdge, "exactly at the edge is still untouched"},
		{1024, 512, 512, 256, "landscape scales on width and keeps the ratio"},
		{512, 1024, 256, 512, "portrait scales on height"},
		{4000, 4, 512, 1, "an extreme ratio must not round a side to zero"},
	} {
		t.Run(fmt.Sprintf("%dx%d", tc.w, tc.h), func(t *testing.T) {
			var buf bytes.Buffer
			if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, tc.w, tc.h))); err != nil {
				t.Fatal(err)
			}
			got, ok := decodeAndEncode(bytes.NewReader(buf.Bytes()))
			if !ok {
				t.Fatal("decodeAndEncode returned false")
			}
			w, h := decodeSize(t, got)
			if w != tc.wantW || h != tc.wantH {
				t.Errorf("got %dx%d, want %dx%d (%s)", w, h, tc.wantW, tc.wantH, tc.whatItCosts)
			}
		})
	}
}

// TestWithinBudget covers the decompression bomb guard. The overflow cases are
// the reason it divides instead of multiplying: 1<<40 squared is not a large
// number in int64, it is a negative one, and a negative product passes every
// "less than the cap" test ever written.
func TestWithinBudget(t *testing.T) {
	for _, tc := range []struct {
		w, h int
		want bool
	}{
		{1, 1, true},
		{8192, 8192, true},
		{40000, 40000, false},
		{1 << 40, 1 << 40, false},
		{maxPixels, 2, false},
		{0, 10, false},
		{-1, -1, false},
	} {
		if got := withinBudget(tc.w, tc.h); got != tc.want {
			t.Errorf("withinBudget(%d, %d) = %v, want %v", tc.w, tc.h, got, tc.want)
		}
	}
}

// TestDeclaredPixelsIsOnlyAHint checks the ranking key survives a hostile
// header. The declared size comes out of a file, so it is an input: a block
// claiming 99999999x99999999 must not sort itself to the front of the
// candidate list on the strength of an overflowed product.
func TestDeclaredPixelsIsOnlyAHint(t *testing.T) {
	for _, tc := range []struct {
		body string
		want int
	}{
		{"thumbnail begin 300x300 5420", 90000},
		{"thumbnail_QOI begin 16x16 500", 256},
		{"thumbnail begin 99999999x99999999 1", 0},
		{"thumbnail begin -5x-5 1", 0},
		{"thumbnail begin junk 1", 0},
		{"thumbnail begin", 0},
	} {
		if got := declaredPixels(tc.body); got != tc.want {
			t.Errorf("declaredPixels(%q) = %d, want %d", tc.body, got, tc.want)
		}
	}
}

// TestAuxiliaryMatchesBaseNameOnly pins the demotion rule to the file name. An
// archive stored under a folder called "pick" must not have every entry
// demoted, and "picture.png" is a picture.
func TestAuxiliaryMatchesBaseNameOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"Metadata/plate_1.png", false},
		{"Metadata/thumbnail.png", false},
		{"Metadata/picture.png", false},
		{"Metadata/pick_1.png", true},
		{"Metadata/plate_no_light_1.png", true},
		{"Metadata/pick/plate_1.png", false},
	} {
		if got := auxiliary(tc.name); got != tc.want {
			t.Errorf("auxiliary(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A real, valid PNG that decodes to more pixels than the budget allows, and the
// assertion is on allocation rather than on the return value.
//
// The return value proves nothing here: the post-decode bounds check refuses
// this image too, so a version with no header check at all still answers false.
// What separates them is that the version with no header check answers false
// *after* allocating the whole 81 MB image, and a 9000x9000 declaration in a
// 15 KB file is how a decompression bomb gets its name. Measuring the
// allocation is the only way to say which one ran.
//
// All three sources are driven, not just the image one. They share
// decodeAndEncode today, but that is exactly the thing under test: an earlier
// version of this check lived in the image path alone, and a test that only
// uploaded a PNG would not have noticed.
func TestOversizedImageIsRefusedBeforeDecoding(t *testing.T) {
	// 81 million pixels against a 67 million budget, and 1-bit grayscale so the
	// file stays tiny while Go's decoder still expands it to one byte per pixel.
	bomb := onebitPNG(t, 9000, 9000)
	if len(bomb) > 64<<10 {
		t.Fatalf("the fixture is %d bytes; it is meant to be small on disk", len(bomb))
	}

	var gbuf bytes.Buffer
	gbuf.WriteString("; generated by PrusaSlicer 2.6.0\n")
	writeRawBlock(&gbuf, 9000, 9000, bomb)
	gbuf.WriteString("G1 X0 Y0\n")

	for _, tc := range []struct {
		kind string
		body []byte
	}{
		{"image", bomb},
		{"3mf", zipOf(t, map[string][]byte{"Metadata/thumbnail.png": bomb})},
		{"gcode", gbuf.Bytes()},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			if _, ok := Extract(bytes.NewReader(tc.body), int64(len(tc.body)), tc.kind); ok {
				t.Fatal("extracted a thumbnail from a 9000x9000 image")
			}
			runtime.ReadMemStats(&after)

			// 8 MB against the 81 MB the decode would need. The margin is
			// ten-fold in both directions, so this is a threshold and not a
			// coin toss.
			if used := after.TotalAlloc - before.TotalAlloc; used > 8<<20 {
				t.Errorf("allocated %d bytes refusing a 9000x9000 image; the header check is not running before the decode", used)
			}
		})
	}
}

// A zip's cost is paid when it is opened, not when an entry is read, so this
// measures allocation rather than the answer. Every case here would pass a test
// that only asserted "no thumbnail" - the archives genuinely have none.
//
// Each case defeats a different arm of the check. The second is the one worth
// staring at: it declares a hundred-byte directory and archive/zip reads all
// 200,000 headers anyway, because when the declared size does not lead
// anywhere sensible it falls back to trusting directoryOffset on its own. A
// budget that read only directorySize measures 49 MB there.
func TestAHostileDirectoryIsRefusedBeforeParsing(t *testing.T) {
	const entries = 200000

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i < entries; i++ {
		if _, err := zw.CreateHeader(&zip.FileHeader{Name: strconv.Itoa(i), Method: zip.Store}); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zip64 := buf.Bytes()

	// zip.Writer emits zip64 past 65,535 entries, so the archive above only
	// ever reaches the sentinel arm. The rest of the cases need a plain 32-bit
	// record over the same directory, which is legal: nothing stops a writer
	// putting more headers on disk than the count it declares, and archive/zip
	// compares the two only after it has read them all, modulo 2^16.
	find := func(b []byte, sig string) int {
		i := bytes.Index(b, []byte(sig))
		if i < 0 {
			t.Fatalf("no %q in the archive", sig)
		}
		return i
	}
	dirStart := find(zip64, "PK\x01\x02")
	dirEnd := find(zip64, "PK\x06\x06")
	flat := func(dirSize, dirOffset uint32) []byte {
		b := append([]byte(nil), zip64[:dirEnd]...)
		rec := make([]byte, 22)
		copy(rec, "PK\x05\x06")
		binary.LittleEndian.PutUint16(rec[8:], uint16(entries%(1<<16)))
		binary.LittleEndian.PutUint16(rec[10:], uint16(entries%(1<<16)))
		binary.LittleEndian.PutUint32(rec[12:], dirSize)
		binary.LittleEndian.PutUint32(rec[16:], dirOffset)
		return append(b, rec...)
	}
	honest := flat(uint32(dirEnd-dirStart), uint32(dirStart))

	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"declares the whole directory", honest},
		{"understates the size but keeps the offset", flat(100, uint32(dirStart))},
		{"hides behind a zip64 sentinel", zip64},
		{"keeps the zip64 record but writes a modest 32-bit one", func() []byte {
			// records stays 0xffff, which is enough on its own to send
			// archive/zip to the zip64 record for the real size and offset.
			// The 32-bit fields are then free to describe a hundred-byte
			// directory sitting just behind the record, which is exactly what
			// a check that read only these two fields would believe.
			b := append([]byte(nil), zip64...)
			p := len(b) - 22
			binary.LittleEndian.PutUint32(b[p+12:], 100)
			binary.LittleEndian.PutUint32(b[p+16:], uint32(p-100))
			return b
		}()},
		{"buries a second record in the archive comment", func() []byte {
			b := append([]byte(nil), honest...)
			p := len(b) - 22
			fake := make([]byte, 22)
			copy(fake, "PK\x05\x06")
			binary.LittleEndian.PutUint32(fake[12:], 100)
			binary.LittleEndian.PutUint16(b[p+20:], uint16(len(fake)))
			return append(b, fake...)
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			if _, ok := Extract(bytes.NewReader(tc.body), int64(len(tc.body)), "3mf"); ok {
				t.Fatal("extracted a thumbnail from an archive that has none")
			}
			runtime.ReadMemStats(&after)

			if used := after.TotalAlloc - before.TotalAlloc; used > 8<<20 {
				t.Errorf("allocated %d bytes on a %d-entry archive; the directory check did not bound zip.NewReader", used, entries)
			}
		})
	}
}

// The budget must not refuse the archives the app actually stores. A cap that
// rejected a normal 3MF would be worse than no cap, and this is the assertion
// that stops maxDirectoryBytes being tuned down until it does.
func TestARealArchivePassesTheDirectoryBudget(t *testing.T) {
	for _, name := range []string{"bambustudio.3mf", "prusaslicer.3mf", "nothumb.3mf"} {
		body, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if !directoryWithinBudget(bytes.NewReader(body), int64(len(body))) {
			t.Errorf("%s was refused by the directory budget", name)
		}
	}
}

// onebitPNG writes a valid 1-bit grayscale PNG of w x h, all zeroes. Every
// scanline compresses to almost nothing, which is the whole trick: a few
// kilobytes on disk, tens of megabytes once decoded.
func onebitPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")

	chunk := func(kind string, body []byte) {
		if err := binary.Write(&buf, binary.BigEndian, uint32(len(body))); err != nil {
			t.Fatal(err)
		}
		payload := append([]byte(kind), body...)
		buf.Write(payload)
		if err := binary.Write(&buf, binary.BigEndian, crc32.ChecksumIEEE(payload)); err != nil {
			t.Fatal(err)
		}
	}

	var ihdr bytes.Buffer
	binary.Write(&ihdr, binary.BigEndian, uint32(w))
	binary.Write(&ihdr, binary.BigEndian, uint32(h))
	ihdr.Write([]byte{1, 0, 0, 0, 0}) // 1-bit, grayscale, no interlace
	chunk("IHDR", ihdr.Bytes())

	var idat bytes.Buffer
	zw := zlib.NewWriter(&idat)
	row := make([]byte, 1+(w+7)/8) // a filter byte, then the packed bits
	for i := 0; i < h; i++ {
		if _, err := zw.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	chunk("IDAT", idat.Bytes())
	chunk("IEND", nil)
	return buf.Bytes()
}

// A 3MF whose only image is one of the auxiliary renders. The ranking demotes
// pick_*/no_light rather than filtering them, and this is the test that says
// which: an implementation that skips them returns nothing here, and the
// milestone asks for a picture whenever one can be had.
func TestAuxiliaryOnlyArchiveStillYieldsAThumbnail(t *testing.T) {
	var only bytes.Buffer
	if err := encodePNG(&only, 24, 24); err != nil {
		t.Fatal(err)
	}
	archive := zipOf(t, map[string][]byte{"Metadata/pick_1.png": only.Bytes()})

	got, ok := Extract(bytes.NewReader(archive), int64(len(archive)), "3mf")
	if !ok {
		t.Fatal("Extract returned false: an auxiliary image is a worse picture than a plate render, not no picture")
	}
	if w, h := decodeSize(t, got); w != 24 || h != 24 {
		t.Errorf("got %dx%d, want 24x24", w, h)
	}
}

// writeRawBlock is writeBlock for a payload the caller supplies, so a test can
// put bytes in the block that encodePNG would never produce. The declared WxH
// is honest so that declaredPixels lets the block through and the decoder is
// what has to refuse it.
func writeRawBlock(w io.Writer, width, height int, payload []byte) {
	enc := base64.StdEncoding.EncodeToString(payload)
	fmt.Fprintf(w, ";\n; thumbnail begin %dx%d %d\n", width, height, len(enc))
	for len(enc) > 78 {
		fmt.Fprintf(w, "; %s\n", enc[:78])
		enc = enc[78:]
	}
	fmt.Fprintf(w, "; %s\n; thumbnail end\n;\n", enc)
}

// zipOf builds a 3MF-shaped archive in memory. Deflate, not Store, because a
// stored entry would make the decompression-bomb test prove nothing.
func zipOf(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Every extension the library calls an "image" must actually produce one, or
// the type is a promise the UI cannot keep: the tile would say "image" and show
// a placeholder forever. SVG is the deliberate exception, documented in the
// package.
//
// The five swatches are committed rather than encoded here, and that is the
// whole point of the test. Encoding a GIF in the test means importing
// image/gif, whose init registers the decoder in the test binary - so the test
// would pass against a thumb.go that registered nothing. Reading bytes off disk
// leaves the registration entirely to the package under test. They are 20x10
// gradients from Pillow, which is also the only way the WebP one could exist:
// x/image decodes WebP but does not encode it.
//
// A weaker version would test PNG and JPEG, the two that already worked, and
// pass against a build with no GIF, BMP or WebP decoder registered at all.
func TestEveryImageTypeTheLibraryNamesDecodes(t *testing.T) {
	for _, name := range []string{"swatch.png", "swatch.jpg", "swatch.gif", "swatch.bmp", "swatch.webp"} {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile("testdata/" + name)
			if err != nil {
				t.Fatal(err)
			}
			out, ok := Extract(bytes.NewReader(b), int64(len(b)), "image")
			if !ok {
				t.Fatalf("Extract returned false for a valid %s", name)
			}
			// The output is always PNG whatever went in, because the browser
			// gets one content type and the sidecar has no extension.
			cfg, err := png.DecodeConfig(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("output is not a PNG: %v", err)
			}
			if cfg.Width != 20 || cfg.Height != 10 {
				t.Errorf("got %dx%d, want 20x10 - nothing under maxEdge is resized", cfg.Width, cfg.Height)
			}
		})
	}
}
