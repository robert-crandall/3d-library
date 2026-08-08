# Thumbnail extraction fixtures

Real files, from real slicers. Invented fixtures agree with whatever the parser
happens to do, which is how M3 nearly shipped a G-code parser that could not
read an OrcaSlicer file, so nothing here was typed by hand.

The G-code files keep the real head and the real tail with the extrusion body
replaced by `; ---- body elided for the test fixture ----`. The tests do not use
their on-disk length: at 15-48 KB each one is under the 144 KB at which the two
read windows collapse into a single whole-file read, so `virtual()` splices the
bytes back into a reader that reports the original file's length. Without that
the fixtures would prove the opposite of what they claim, because a block the
real file hides behind 16 KB would be visible in a short one.

| File | Source | What it is | Why it is here |
| --- | --- | --- | --- |
| `orcaslicer_1.5.gcode` | [`mjonuschat/acceleration-control`](https://github.com/mjonuschat/acceleration-control) `GCode/orcaslicer.gcode`, 278,218 B | OrcaSlicer 1.5.0. One 300x300 PNG in a `THUMBNAIL_BLOCK_START` wrapper, bytes 342-6,019 | The wrapper OrcaSlicer and Bambu Studio add around PrusaSlicer's format. Comfortably inside the head window |
| `prusaslicer_2.4.gcode` | [`kageurufu/preprocess_cancellation`](https://github.com/kageurufu/preprocess_cancellation) `GCode/prusaslicer.gcode`, 263,086 B | PrusaSlicer 2.4.0, **CRLF line endings**. 64x64 at 323-3,536 and 400x300 at 3,544-19,824 | Two blocks, and the larger one straddles the 16 KB edge. Pins both the CRLF handling and the "a truncated block is not a thumbnail" rule |
| `prusaslicer_2.9_qoi.gcode` | [`bramp/gcode`](https://github.com/bramp/gcode) `tests/fixtures/lines_0.4n_0.2mm_PETG_XLIS_57s.gcode`, 253,459 B | PrusaSlicer 2.9.2. Three QOI blocks fill the head; the sole PNG is at byte 147,341 | Asserts **no thumbnail**. QOI is not decoded and the PNG is past both windows, which is the documented cost of a bounded read rather than a bug |
| `superslicer_footer.gcode` | SuperSlicer 2.4.58 head and tail, with `orcaslicer_1.5.gcode`'s real 300x300 block moved to the end | Footer placement, block at byte 17,806 | **Relocated, not captured.** No public G-code with a real trailing thumbnail was found: the vendored SuperSlicer config has `thumbnails_end_file = 0`, and every slicer ships that default. The block is real, its position is not, and the tests present the file at 5 MB so the tail window is what finds it |
| `prusaslicer.3mf` | PrusaSlicer project export | Real `Metadata/thumbnail.png`, 256x256, 35,334 B. Mesh replaced with a stub | The single-entry layout |
| `bambustudio.3mf` | Bambu Studio entry naming, real PNGs | `Metadata/plate_1.png` (300x300, 4,064 B) beside `pick_1.png` (11,580 B) and `plate_no_light_1.png` (35,334 B) | The two entries that are not the model's picture are deliberately the **two largest**, so "take the biggest PNG" returns a colour-keyed click mask and the test says so |
| `nothumb.3mf` | OrcaSlicer project export | A real 3MF with no PNG in it | The archive-with-no-thumbnail path |
| `render.png` | PrusaSlicer's 256x256 render | RGBA PNG | An uploaded image as its own thumbnail |
| `photo.jpg` | `render.png` re-encoded with `sips` | JPEG | A JPEG must still come out as PNG |

All four G-code blocks decode to PNG colortype 6, RGBA. Slicers render the part
on a transparent background, which is why the stored thumbnail is always PNG: a
JPEG would flatten that alpha to black.

Not covered, deliberately: QOI decoding, bgcode (the binary G-code container),
Cura, ideaMaker, and 3MFs written by anything other than PrusaSlicer, Bambu
Studio and OrcaSlicer. Each is a follow-up with a real file behind it or it is
not worth adding.
