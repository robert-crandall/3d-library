// A G-code toolpath parser: bytes in, line segments and layer boundaries out.
//
// Nothing here imports three.js or touches the DOM, because this is the part most
// likely to be quietly wrong and the epic rules out browser tests. Relative
// positioning, extruder relative mode, coordinate offsets, per-tool extruders, arcs and
// a Z that changes mid-layer are all places a plausible-looking parser renders
// something subtly untrue, and all of them are reachable from a real slicer profile.
// Every one of them is a table test in toolpath.test.ts.
//
// It is pushable rather than a single call - `push(bytes)` per chunk of the download,
// `finish()` at the end - so a 300 MB file is parsed as it arrives instead of being
// buffered whole and then parsed.

/** Two endpoints, three axes each. */
const FLOATS_PER_SEGMENT = 6;

/**
 * Segments per fixed-size chunk.
 *
 * The segment count is unknown while streaming and `Content-Length` says nothing
 * usable about it, so the alternative is one growing buffer - which doubles peak
 * memory at every reallocation, on the largest allocation in the app. Fixed chunks
 * never copy. 65536 segments is 1.5 MB per chunk.
 */
import type { Bounds } from '$lib/viewer/framing';

export const CHUNK_SEGMENTS = 65536;

/**
 * The largest print this viewer will draw, counting extrusion and travel together.
 *
 * Measured, not guessed. Parsing a 213 MB generated file on this machine gave 8.0M
 * segments in 3.7 s and 204 MB of `Float32Array` - 27 bytes a segment against a floor of
 * 24, which is two points of three floats. three.js uploads a buffer of the same size to
 * the GPU, so eight million segments is roughly 190 MB on each side of the bus.
 *
 * Real files run about 33-37k segments per megabyte either way you measure it: a 4.6 MB
 * 320-layer Benchy is 151k segments, and the generated file is 37k/MB. So this cap is
 * around a 230 MB G-code file, and the 500 MB upload limit is about 17M segments - which
 * is to say the cap is reachable by a file this app would accept, and is meant to be. A
 * tab holding 800 MB of line geometry on a laptop with integrated graphics is one the
 * browser may simply kill, and "this print is too detailed to preview" is a better
 * outcome than a dead tab.
 */
export const SEGMENT_CAP = 8_000_000;

/**
 * The largest number of layers the scrubber will accept.
 *
 * A file with no layer markers falls back to splitting whenever Z changes, and a vase
 * mode print written by a slicer that emits no markers changes Z on every single
 * segment. Without this, that file allocates a layer record per segment. No slicer this
 * app has seen omits markers, so the cap exists to turn an out-of-memory tab into a
 * sentence, not because it is expected to fire.
 */
export const LAYER_CAP = 100_000;

/**
 * How far a flattened arc may sit from the true curve, in mm.
 *
 * 0.05 mm is a quarter of a layer height and well under a nozzle width, so the
 * flattening is not visible at any zoom this viewer offers.
 */
export const CHORD_TOLERANCE_MM = 0.05;

/**
 * How much text may accumulate without a newline before the file is called malformed.
 *
 * A binary uploaded with a `.gcode` extension has no newlines at all, so without this
 * the whole file arrives in one string before anything notices. 1 MB is far longer than
 * any real G-code line.
 */
const MAX_CARRY_CHARS = 1_000_000;

/**
 * Tolerance for "the same Z", in mm.
 *
 * Not an equality test, because a Z that has been through a `G92` offset is
 * `declared + offset` and no longer has the exact bits the file wrote. 1e-6 mm is a
 * thousandth of the smallest layer height anyone prints.
 */
const Z_EPSILON = 1e-6;

const TWO_PI = Math.PI * 2;

/** One layer, as an exclusive end index into each segment list. */
export type ToolpathLayer = {
  /** The Z this layer prints at, as the file labels it. */
  z: number;
  extrusionEnd: number;
  travelEnd: number;
};

