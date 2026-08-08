// Package thumb makes a small PNG preview of an uploaded file.
//
// Three sources, one output. An image file is its own thumbnail; a 3MF is a zip
// with the slicer's render under Metadata/; a G-code file has that render
// base64'd into a comment block near one end. Everything else has none.
//
// Everything is best-effort, like the G-code parser next door: a file this
// package cannot read is still a file, and Extract says so by returning false
// rather than an error. Nothing here may fail an upload.
package thumb

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	_ "image/gif"  // registers the GIF decoder
	_ "image/jpeg" // registers the JPEG decoder; uploaded photos are the common case
	"image/png"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/robert-crandall/3d-library/internal/gcode"

	_ "golang.org/x/image/bmp" // registers the BMP decoder
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the WebP decoder
)

// The blank imports above exist to match the library's own vocabulary: it calls
// png, jpg, jpeg, gif, webp, bmp and svg all "image", so all but one of those
// must decode or the type is a lie. SVG is the exception and stays one - it is
// a document to every decoder in Go, and rasterising it means a renderer, a
// font stack and a much larger attack surface for a format almost nobody
// uploads to a 3D print library. An SVG gets a placeholder.

// maxEdge is the longest side of the stored thumbnail.
//
// The grid tile is 168 px tall and the file list uses 22 px, so 512 covers both
// on a 2x display with room to grow. Nothing upscales: a 64x64 slicer thumbnail
// stays 64x64 rather than becoming a blurry 512.
const maxEdge = 512

// maxPixels bounds what will be decoded.
//
// A decoded image is 4 bytes a pixel whatever the file size, so a 200 KB PNG
// declaring 40000x40000 is a 6.4 GB allocation - the decompression bomb. 64
// megapixels is more than any camera the user will point at a print and about
// 256 MB decoded, which is survivable.
const maxPixels = 64 << 20

// maxEntryBytes bounds one zip entry.
//
// A 3MF's mesh runs to tens of megabytes but its thumbnail does not, so this is
// only ever hit by something that is not a thumbnail. It also caps the classic
// zip bomb: an entry claiming 4 GB uncompressed stops here rather than in the
// allocator. It does not cap the entry *count*; maxDirectoryBytes does.
const maxEntryBytes = 16 << 20

// maxDirectoryBytes bounds the zip central directory.
//
// zip.NewReader parses the whole central directory up front and keeps a
// zip.File per entry, which measures at about 245 bytes against 78 bytes of
// archive for a minimal entry. So a 500 MB upload - the per-file cap - of
// nothing but empty entries turns into something over a gigabyte of live heap
// before a single byte is decompressed, and no per-entry limit can help
// because the cost is already paid by the time there are entries to look at.
//
// A real 3MF has tens of entries. 4 MB of directory is room for roughly 50,000,
// which is three orders of magnitude of headroom and still bounds the reader at
// about 10 MB.
const maxDirectoryBytes = 4 << 20

// Extract returns a PNG thumbnail for a file, or false if it has none.
//
// kind is the library's file type, so the caller does not re-derive it from the
// name. Only "image", "3mf" and "gcode" can produce anything; every other type
// returns false without reading a byte.
func Extract(r io.ReaderAt, size int64, kind string) ([]byte, bool) {
	if size <= 0 {
		return nil, false
	}
	switch kind {
	case "image":
		return fromImage(r, size)
	case "3mf":
		return fromArchive(r, size)
	case "gcode":
		return fromGCode(r, size)
	}
	return nil, false
}

// fromImage re-encodes an uploaded image.
//
// The file is never read whole. DecodeConfig reads only the header, which is
// what lets an oversized image be refused before it is decoded, and Decode then
// gets a second section reader because the first one has been consumed.
func fromImage(r io.ReaderAt, size int64) ([]byte, bool) {
	return decodeAndEncode(io.NewSectionReader(r, 0, size))
}

