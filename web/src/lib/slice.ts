import type { components } from './api/schema';

export type SliceMeta = components['schemas']['Meta'];
export type SliceRow = { label: string; value: string };

/**
 * The slice settings panel, turned from what the slicer wrote into what design
 * 1c draws.
 *
 * All of it happens here rather than on the server, because the API stores what
 * the file said - millimetres, seconds, a bare cost with no currency - and this
 * is the only place that has an opinion about how a person reads it.
 *
 * Every row is dropped when the slicer did not write it. A row reading "—" is a
 * blank the reader has to interpret; an absent row says the same thing and says
 * it faster.
 */
export function sliceRows(meta: SliceMeta): SliceRow[] {
  const rows: SliceRow[] = [];
  const add = (label: string, value: string | null) => {
    if (value !== null) rows.push({ label, value });
  };

  const layer = num(meta.layerHeightMm);
  const walls = num(meta.wallLoops);
  const cost = num(meta.filamentCost);
  const speed = num(meta.maxVolumetricSpeed);

  add('Layer height', layer === null ? null : `${layerHeight(layer)} mm`);
  add('Infill', infill(meta));
  add('Wall loops', walls === null ? null : String(walls));
  add(...pair('Top', 'bottom', meta.topLayers, meta.bottomLayers, (v) => String(v)));
  add(...pair('Nozzle', 'bed', meta.nozzleTempC, meta.bedTempC, (v) => `${round(v)} °C`));
  add('Print time', duration(meta.printTimeSeconds));
  add('Filament', filament(meta));
  add('Est. cost', cost === null ? null : `$${cost.toFixed(2)}`);
  add('Supports', meta.supports === undefined ? null : meta.supports ? 'enabled' : 'none');
  add('Printer', meta.printerModel || null);
  add('Max volumetric speed', speed === null ? null : `${round(speed)} mm³/s`);

  return rows;
}

/**
 * How many settings the file gave up.
 *
 * Not the row count. Rows pair settings that read better together - top and
 * bottom layers share one, so do nozzle and bed, and grams, length and material
 * share a third - so a full OrcaSlicer file is sixteen settings arranged into
 * eleven rows. Every one of the sixteen is on the panel, which is why the larger
 * number is the honest one: nothing is being withheld, and reporting eleven
 * would undercount what was read.
 *
 * Counted from the same values sliceRows renders, rather than from the object's
 * keys, so a field the panel does not draw could never inflate it.
 */
export function fieldCount(meta: SliceMeta): number {
  const present = [
    num(meta.layerHeightMm),
    num(meta.infillPercent),
    meta.infillPattern,
    num(meta.wallLoops),
    num(meta.topLayers),
    num(meta.bottomLayers),
    num(meta.nozzleTempC),
    num(meta.bedTempC),
    num(meta.printTimeSeconds),
    num(meta.filamentGrams),
    num(meta.filamentMm),
    meta.filamentType,
    num(meta.filamentCost),
    num(meta.maxVolumetricSpeed),
    meta.printerModel,
    meta.supports
  ];
  return present.filter((v) => v !== null && v !== undefined && v !== '').length;
}

/** "OrcaSlicer 2.1.1", or just the name when the file carried no version. */
export function detectedSlicer(meta: SliceMeta): string {
  return [meta.slicer, meta.slicerVersion].filter(Boolean).join(' ');
}

/**
 * Layer height needs more precision than the rest. Two decimals is what the
 * design shows and what covers almost every print, but 0.125 mm is a real
 * setting and rounding it to "0.13 mm" would report a height nobody sliced at.
 */
function layerHeight(mm: number): string {
  const two = mm.toFixed(2);
  return Number(two) === mm ? two : String(Number(mm.toFixed(3)));
}

/** "15% gyroid", or whichever half of it the file has. */
function infill(meta: SliceMeta): string | null {
  const percent = num(meta.infillPercent);
  const parts: string[] = [];
  if (percent !== null) parts.push(`${round(percent)}%`);
  if (meta.infillPattern) parts.push(meta.infillPattern);
  return parts.length ? parts.join(' ') : null;
}

/**
 * "184.2 g · 61.7 m · PETG".
 *
 * Length switches to millimetres below a metre, because an 8 mm purge line
 * rendered as "0.0 m" reads as nothing at all - and one of the real test
 * fixtures is exactly that.
 */
function filament(meta: SliceMeta): string | null {
  const grams = num(meta.filamentGrams);
  const mm = num(meta.filamentMm);
  const parts: string[] = [];
  if (grams !== null) parts.push(`${grams.toFixed(1)} g`);
  if (mm !== null) parts.push(mm >= 1000 ? `${(mm / 1000).toFixed(1)} m` : `${round(mm)} mm`);
  if (meta.filamentType) parts.push(meta.filamentType);
  return parts.length ? parts.join(' · ') : null;
}

/**
 * "9 h 42 m", "42 m", "35 s". Two units at most: a print time given to the
 * second is precision the slicer's own estimate does not have.
 */
function duration(seconds: number | undefined): string | null {
  const value = num(seconds);
  if (value === null || value < 0) return null;

  const total = Math.round(value);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;

  if (h > 0) return m > 0 ? `${h} h ${m} m` : `${h} h`;
  if (m > 0) return s > 0 ? `${m} m ${s} s` : `${m} m`;
  return `${s} s`;
}

/**
 * Two settings that share a row, and the label has to shrink when only one of
 * them is known: "Top / bottom" over a lone "5" invites the reader to work out
 * which one it is.
 */
function pair(
  first: string,
  second: string,
  a: number | undefined,
  b: number | undefined,
  render: (v: number) => string
): [string, string | null] {
  const left = num(a);
  const right = num(b);
  if (left !== null && right !== null) {
    return [`${first} / ${second}`, `${render(left)} / ${render(right)}`];
  }
  if (left !== null) return [first, render(left)];
  if (right !== null) return [second[0].toUpperCase() + second.slice(1), render(right)];
  return [first, null];
}

/**
 * Absent means absent, and zero does not. An unheated bed is 0 °C and a vase has
 * 0 top layers; both are settings the file states, so `!value` would hide them.
 * A non-finite number can only arrive from a hand-edited payload, and is treated
 * as absent rather than rendered as "NaN mm".
 */
function num(value: number | undefined): number | null {
  return value === undefined || !Number.isFinite(value) ? null : value;
}

/** Temperatures and speeds are whole numbers on the panel; nothing is gained by
 *  "245.0 °C". */
function round(value: number): string {
  return String(Math.round(value));
}
