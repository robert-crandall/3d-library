# G-code fixtures

Four trimmed captures of real slicer output, used by `../toolpath.test.ts`.

These are here because invented fixtures hide real traps, and one of them was found
exactly this way: OrcaSlicer's Klipper start G-code contains macro names whose letters
are G-code axis words (`PRINT_START EXTRUDER=260`, `SET_VELOCITY_LIMIT ACCEL=300`). A
parser that scans a line for a command word instead of judging the first word alone
reads the `I` of `PRINT` and the `E` of `SET` as motion, and refuses two of the five
slicers this app supports. Nobody would write that by hand.

| File | Slicer | What it is for |
| --- | --- | --- |
| `superslicer.gcode` | SuperSlicer 2.5 | `BEFORE_`/`AFTER_LAYER_CHANGE` bracketing a `LAYER_CHANGE`, which have to coalesce into one transition |
| `orcaslicer_1.5.gcode` | OrcaSlicer 1.5 | Klipper macro lines in the start G-code; the same BEFORE/AFTER bracketing |
| `orcaslicer_2.3.gcode` | OrcaSlicer 2.3 | A belt printer: toolpaths at a machine Z near -988 while the file announces `;Z:0.2`. Also writes `;_SET_FAN_SPEED_CHANGING_LAYER` at every boundary, which a substring match treats as a marker |
| `cura.gcode` | Cura 5.x | Shares no spelling with the others: `;LAYER:n` markers, no `;Z:`, and a `;LAYER_COUNT:` line sitting right beside the real markers |

## How they were made

Each is the head of an unmodified download, cut after a fixed number of layer markers
and then back to the last complete command, so the start G-code is intact and the file
ends mid-print rather than mid-line. Nothing inside the kept range was edited, which is
the point: the traps live in lines nobody would think to write.

The expected layer counts, Z labels and segment counts in `toolpath.test.ts` were read
back out of these trimmed files, not out of the originals.

## What is deliberately not here

The parser was also checked against six complete unmodified files totalling about 5 MB,
including a 320-layer PrusaSlicer 2.1 benchy and a five-tool PrusaSlicer 2.9 file whose
start G-code is 2,800 lines. Every one reported exactly the layer count its own header
declared. Those files are not committed because they are hundreds of kilobytes each and
the cases they carry - a purge line absorbed into layer one, `AFTER_LAYER_CHANGE` with
no `;Z:` to fall back on, per-tool extruder state across `T` changes - are covered by
synthetic cases in `toolpath.test.ts` that say what they are testing.

The `.bgcode` container is not represented: it needs heatshrink decompression, the
server-side extractor gets nothing out of it either, and the parser refuses it by its
`GCDE` magic number rather than pretending.