// fromArchive pulls the slicer's render out of a 3MF.
//
// A 3MF is an OPC zip. PrusaSlicer writes one Metadata/thumbnail.png; Bambu
// Studio and OrcaSlicer write a set - plate_1.png is the render, and beside it
// sit pick_1.png (a flat colour-keyed mask for click detection) and
// plate_no_light_1.png (the same scene unlit). Both are real images and both
// look wrong as a thumbnail, so they are ranked last rather than excluded: a
// 3MF whose only image is one of them still gets a thumbnail, which is what the
// acceptance criterion asks for.
func fromArchive(r io.ReaderAt, size int64) ([]byte, bool) {
	if !directoryWithinBudget(r, size) {
		return nil, false
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, false
	}

	var candidates []*zip.File
	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		if strings.HasPrefix(name, "metadata/") && strings.HasSuffix(name, ".png") {
			candidates = append(candidates, f)
		}
	}
	// Preferred entries first, then largest, then by name so a tie is not
	// decided by zip order.
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if ai, bi := auxiliary(a.Name), auxiliary(b.Name); ai != bi {
			return !ai
		}
		if a.UncompressedSize64 != b.UncompressedSize64 {
			return a.UncompressedSize64 > b.UncompressedSize64
		}
		return a.Name < b.Name
	})

	// One entry open at a time. Reading every candidate up front would hold
	// several decoded megabytes for the sake of a file that is usually the
	// first one tried.
	for _, f := range candidates {
		if f.UncompressedSize64 > maxEntryBytes {
			continue
		}
		if out, ok := readEntry(f); ok {
			return out, true
		}
	}
	return nil, false
}

// directoryWithinBudget reports whether the archive's central directory is small
// enough to hand to zip.NewReader. See maxDirectoryBytes for why the check has
// to happen first.
//
// The budget is expressed as a distance, not as a declared size, because the
// declared size is not what bounds the reader. archive/zip derives where the
// directory starts from the end-of-central-directory record two different ways:
// normally eocd-directorySize, but when that lands somewhere that does not look
// like a header it falls back to trusting directoryOffset on its own (a real
// workaround for real archives with a wrong offset). So an archive can declare
// a hundred-byte directory, keep an honest offset, and still get every one of
// its two hundred thousand headers read. Measured: 49 MB. Both readings are
// therefore checked, and the loop cannot run past the record itself, so the
// larger of the two distances is the true bound on what will be parsed.
//
// A zip64 archive keeps the real values in a second record. Rather than parse
// it this refuses every value that would send archive/zip looking for one, and
// that is a choice, not an oversight: zip64 exists for archives past 4 GB or
// 65,535 entries, no slicer writes one for a 3MF, and following the pointer
// would hand an attacker a two-byte bypass of the whole check.
//
// An archive with no readable EOCD is passed through. It is not a zip, so
// zip.NewReader will say so and allocate nothing.
func directoryWithinBudget(r io.ReaderAt, size int64) bool {
	off, rec, ok := findEOCD(r, size)
	if !ok {
		return true
	}
	records := binary.LittleEndian.Uint16(rec[10:12])
	dirSize := binary.LittleEndian.Uint32(rec[12:16])
	dirOffset := binary.LittleEndian.Uint32(rec[16:20])

	// The exact set archive/zip treats as "this may be zip64", including its
	// 0xffff test of a 32-bit field. Refusing a superset would be safe; this is
	// the set, so the two agree about which archives never reach that path.
	if records == 0xFFFF || dirSize == 0xFFFF || dirSize == 0xFFFFFFFF || dirOffset == 0xFFFFFFFF {
		return false
	}

	span := int64(dirSize)
	if back := off - int64(dirOffset); back > span {
		span = back
	}
	return span <= maxDirectoryBytes
}