export type Toolpath = {
  /** Extrusion segments, in print order, trimmed to the part actually filled. */
  extrusion: Float32Array[];
  /** Travel moves, same shape. Drawn behind a toggle. */
  travel: Float32Array[];
  extrusionSegments: number;
  travelSegments: number;
  layers: ToolpathLayer[];
  /**
   * Extrusion segments printed before the first layer marker claimed the print.
   *
   * PrusaSlicer's purge line runs the full width of the bed, so framing the camera on a
   * 20 mm part that has one would report the bed's width as the print's. These segments
   * are still drawn and still belong to their layer; they are only left out of the
   * camera fit and the dimensions readout.
   */
  purgeSegments: number;
  /**
   * Axis-aligned bounds of the extrusion that belongs to the print, purge excluded.
   *
   * Accumulated as the segments are emitted rather than measured afterwards: a second
   * pass over the largest allocation in the app, to produce six numbers, is work nobody
   * needs to do. `[0,0,0]`-`[0,0,0]` when the file printed nothing, which `finish`
   * refuses before returning anyway.
   */
  bounds: Bounds;
};

export type ToolpathOptions = {
  segmentCap?: number;
  chunkSegments?: number;
  chordToleranceMm?: number;
  layerCap?: number;
};

export type ToolpathParser = {
  push(bytes: Uint8Array): void;
  finish(): Toolpath;
};

// Character codes, so the scanner never allocates a one-character string.
const SPACE = 32;
const TAB = 9;
const MINUS = 45;
const PLUS = 43;
const DOT = 46;
const ZERO = 48;
const NINE = 57;
const UPPER_A = 65;
const UPPER_Z = 90;
const LOWER_A = 97;
const LOWER_Z = 122;

const G = 103;
const M = 109;
const T = 116;
const X = 120;
const Y = 121;
const Z = 122;
const E = 101;
const I = 105;
const J = 106;
const R = 114;

function isLetter(c: number): boolean {
  return (c >= UPPER_A && c <= UPPER_Z) || (c >= LOWER_A && c <= LOWER_Z);
}

// `e` is deliberately not a number character even though JavaScript would accept
// `1e2`: `E` is the extruder word, so treating it as an exponent would read
// `X1E2` - a move to X1 while extruding 2 mm, which post-processors that strip
// spaces really do write - as a move to X100.
function isNumberChar(c: number): boolean {
  return (c >= ZERO && c <= NINE) || c === DOT || c === MINUS || c === PLUS;
}

const NO_TOOLPATHS = 'This file contains no toolpaths.';
const BINARY_GCODE =
  'This is a binary G-code file (.bgcode), which this viewer cannot read. Re-export it as plain G-code.';
const INCHES = 'This file is in inches (G20), which this viewer cannot read.';
const NOT_XY_PLANE =
  'This file selects an arc plane other than XY (G18/G19), which this viewer cannot read.';
const ARC_R_FORM =
  'This file uses radius-form arcs (G2/G3 with R), which this viewer cannot read.';
const ARC_ABSOLUTE_CENTRES =
  'This file uses absolute arc centres (G90.1), which this viewer cannot read.';
const ARC_NO_CENTRE = 'This file has a G2/G3 arc with no I or J offset.';
const MODAL_MOTION =
  'This file uses modal motion (coordinates with no G0/G1 in front of them), which this viewer cannot read.';
const NOT_A_NUMBER = 'This file has a coordinate that is not a number.';
const LINE_TOO_LONG = 'This file is not text, or has a single line longer than a megabyte.';

function tooManySegments(cap: number): string {
  return `This file has more than ${cap.toLocaleString()} moves, which is more than this viewer can draw.`;
}

function tooManyLayers(cap: number): string {
  return `This file has more than ${cap.toLocaleString()} layers, which is more than this viewer can draw.`;
}

