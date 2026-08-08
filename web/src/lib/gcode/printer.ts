/*
  Where the build-volume grid and the toolpath colour come from.

  Both are pure lookups over what milestone 4's parser stored, kept out of the viewer so
  they can be tested without a GL context - which matters because the failure modes are
  "invented a build volume nobody has" and "drew the print in a colour nobody can see",
  neither of which a canvas would reveal.
*/

import type { Bounds } from '$lib/viewer/framing';

export type Volume = {
  /** Bed width, depth and maximum print height, in millimetres. */
  readonly x: number;
  readonly y: number;
  readonly z: number;
  /**
   * The bed's front-left corner in the printer's own coordinates. Usually the origin,
   * but a delta's bed is centred on it and runs negative, and drawing that bed from 0
   * puts the print in a corner of a box it is nowhere near.
   */
  readonly originX: number;
  readonly originY: number;
};

type DeclaredBed = {
  readonly minXMm: number;
  readonly minYMm: number;
  readonly maxXMm: number;
  readonly maxYMm: number;
  readonly heightMm: number;
};

export type PrinterMeta = {
  readonly printerModel?: string;
  readonly buildVolume?: DeclaredBed;
  readonly filamentColor?: string;
};

export type Printer = {
  readonly volume: Volume;
  /** The printer's own name, or undefined when the file did not say. */
  readonly model?: string;
  /** False when the volume is the fallback rather than anything the file supports. */
  readonly known: boolean;
};

/**
 * A 220 mm cube: an i3-class bed, the most common size there is. Used only when a file
 * says nothing about its printer, and reported as unknown rather than as a detection.
 */
export const DEFAULT_VOLUME: Volume = { x: 220, y: 220, z: 220, originX: 0, originY: 0 };

/**
 * Build volumes for printers whose slicer names the model but never writes a bed shape.
 *
 * This exists for exactly one reason: Bambu Studio does that. Every other slicer this
 * app has a fixture for - PrusaSlicer, SuperSlicer, OrcaSlicer - writes `bed_shape` and
 * `max_print_height` into its config block, and the parser reads them, so their volume
 * comes from the file itself and needs no table.
 *
 * The table is therefore deliberately short. Adding printers whose files already carry
 * their bed shape would be dead weight, and adding printers from memory would risk the
 * one thing acceptance criterion 4 rules out: inventing a build volume rather than
 * admitting the printer is unknown.
 */
const VOLUMES: ReadonlyArray<readonly [pattern: RegExp, size: Omit<Volume, 'originX' | 'originY'>]> =
  [
    // The mini is the only Bambu that is not a 256 cube, so it has to be matched first.
    [/\ba1\s*mini\b/, { x: 180, y: 180, z: 180 }],
    // One trailing letter, because the range is X1, X1C, X1E, P1P, P1S and A1: a plain
    // word boundary after `p1` does not match `P1S`.
    [/\b(x1|p1|a1)[a-z]?\b/, { x: 256, y: 256, z: 256 }],
  ];

/** What grid to draw, and what to say about it. */
export function resolvePrinter(meta: PrinterMeta | undefined): Printer {
  const model = meta?.printerModel?.trim() || undefined;

  // The file's own configuration beats any table: it is what the slicer was actually
  // set to, including a bed the owner has modified.
  const declared = meta?.buildVolume;
  if (declared && plausible(declared)) {
    return {
      // The server reports the bed's corners rather than its size, because a bed_shape
      // does not have to start at the origin - a delta's is centred on it.
      volume: {
        x: declared.maxXMm - declared.minXMm,
        y: declared.maxYMm - declared.minYMm,
        z: declared.heightMm,
        originX: declared.minXMm,
        originY: declared.minYMm,
      },
      model,
      known: true,
    };
  }

  if (model) {
    const needle = model.toLowerCase();
    for (const [pattern, size] of VOLUMES) {
      // Every printer in the table is a cartesian bed cornered at the origin; the ones
      // that are not declare their own bed shape and never reach here.
      if (pattern.test(needle)) {
        return { volume: { ...size, originX: 0, originY: 0 }, model, known: true };
      }
    }
  }

  return { volume: DEFAULT_VOLUME, model, known: false };
}

/**
 * Ten metres, which is not a printer. The server already rejects a bed_shape that is
 * not finite, but `1e300` is finite, and a hand-edited profile is where such a number
 * would come from. The plate goes to the GPU as Float32, so anything past ~3.4e38
 * becomes Infinity there and takes the camera fit to NaN - a blank panel with nothing
 * said. Falling back to the assumed volume at least draws the print.
 */
const MAX_BED_MM = 10_000;

function plausible(bed: DeclaredBed): boolean {
  const x = bed.maxXMm - bed.minXMm;
  const y = bed.maxYMm - bed.minYMm;
  return (
    x > 0 &&
    y > 0 &&
    bed.heightMm > 0 &&
    x <= MAX_BED_MM &&
    y <= MAX_BED_MM &&
    bed.heightMm <= MAX_BED_MM &&
    Math.abs(bed.minXMm) <= MAX_BED_MM &&
    Math.abs(bed.minYMm) <= MAX_BED_MM
  );
}