// findEOCD locates the end-of-central-directory record the way archive/zip
// does: the last kilobyte first, then the last 64 KB, taking the last signature
// in the block whose comment length reaches exactly to the end of the file.
//
// Copying the search rather than writing a simpler one is the point. A budget
// computed from a different record than the one the reader will use is not a
// budget, and a `PK\x05\x06` inside an archive comment is enough to make two
// reasonable searches disagree.
func findEOCD(r io.ReaderAt, size int64) (off int64, rec []byte, ok bool) {
	const eocdLen = 22
	for _, span := range []int64{1024, 65 * 1024} {
		if span > size {
			span = size
		}
		buf := make([]byte, span)
		if _, err := r.ReadAt(buf, size-span); err != nil && err != io.EOF {
			return 0, nil, false
		}
		for i := len(buf) - eocdLen; i >= 0; i-- {
			if buf[i] != 'P' || buf[i+1] != 'K' || buf[i+2] != 0x05 || buf[i+3] != 0x06 {
				continue
			}
			comment := int(binary.LittleEndian.Uint16(buf[i+20 : i+22]))
			if i+eocdLen+comment > len(buf) {
				// Truncated comment. archive/zip abandons the whole block
				// here rather than looking further back, so this does too.
				break
			}
			return size - span + int64(i), buf[i:], true
		}
		if span == size {
			break
		}
	}
	return 0, nil, false
}

// readEntry decodes one zip entry, closing it before the caller moves on.
func readEntry(f *zip.File) ([]byte, bool) {
	rc, err := f.Open()
	if err != nil {
		return nil, false
	}
	defer rc.Close()

	// LimitReader as well as the size check above, because the header's
	// declared size is the archive's word and not a fact.
	raw, err := io.ReadAll(io.LimitReader(rc, maxEntryBytes))
	if err != nil {
		return nil, false
	}
	return decodeAndEncode(bytes.NewReader(raw))
}

// auxiliary reports whether a 3MF entry is one of the renders that is not the
// model's picture. Matching is on the base name so a folder called "pick" does
// not demote everything inside it, and a false positive only costs a place in
// the ranking.
func auxiliary(name string) bool {
	base := strings.ToLower(path.Base(name))
	return strings.HasPrefix(base, "pick_") || strings.Contains(base, "no_light")
}

// fromGCode pulls the render out of a slicer's comment block.
//
// The two windows are the G-code package's, imported rather than repeated: a
// sliced file runs to hundreds of megabytes and nothing here may grow with file
// size. A thumbnail outside them is not extracted, which is a real limitation -
// PrusaSlicer 2.9 writes QOI thumbnails ahead of the PNG and can push it past
// 128 KB - and is the documented cost of a bounded read.
func fromGCode(r io.ReaderAt, size int64) ([]byte, bool) {
	var blocks []gcodeBlock
	if size <= gcode.HeadBytes+gcode.TailBytes {
		blocks = findBlocks(window(r, 0, size))
	} else {
		blocks = append(blocks, findBlocks(window(r, 0, gcode.HeadBytes))...)
		blocks = append(blocks, findBlocks(window(r, size-gcode.TailBytes, gcode.TailBytes))...)
	}

	// Biggest declared image first, because a slicer that writes several writes
	// a small one for the printer's screen and a large one for the file
	// browser. The declaration is only a ranking hint: it is the slicer's word
	// about bytes we have not decoded yet, and the loop below is what decides.
	sort.SliceStable(blocks, func(i, j int) bool { return blocks[i].pixels > blocks[j].pixels })

	for _, b := range blocks {
		if out, ok := decodeAndEncode(bytes.NewReader(b.payload)); ok {
			return out, true
		}
	}
	return nil, false
}

type gcodeBlock struct {
	pixels  int
	payload []byte
}

// findBlocks collects every complete thumbnail block in one window.
//
// The format is PrusaSlicer's, and OrcaSlicer, SuperSlicer and Bambu Studio all
// inherit it:
//
//	; thumbnail begin 300x300 5420
//	; iVBORw0KGgoAAAANS... (78 base64 characters per line)
//	; thumbnail end
//
// The trailing number is the base64 length, not the decoded length. It is not
// used: a block whose payload is short because the window cut it off is exactly
// the case that has to be skipped, and failing to decode says that already.
//
// thumbnail_JPG and thumbnail_QOI blocks are collected too. JPEG decodes; QOI
// does not, and lands in the loop above as a candidate that fails, which is why
// candidates are tried in order rather than the best one being picked outright.
func findBlocks(b []byte) []gcodeBlock {
	var out []gcodeBlock

	// Windows-written G-code is CRLF, so every line is trimmed on both sides
	// rather than split on "\n" and used as-is.
	lines := strings.Split(string(b), "\n")
	var enc strings.Builder
	var pixels int
	open := false

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, ";") {
			continue
		}
		body := strings.TrimSpace(strings.TrimPrefix(line, ";"))

		switch {
		case strings.HasSuffix(body, " end") && open && isMarker(body):
			if raw, err := base64.StdEncoding.DecodeString(enc.String()); err == nil && len(raw) > 0 {
				out = append(out, gcodeBlock{pixels: pixels, payload: raw})
			}
			open, pixels = false, 0
			enc.Reset()
		case isMarker(body) && strings.Contains(body, " begin "):
			// A second "begin" before an "end" means the first block was cut
			// off by the window edge. Start over rather than splicing two
			// payloads into one unusable one.
			open, pixels = true, declaredPixels(body)
			enc.Reset()
		case open:
			enc.WriteString(body)
		}
	}
	return out
}