/**
 * Layer markers, matched against the whole trimmed comment payload rather than as a
 * substring.
 *
 * Substring matching is wrong twice over in files this repo has on disk: OrcaSlicer 2.3
 * writes `;_SET_FAN_SPEED_CHANGING_LAYER` twenty times, and Cura writes
 * `;LAYER_COUNT:8` once beside its real `;LAYER:n` markers.
 *
 * `BEFORE_LAYER_CHANGE` is deliberately absent. Every file that writes it also writes
 * `AFTER_LAYER_CHANGE`, and treating both as markers splits each boundary twice on a
 * multi-material print, where a tool change extrudes into the wipe tower between them.
 */
const LAYER_MARKERS = new Set(['LAYER_CHANGE', 'AFTER_LAYER_CHANGE', 'CHANGE_LAYER']);

export function createToolpathParser(options: ToolpathOptions = {}): ToolpathParser {
  const segmentCap = options.segmentCap ?? SEGMENT_CAP;
  const chunkSegments = options.chunkSegments ?? CHUNK_SEGMENTS;
  const chordTolerance = options.chordToleranceMm ?? CHORD_TOLERANCE_MM;
  const layerCap = options.layerCap ?? LAYER_CAP;

  const decoder = new TextDecoder();
  let carry = '';
  let firstPush = true;

  // Machine state. `offset` is physical minus declared: `G92 X10` renames the position
  // the nozzle is already at, so every later absolute X is displaced by the difference.
  // Assigning the declared value straight to the position instead - which is the
  // obvious-looking version - translates all following geometry.
  let absoluteXYZ = true;
  let absoluteE = true;
  let px = 0;
  let py = 0;
  let pz = 0;
  let offsetX = 0;
  let offsetY = 0;
  let offsetZ = 0;

  // A file's position is undefined until it homes or declares one, and homing is a
  // `G28` whose axes and home position this parser does not model. So the first travel
  // move has no start point to draw from, and drawing it from the origin invents a
  // line: on the belt printer in this repo's fixtures, which prints at a machine Z near
  // -988, that line is a metre long and crosses the whole scene.
  let positioned = false;

  // Firmware keeps an extruder register per tool, so returning to a tool resumes its E
  // where it left off. Dropping the baseline on every `T` would misclassify the first
  // move back to a tool as a retraction.
  let tool = 0;
  const eByTool: number[] = [0];

  // Layer state. `pending` is a boolean rather than a counter so that two markers with
  // no extrusion between them open one layer, not two.
  let pending = false;
  let sawMarker = false;
  let anchored = false;
  let announcedZ: number | null = null;
  let openLayer: ToolpathLayer | null = null;
  let openPhysicalZ = 0;
  let purgeSegments = 0;
  // Bounds of the print, purge excluded. Widened per emitted point rather than measured
  // in a second pass; see `Toolpath.bounds`.
  const min: [number, number, number] = [Infinity, Infinity, Infinity];
  const max: [number, number, number] = [-Infinity, -Infinity, -Infinity];

  const layers: ToolpathLayer[] = [];
  const extrusion: Float32Array[] = [];
  const travel: Float32Array[] = [];
  let extrusionSegments = 0;
  let travelSegments = 0;

  // Scanner cursor and the current word, kept out here so scanning a line allocates
  // nothing but the number slices.
  let pos = 0;
  let end = 0;
  let wordLetter = 0;
  let wordValue = 0;

  // Parameters of the line being read. `has*` means "present and a number"; a word with
  // no digits after it leaves the value NaN, which only matters if the command goes on
  // to use it - `G28 X` is a real homing line and must not be an error.
  let hasX = false;
  let hasY = false;
  let hasZ = false;
  let hasE = false;
  let hasI = false;
  let hasJ = false;
  let hasR = false;
  let valX = 0;
  let valY = 0;
  let valZ = 0;
  let valE = 0;
  let valI = 0;
  let valJ = 0;

  function push(bytes: Uint8Array): void {
    if (firstPush) {
      firstPush = false;
      // Binary G-code is heatshrink-compressed and starts `GCDE`. Without this it
      // scans as a few thousand junk words and reports "no toolpaths", which sends the
      // reader looking for a problem with their model.
      if (
        bytes.length >= 4 &&
        bytes[0] === 0x47 &&
        bytes[1] === 0x43 &&
        bytes[2] === 0x44 &&
        bytes[3] === 0x45
      ) {
        throw new Error(BINARY_GCODE);
      }
    }

    const text = carry + decoder.decode(bytes, { stream: true });
    let from = 0;
    for (;;) {
      const nl = text.indexOf('\n', from);
      if (nl < 0) break;
      handleLine(text, from, nl);
      from = nl + 1;
    }
    carry = text.slice(from);
    if (carry.length > MAX_CARRY_CHARS) {
      throw new Error(LINE_TOO_LONG);
    }
  }

  function finish(): Toolpath {
    carry += decoder.decode();
    if (carry.length > 0) {
      handleLine(carry, 0, carry.length);
      carry = '';
    }
    closeLayer();
    if (extrusionSegments === 0) {
      throw new Error(NO_TOOLPATHS);
    }
    return {
      extrusion: trim(extrusion, extrusionSegments),
      travel: trim(travel, travelSegments),
      extrusionSegments,
      travelSegments,
      layers,
      purgeSegments,
      bounds: Number.isFinite(min[0])
        ? { min, max }
        : { min: [0, 0, 0], max: [0, 0, 0] },
    };
  }

  function trim(chunks: Float32Array[], count: number): Float32Array[] {
    if (count === 0) return [];
    const out = chunks.slice();
    const usedInLast = count % chunkSegments || chunkSegments;
    const last = out.length - 1;
    out[last] = out[last].subarray(0, usedInLast * FLOATS_PER_SEGMENT);
    return out;
  }

  function handleLine(text: string, from: number, to: number): void {
    const hash = text.indexOf(';', from);
    if (hash >= 0 && hash < to) {
      handleComment(text.slice(hash + 1, to));
      to = hash;
    }
    if (to <= from) return;

    hasX = hasY = hasZ = hasE = hasI = hasJ = hasR = false;
    pos = from;
    end = to;

    if (!readWord(text)) return;
    const cmdLetter = wordLetter;
    const cmdValue = wordValue;
    if (cmdLetter !== G && cmdLetter !== M && cmdLetter !== T) {
      // A line that starts with a coordinate is modal motion: valid G-code that
      // continues the previous move, which no slicer emits. Ignoring it would drop
      // geometry silently, so it is an error instead.
      if (
        Number.isFinite(cmdValue) &&
        (cmdLetter === X ||
          cmdLetter === Y ||
          cmdLetter === Z ||
          cmdLetter === E ||
          cmdLetter === I ||
          cmdLetter === J)
      ) {
        throw new Error(MODAL_MOTION);
      }
      // Everything else starting with a word that is not a command is a firmware
      // macro and is ignored. This has to be judged on the first word alone rather
      // than by scanning ahead for a `G`: Klipper's `PRINT_START EXTRUDER=260` and
      // `SET_VELOCITY_LIMIT ACCEL=300` are in this repo's own OrcaSlicer fixtures,
      // and both contain a letter that is an axis word in G-code.
      //
      // The cost is that a file whose lines are all numbered - `N123 G1 X10`, which
      // print servers write and slicers do not - is ignored line by line and ends up
      // reporting that it contains no toolpaths. That is a sentence, not a silently
      // wrong picture, which is the property that matters.
      return;
    }

    while (readWord(text)) {
      switch (wordLetter) {
        case X:
          hasX = true;
          valX = wordValue;
          break;
        case Y:
          hasY = true;
          valY = wordValue;
          break;
        case Z:
          hasZ = true;
          valZ = wordValue;
          break;
        case E:
          hasE = true;
          valE = wordValue;
          break;
        case I:
          hasI = true;
          valI = wordValue;
          break;
        case J:
          hasJ = true;
          valJ = wordValue;
          break;
        case R:
          hasR = true;
          break;
      }
    }

    dispatch(cmdLetter, cmdValue);
  }

  function readWord(text: string): boolean {
    let c = 0;
    // Skip anything that is not a letter, which covers the spaces between words and
    // also the `*` of a checksum suffix.
    for (;;) {
      if (pos >= end) return false;
      c = text.charCodeAt(pos);
      if (isLetter(c)) break;
      pos++;
    }
    wordLetter = c | 0x20;
    pos++;
    const start = pos;
    while (pos < end) {
      const n = text.charCodeAt(pos);
      if (!isNumberChar(n)) break;
      pos++;
    }
    // `Number('')` is 0, which would turn a valueless word into a real zero. NaN is
    // what "present, no value" has to mean so the consuming command can reject it.
    wordValue = pos > start ? Number(text.slice(start, pos)) : NaN;
    return true;
  }

  function dispatch(letter: number, value: number): void {
    if (letter === T) {
      if (Number.isFinite(value) && value >= 0) {
        tool = value;
        if (eByTool[tool] === undefined) eByTool[tool] = 0;
      }
      return;
    }
    if (letter === M) {
      if (value === 82) absoluteE = true;
      else if (value === 83) absoluteE = false;
      return;
    }
    switch (value) {
      case 0:
      case 1:
        linearMove();
        return;
      case 2:
        arcMove(true);
        return;
      case 3:
        arcMove(false);
        return;
      case 90:
        // Marlin's `set_relative_mode` writes every axis bit including E, so `G90` and
        // `G91` reset an `M82`/`M83` set earlier; a later `M82`/`M83` overrides E again.
        // Cura's end block relies on this: it emits `G91` and then bare `E` deltas
        // without an `M83`, so treating E as still absolute there reads a retraction and
        // its unretract as one enormous deposit.
        absoluteXYZ = true;
        absoluteE = true;
        return;
      case 91:
        absoluteXYZ = false;
        absoluteE = false;
        return;
      case 92:
        setPosition();
        return;
      case 17:
        return;
      case 18:
      case 19:
        throw new Error(NOT_XY_PLANE);
      case 20:
        // No FDM slicer emits inches, but rendering a model 25.4 times too small is a
        // worse failure than a sentence.
        throw new Error(INCHES);
      case 21:
        return;
      case 90.1:
        // I and J are incremental offsets by default; under G90.1 they are absolute
        // coordinates. Ignoring the mode would misplace every arc after it.
        throw new Error(ARC_ABSOLUTE_CENTRES);
      case 91.1:
        return;
      default:
        // Every other G word - homing, dwells, bed levelling, firmware retraction - is
        // ignored, as every G-code consumer does.
        return;
    }
  }

  /** Where an axis word puts the nozzle, given the current positioning mode. */
  function axisTarget(
    present: boolean,
    value: number,
    current: number,
    offset: number,
  ): number {
    if (!present) return current;
    if (!Number.isFinite(value)) throw new Error(NOT_A_NUMBER);
    // Under G91 the value is already a displacement, so the offset must not be added:
    // doing so would translate the model once per move.
    return absoluteXYZ ? value + offset : current + value;
  }

  function extrusionDelta(): number {
    if (!hasE) return 0;
    if (!Number.isFinite(valE)) throw new Error(NOT_A_NUMBER);
    const previous = eByTool[tool];
    if (absoluteE) {
      eByTool[tool] = valE;
      return valE - previous;
    }
    eByTool[tool] = previous + valE;
    return valE;
  }

  function linearMove(): void {
    const x = axisTarget(hasX, valX, px, offsetX);
    const y = axisTarget(hasY, valY, py, offsetY);
    const z = axisTarget(hasZ, valZ, pz, offsetZ);
    const deltaE = extrusionDelta();
    const movedXY = x !== px || y !== py;
    const moved = movedXY || z !== pz;
    if (moved) {
      // Extrusion needs the nozzle to travel across the plate. A positive E with
      // no XY movement is an unretract: the filament refills the melt zone and
      // nothing lands. Cura's end block is `G91` then `G0 Z1.5 E4.5`, which read
      // as a deposit puts a point 2 mm above the print and reports a 15x15x2.7 mm
      // object where the real one is 0.7 mm tall.
      const extruding = deltaE > 0 && movedXY;
      if (!positioned && !extruding) {
        // Where this move started is unknown, so there is nothing to draw. An
        // extruding first move is drawn anyway: the start point is just as unknown,
        // but dropping printed geometry is the worse of the two wrongs.
        positioned = true;
        px = x;
        py = y;
        pz = z;
        return;
      }
      positioned = true;
      if (extruding) openLayerFor(z);
      emit(extruding, px, py, pz, x, y, z);
      px = x;
      py = y;
      pz = z;
    }
  }

  function arcMove(clockwise: boolean): void {
    if (!hasI && !hasJ) {
      // R form is a different and ambiguous parameterisation - two arcs satisfy any
      // radius - so it is refused rather than approximated by a straight line, which
      // would draw a chord where the file says curve.
      throw new Error(hasR ? ARC_R_FORM : ARC_NO_CENTRE);
    }
    const i = hasI ? valI : 0;
    const j = hasJ ? valJ : 0;
    if (!Number.isFinite(i) || !Number.isFinite(j)) throw new Error(NOT_A_NUMBER);

    const x1 = axisTarget(hasX, valX, px, offsetX);
    const y1 = axisTarget(hasY, valY, py, offsetY);
    const z1 = axisTarget(hasZ, valZ, pz, offsetZ);
    const deltaE = extrusionDelta();

    const cx = px + i;
    const cy = py + j;
    const radius = Math.hypot(i, j);
    if (!(radius > 0)) throw new Error(ARC_NO_CENTRE);

    const startAngle = Math.atan2(py - cy, px - cx);
    let sweep: number;
    if (Math.abs(x1 - px) < Z_EPSILON && Math.abs(y1 - py) < Z_EPSILON) {
      // Same start and end point is a full circle, which is the one case an endpoint
      // test for "did this move anywhere" gets exactly backwards.
      sweep = clockwise ? -TWO_PI : TWO_PI;
    } else {
      const endAngle = Math.atan2(y1 - cy, x1 - cx);
      sweep = clockwise ? startAngle - endAngle : endAngle - startAngle;
      while (sweep <= 0) sweep += TWO_PI;
      if (clockwise) sweep = -sweep;
    }

    const maxAngle =
      radius > chordTolerance ? 2 * Math.acos(1 - chordTolerance / radius) : Math.PI;
    // A tight chord tolerance on a large radius turns one line into millions of
    // segments. There is no separate budget check here because `emit` refuses at the
    // cap anyway, which bounds the loop to the same number either way.
    const steps = Math.max(1, Math.ceil(Math.abs(sweep) / maxAngle));

    const extruding = deltaE > 0;
    if (extruding) openLayerFor(z1);
    // An arc is defined relative to where it starts, so a file that opens with one has
    // already told us the position is meaningful.
    positioned = true;

    let fromX = px;
    let fromY = py;
    let fromZ = pz;
    for (let step = 1; step <= steps; step++) {
      const t = step / steps;
      const angle = startAngle + sweep * t;
      // The last point comes from the file rather than from the angle, so an arc ends
      // exactly where the next move starts.
      const toX = step === steps ? x1 : cx + radius * Math.cos(angle);
      const toY = step === steps ? y1 : cy + radius * Math.sin(angle);
      const toZ = pz + (z1 - pz) * t;
      emit(extruding, fromX, fromY, fromZ, toX, toY, toZ);
      fromX = toX;
      fromY = toY;
      fromZ = toZ;
    }
    px = x1;
    py = y1;
    pz = z1;
  }

  function setPosition(): void {
    // `G92` states outright where the nozzle is, which is the other way a file can
    // establish a position it never homed to. Only an axis word does that, though:
    // Cura opens every file with a bare `G92 E0`, which says nothing about XY, and
    // taking it as a position drew a travel line from the origin to the first move.
    if (hasX || hasY || hasZ) positioned = true;
    if (hasX) {
      if (!Number.isFinite(valX)) throw new Error(NOT_A_NUMBER);
      offsetX = px - valX;
    }
    if (hasY) {
      if (!Number.isFinite(valY)) throw new Error(NOT_A_NUMBER);
      offsetY = py - valY;
    }
    if (hasZ) {
      if (!Number.isFinite(valZ)) throw new Error(NOT_A_NUMBER);
      offsetZ = pz - valZ;
    }
    if (hasE) {
      if (!Number.isFinite(valE)) throw new Error(NOT_A_NUMBER);
      eByTool[tool] = valE;
    }
  }

  function handleComment(payload: string): void {
    const trimmed = payload.trim();
    if (trimmed.length === 0) return;
    const upper = trimmed.toUpperCase();
    if (LAYER_MARKERS.has(upper) || (upper.startsWith('LAYER:') && upper.length > 6)) {
      pending = true;
      sawMarker = true;
      return;
    }
    if (upper.startsWith('Z:')) {
      const z = Number(trimmed.slice(2).trim());
      if (Number.isFinite(z)) announcedZ = z;
    }
  }

  /**
   * Decide, at the first extrusion of a run, whether it continues the open layer or
   * starts a new one.
   *
   * The decision is made here rather than when the marker arrives because a marker
   * fires while Z is still the old layer's: judging at marker time puts the next
   * layer's first extrusion into the previous layer. What separates a purge line from a
   * real preceding layer is that a purge line is at the first layer's Z, so that is the
   * test, and it is applied to the extrusion, not the marker.
   */
  function openLayerFor(z: number): void {
    if (openLayer === null) {
      startLayer(z);
      // A marker that arrived before anything extruded is spent on this layer rather
      // than left to split the next one.
      if (pending) {
        pending = false;
        anchor();
      }
      return;
    }
    if (pending) {
      pending = false;
      if (!anchored && Math.abs(z - openPhysicalZ) < Z_EPSILON) {
        // The first marker of the file, arriving over a run already at this Z: a purge
        // line, which belongs to layer one rather than to a layer of its own.
        anchor();
        purgeSegments = extrusionSegments;
        if (announcedZ !== null) {
          openLayer.z = announcedZ;
          announcedZ = null;
        }
        return;
      }
      if (!anchored) {
        anchor();
        purgeSegments = extrusionSegments;
      }
      closeLayer();
      startLayer(z);
      return;
    }
    // Markers are the slicer's opinion about where layers begin; a Z change is a guess.
    // Once any marker has been seen the guess is off.
    if (!sawMarker && Math.abs(z - openPhysicalZ) >= Z_EPSILON) {
      closeLayer();
      startLayer(z);
    }
  }

  /**
   * The first layer marker is the only evidence a file gives that the purge line is
   * over, so bounds accumulated before it are thrown away rather than never collected.
   * Collecting them is what makes a file with no markers at all measurable: it splits
   * layers on Z instead, never anchors, and gating the widen on `anchored` reported
   * every such print as 0 x 0 x 0 mm.
   */
  function anchor(): void {
    anchored = true;
    min[0] = min[1] = min[2] = Infinity;
    max[0] = max[1] = max[2] = -Infinity;
  }

  function startLayer(z: number): void {
    if (layers.length >= layerCap) throw new Error(tooManyLayers(layerCap));
    openPhysicalZ = z;
    openLayer = { z: announcedZ ?? z, extrusionEnd: 0, travelEnd: 0 };
    announcedZ = null;
    layers.push(openLayer);
  }

  function closeLayer(): void {
    if (openLayer === null) return;
    openLayer.extrusionEnd = extrusionSegments;
    openLayer.travelEnd = travelSegments;
    openLayer = null;
  }

  function emit(
    extruding: boolean,
    x0: number,
    y0: number,
    z0: number,
    x1: number,
    y1: number,
    z1: number,
  ): void {
    if (extrusionSegments + travelSegments >= segmentCap) {
      throw new Error(tooManySegments(segmentCap));
    }
    const chunks = extruding ? extrusion : travel;
    const count = extruding ? extrusionSegments : travelSegments;
    const slot = count % chunkSegments;
    if (slot === 0) chunks.push(new Float32Array(chunkSegments * FLOATS_PER_SEGMENT));
    const buffer = chunks[chunks.length - 1];
    const at = slot * FLOATS_PER_SEGMENT;
    buffer[at] = x0;
    buffer[at + 1] = y0;
    buffer[at + 2] = z0;
    buffer[at + 3] = x1;
    buffer[at + 4] = y1;
    buffer[at + 5] = z1;
    if (extruding) extrusionSegments++;
    else travelSegments++;

    // Travel is left out because it is where the nozzle went, not where the print is -
    // a wipe to the back of the bed would report the bed's depth as the model's. Purge
    // is left out for the same reason: PrusaSlicer's runs the full width of the plate.
    if (extruding) widen(x0, y0, z0, x1, y1, z1);
  }

  function widen(
    x0: number,
    y0: number,
    z0: number,
    x1: number,
    y1: number,
    z1: number,
  ): void {
    if (x0 < min[0]) min[0] = x0;
    if (y0 < min[1]) min[1] = y0;
    if (z0 < min[2]) min[2] = z0;
    if (x0 > max[0]) max[0] = x0;
    if (y0 > max[1]) max[1] = y0;
    if (z0 > max[2]) max[2] = z0;
    if (x1 < min[0]) min[0] = x1;
    if (y1 < min[1]) min[1] = y1;
    if (z1 < min[2]) min[2] = z1;
    if (x1 > max[0]) max[0] = x1;
    if (y1 > max[1]) max[1] = y1;
    if (z1 > max[2]) max[2] = z1;
  }

  return { push, finish };
}

