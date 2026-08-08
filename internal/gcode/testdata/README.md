# G-code parser fixtures

Real slicer output. A parser that cannot read what the slicers actually emit is
worthless, so these are captured files rather than invented ones - the comment
lines are byte-for-byte what the slicer wrote.

Each file keeps the real header block and the real footer statistics + config
block, with the extrusion body replaced by a single
`; ---- body elided for the test fixture ----` line. Base64/QOI thumbnail blocks
went with the body. That keeps the fixtures small without touching a byte of the
metadata under test.

| File | Slicer | Source |
| --- | --- | --- |
| `prusaslicer.gcode` | PrusaSlicer 2.9.2, 5-extruder MMU | [bramp/gcode](https://github.com/bramp/gcode) `tests/fixtures/lines_0.4n_0.2mm_PETG_XLIS_57s.gcode` |
| `superslicer.gcode` | SuperSlicer 2.5.59 | [Caribou3d/CaribouDuet2Duet3Mini5-Configuration-and-Macros](https://github.com/Caribou3d/CaribouDuet2Duet3Mini5-Configuration-and-Macros) `Configuration/gcodes/Square_PLA215_15min.gcode` |
| `orcaslicer_1.5.gcode` | OrcaSlicer 1.5.0 | [mjonuschat/acceleration-control](https://github.com/mjonuschat/acceleration-control) `GCode/orcaslicer.gcode` |
| `orcaslicer_2.3.gcode` | OrcaSlicer 2.3.2-dev | [tommasobbianchi/ShidaoSlicer](https://github.com/tommasobbianchi/ShidaoSlicer) `Cube_PLA_10x10x10.gcode` |
| `cura.gcode` | Cura_SteamEngine (Marlin flavour) | [Gobbel2000/gcode_metadata](https://github.com/Gobbel2000/gcode_metadata) `sample_gcodes/cura_marlin.gcode` |
| `bambustudio.gcode` | Bambu Studio 02.07.01.62 | **reconstructed, not captured** - see below |

## The Bambu Studio fixture is reconstructed

I could not find a genuine full Bambu Studio `.gcode` to capture, so
`bambustudio.gcode` is assembled from two verified sources rather than a real
print:

- The header shape and the `BambuStudio-02.07.01.62` generator spelling come
  from a real Bambu 3MF's `Metadata/plate_1.gcode`
  ([iamrbtm/dfp_os](https://github.com/iamrbtm/dfp_os)).
- The exact comment formats come from Bambu Studio's own writer,
  `src/libslic3r/GCode/GCodeProcessor.cpp` - `format_filament_used_info` builds
  `"; " + info + " : "` (hence the space before the colon in
  `total filament weight [g] :`), and the estimated-time branch for BBL printers
  emits `"; model printing time: %s; total estimated time: %s"` on one line.

Those two formats are the only things Bambu writes that Orca does not, so they
are the parts worth covering. Everything else in the file is Orca's config
serialisation, which the two real Orca fixtures already exercise.

## Notes on what these files prove

- **PrusaSlicer's is multi-material.** Its values are comma-separated
  per-extruder lists (`; temperature = 230,230,240,240,220`), which is why the
  parser takes the first element for per-extruder scalars.
- **OrcaSlicer 1.5 writes a fake SuperSlicer generator line** immediately after
  `HEADER_BLOCK_END`, labelled `; hack-fix: write fake slicer info here so that
  preprocess_cancellation can process.` A parser that scans for SuperSlicer
  before OrcaSlicer misidentifies every Orca file. First generator line in file
  order wins.
- **OrcaSlicer 1.5 puts two statements on one comment line**:
  `; filament cost = 0.01; total filament used [g] = 0.59`. The parser reads
  only the first, because in a `key = value` line everything after a `;` is the
  value's list separator - splitting there would let a user's free-text
  `filament_notes` override a real setting. Nothing is lost: Orca writes
  `; filament used [g] = 0.59` and `; total filament cost = 0.01` on their own
  lines as well.
- **`filament_cost` is not the print's cost.** In every Slic3r-lineage slicer it
  is the price per kilogram of the spool - `27.82` in the PrusaSlicer fixture.
  The print's cost is the space-separated statistic `total filament cost`
  (`0.01`). Confusing the two overstates a small print by three orders of
  magnitude.
- **Cura omits most of the panel.** No infill, no wall count, no temperatures,
  no filament weight or type. That is the point of acceptance criterion 2: the
  fields a slicer does not write are simply absent.