// isMarker reports whether a comment body opens or closes a thumbnail block.
func isMarker(body string) bool {
	tag, _, ok := strings.Cut(body, " ")
	if !ok {
		return false
	}
	return tag == "thumbnail" || strings.HasPrefix(tag, "thumbnail_")
}

// declaredPixels reads WxH out of a begin marker, for ranking only.
//
// The multiplication is guarded by bounding each side first. Two ints parsed
// from a file and multiplied is an overflow, and a negative product sorts to
// the front - the largest candidate would become the one with the most absurd
// header.
func declaredPixels(body string) int {
	fields := strings.Fields(body)
	if len(fields) < 3 {
		return 0
	}
	w, h, ok := strings.Cut(fields[2], "x")
	if !ok {
		return 0
	}
	wi, err1 := strconv.Atoi(w)
	hi, err2 := strconv.Atoi(h)
	if err1 != nil || err2 != nil || wi <= 0 || hi <= 0 || !withinBudget(wi, hi) {
		return 0
	}
	return wi * hi
}

// window reads one range. A read error yields no bytes rather than an error,
// matching the G-code parser: a window we could not read is a window with
// nothing in it, and the other one may still have everything.
func window(r io.ReaderAt, off, n int64) []byte {
	buf := make([]byte, n)
	read, err := io.ReadFull(io.NewSectionReader(r, off, n), buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil
	}
	return buf[:read]
}

// decodeAndEncode turns any supported image into the stored PNG.
//
// PNG out, always. Every thumbnail a slicer writes has an alpha channel - the
// part is rendered on a transparent background - and JPEG would flatten that to
// black. One output format also means the handler can send a fixed content type
// without sniffing what it is about to serve.
func decodeAndEncode(r io.ReadSeeker) ([]byte, bool) {
	// The header first, and a seek back, because the budget has to be enforced
	// *before* the allocation it is guarding against. A 40 KB PNG can declare
	// 50000x50000 and image.Decode will dutifully try to allocate 10 GB for it;
	// checking img.Bounds() afterwards is a check that never runs. Every source
	// in this package comes through here, so this is the only place that needs
	// to know that.
	cfg, _, err := image.DecodeConfig(r)
	if err != nil || !withinBudget(cfg.Width, cfg.Height) {
		return nil, false
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, false
	}

	img, _, err := image.Decode(r)
	if err != nil {
		return nil, false
	}

	// Checked again after decoding: a decoder is entitled to produce different
	// bounds from the header, and an empty image encodes to a PNG nobody wants.
	b := img.Bounds()
	if b.Empty() || !withinBudget(b.Dx(), b.Dy()) {
		return nil, false
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, fit(img)); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// fit scales an image down to maxEdge, preserving aspect ratio. An image
// already inside the box is returned untouched - re-encoding a 64x64 slicer
// thumbnail through a resampler only softens it.
func fit(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxEdge && h <= maxEdge {
		return img
	}
	if w > h {
		h = h * maxEdge / w
		w = maxEdge
	} else {
		w = w * maxEdge / h
		h = maxEdge
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	return dst
}

// withinBudget reports whether w*h is inside maxPixels, by division so the
// multiplication that would overflow is never performed.
func withinBudget(w, h int) bool {
	if w <= 0 || h <= 0 {
		return false
	}
	return h <= maxPixels/w
}