/**
 * The chunk list with the first `segments` segments left out, for the camera fit.
 *
 * Returned as views rather than copies, because the thing being trimmed is the largest
 * allocation in the app.
 */
export function skipSegments(
  chunks: readonly Float32Array[],
  segments: number,
): Float32Array[] {
  let left = segments;
  const out: Float32Array[] = [];
  for (const chunk of chunks) {
    const inChunk = chunk.length / FLOATS_PER_SEGMENT;
    if (left >= inChunk) {
      left -= inChunk;
      continue;
    }
    out.push(left > 0 ? chunk.subarray(left * FLOATS_PER_SEGMENT) : chunk);
    left = 0;
  }
  return out;
}

/**
 * How many segments to draw for layers `0..index` inclusive.
 *
 * Here rather than beside the renderer because this is the one number the scrub slider
 * is, and off-by-one in it is the likeliest bug in the viewer - the canvas would look
 * plausible either way. Out-of-range indices clamp rather than throw: the slider is
 * driven by a number input the user can type into.
 */
export function layerRange(
  toolpath: Toolpath,
  index: number,
  end: 'extrusionEnd' | 'travelEnd',
): number {
  if (toolpath.layers.length === 0) return 0;
  const clamped = Math.min(Math.max(Math.floor(index), 0), toolpath.layers.length - 1);
  return toolpath.layers[clamped][end];
}

/**
 * Split a segment budget across consecutive chunks: full chunks take their whole size,
 * the chunk the boundary falls inside takes part of itself, and the rest take nothing.
 */
export function spreadRange(sizes: readonly number[], total: number): number[] {
  let remaining = total;
  return sizes.map((size) => {
    const drawn = Math.min(Math.max(remaining, 0), size);
    remaining -= size;
    return drawn;
  });
}