/**
 * The build volume to draw around a print, or nothing when it would be misinformation.
 *
 * The grid is only meaningful if the print sits inside it. A belt printer's does not:
 * it prints on a 45-degree belt where Y and Z are coupled, so the IdeaFormer fixture's
 * toolpaths run to `Z -990` while the same file declares a 250 mm-tall volume starting
 * at zero. Drawing that box puts a grid a metre away from the print - the camera frames
 * the extrusions alone, so the print itself is fine and the grid is simply somewhere
 * else, describing a coordinate frame this file is not using.
 *
 * Rejecting the box rather than transforming it into the belt's frame, because a grid is
 * an orientation aid and no grid is a perfectly good state - it is what an unrecognised
 * printer with no declared bed already gets. Drawing the belt's true frame is a feature,
 * and one with no way to test it: the scene needs a GPU. The check is on containment
 * rather than on a belt flag so that any other frame mismatch is caught too.
 *
 * A tolerance because a skirt or a brim legitimately prints outside the declared bed on
 * plenty of profiles, and a print one millimetre over the edge is still worth a grid.
 */
const OUTSIDE_TOLERANCE_MM = 25;

export function volumeFor(printer: Printer, bounds: Bounds | undefined): Volume | undefined {
  if (!bounds) return printer.volume;
  const { x, y, z, originX, originY } = printer.volume;
  const within =
    bounds.min[0] >= originX - OUTSIDE_TOLERANCE_MM &&
    bounds.max[0] <= originX + x + OUTSIDE_TOLERANCE_MM &&
    bounds.min[1] >= originY - OUTSIDE_TOLERANCE_MM &&
    bounds.max[1] <= originY + y + OUTSIDE_TOLERANCE_MM &&
    bounds.min[2] >= -OUTSIDE_TOLERANCE_MM &&
    bounds.max[2] <= z + OUTSIDE_TOLERANCE_MM;
  return within ? printer.volume : undefined;
}

/** `Bambu Lab X1 Carbon · 256³` - design 1c's lower readout line. */
export function formatPrinter(printer: Printer): string {
  const { x, y, z } = printer.volume;
  const size =
    x === y && y === z ? `${round(x)}³` : `${round(x)} × ${round(y)} × ${round(z)} mm`;
  if (!printer.model) return `Unknown printer · ${size} build volume assumed`;
  if (!printer.known) return `${printer.model} · ${size} build volume assumed`;
  return `${printer.model} · ${size} build volume`;
}

function round(value: number): number {
  return Math.round(value * 10) / 10;
}

/** Grey. Not a filament colour anyone loaded - a colour that reads as "we do not know". */
export const DEFAULT_FILAMENT = '#9aa3af';

/**
 * The lightness band a toolpath colour is held inside, as a fraction.
 *
 * The renderer clears to transparent so the panel's own background shows through, and
 * that background is near-white in the light theme and near-black in the dark one. A
 * colour has to be legible against both, so black filament - which is the single most
 * common filament there is - cannot be drawn as black, and white cannot be drawn as
 * white. Clamping lightness rather than substituting a default keeps the hue the file
 * declared, which is the part of "filament color matching" a reader can actually check.
 */
export const MIN_LIGHTNESS = 0.32;
export const MAX_LIGHTNESS = 0.72;

/**
 * A declared filament colour turned into one that will be visible, as `0xrrggbb`.
 *
 * Anything unparseable is the default rather than an error: a colour is decoration, and
 * refusing to draw a print over it would be the wrong trade.
 */
export function toolpathColor(declared: string | undefined): number {
  const rgb = parseHex(declared) ?? parseHex(DEFAULT_FILAMENT)!;
  const [h, s, l] = toHsl(rgb);
  const clamped = Math.min(MAX_LIGHTNESS, Math.max(MIN_LIGHTNESS, l));
  if (clamped === l) return pack(rgb);
  return pack(fromHsl(h, s, clamped));
}

type Rgb = readonly [r: number, g: number, b: number];

function parseHex(value: string | undefined): Rgb | null {
  if (!value) return null;
  const text = value.trim();
  // Six or eight digits: the alpha of an eight-digit colour is dropped, because the
  // toolpaths are opaque lines and a translucent one would read as a fainter filament.
  if (!/^#[0-9a-f]{6}([0-9a-f]{2})?$/i.test(text)) return null;
  const n = Number.parseInt(text.slice(1, 7), 16);
  return [((n >> 16) & 255) / 255, ((n >> 8) & 255) / 255, (n & 255) / 255];
}

function pack([r, g, b]: Rgb): number {
  const to255 = (v: number) => Math.max(0, Math.min(255, Math.round(v * 255)));
  return (to255(r) << 16) | (to255(g) << 8) | to255(b);
}

function toHsl([r, g, b]: Rgb): [number, number, number] {
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const l = (max + min) / 2;
  const span = max - min;
  if (span === 0) return [0, 0, l];
  const s = span / (1 - Math.abs(2 * l - 1));
  let h: number;
  if (max === r) h = ((g - b) / span) % 6;
  else if (max === g) h = (b - r) / span + 2;
  else h = (r - g) / span + 4;
  h *= 60;
  if (h < 0) h += 360;
  return [h, s, l];
}

function fromHsl(h: number, s: number, l: number): Rgb {
  const c = (1 - Math.abs(2 * l - 1)) * s;
  const x = c * (1 - Math.abs(((h / 60) % 2) - 1));
  const m = l - c / 2;
  const [r, g, b] =
    h < 60
      ? [c, x, 0]
      : h < 120
        ? [x, c, 0]
        : h < 180
          ? [0, c, x]
          : h < 240
            ? [0, x, c]
            : h < 300
              ? [x, 0, c]
              : [c, 0, x];
  return [r + m, g + m, b + m];
}
