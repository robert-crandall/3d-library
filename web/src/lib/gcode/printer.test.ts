import { describe, expect, it } from 'vitest';
import {
  DEFAULT_FILAMENT,
  DEFAULT_VOLUME,
  formatPrinter,
  MAX_LIGHTNESS,
  MIN_LIGHTNESS,
  resolvePrinter,
  toolpathColor,
} from './printer';

describe('resolvePrinter', () => {
  it('prefers the bed the file itself declares', () => {
    // PrusaSlicer, SuperSlicer and OrcaSlicer all write `bed_shape` and
    // `max_print_height`, and that beats any table because it is what the slicer was
    // set to - including a bed the owner has modified.
    const printer = resolvePrinter({
      printerModel: 'Bambu Lab X1 Carbon',
      buildVolume: { minXMm: 0, minYMm: 0, maxXMm: 300, maxYMm: 300, heightMm: 400 },
    });
    expect(printer.volume).toEqual({ x: 300, y: 300, z: 400 });
    expect(printer.known).toBe(true);
  });

  it('falls back to the table for a printer that names itself but declares no bed', () => {
    // This is the whole reason the table exists: Bambu Studio writes
    // `printer_model = Bambu Lab X1 Carbon` and no bed shape at all, which is the case
    // in this repo's own Go fixtures.
    const printer = resolvePrinter({ printerModel: 'Bambu Lab X1 Carbon' });
    expect(printer.volume).toEqual({ x: 256, y: 256, z: 256 });
    expect(printer.known).toBe(true);
    expect(printer.model).toBe('Bambu Lab X1 Carbon');
  });

  it.each([
    { model: 'Bambu Lab X1 Carbon', volume: { x: 256, y: 256, z: 256 } },
    { model: 'Bambu Lab P1S', volume: { x: 256, y: 256, z: 256 } },
    { model: 'Bambu Lab X1E', volume: { x: 256, y: 256, z: 256 } },
    { model: 'Bambu Lab A1', volume: { x: 256, y: 256, z: 256 } },
    // The one Bambu that is not a 256 cube, and its name contains `A1`, so the order
    // the table is searched in is load-bearing.
    { model: 'Bambu Lab A1 mini', volume: { x: 180, y: 180, z: 180 } },
    { model: 'bambu lab a1 mini', volume: { x: 180, y: 180, z: 180 } },
  ])('$model', ({ model, volume }) => {
    expect(resolvePrinter({ printerModel: model }).volume).toEqual(volume);
  });

  it('does not match a model that merely contains the letters', () => {
    // `known: false` is the difference between a grid the file supports and a guess,
    // and guessing is the one thing acceptance criterion 4 rules out.
    const printer = resolvePrinter({ printerModel: 'Prusa MK4' });
    expect(printer.volume).toEqual(DEFAULT_VOLUME);
    expect(printer.known).toBe(false);
    expect(printer.model).toBe('Prusa MK4');
  });

  it('reports a file that says nothing as unknown', () => {
    // Cura, in this repo's fixtures: no printer model and no bed shape.
    const printer = resolvePrinter(undefined);
    expect(printer.volume).toEqual(DEFAULT_VOLUME);
    expect(printer.known).toBe(false);
    expect(printer.model).toBeUndefined();
  });

  it('treats a blank printer model as no printer model', () => {
    expect(resolvePrinter({ printerModel: '   ' }).model).toBeUndefined();
  });
});

describe('formatPrinter', () => {
  it.each([
    {
      name: 'a detected cube',
      meta: { printerModel: 'Bambu Lab X1 Carbon' },
      text: 'Bambu Lab X1 Carbon · 256³ build volume',
    },
    {
      name: 'a detected bed that is not a cube',
      meta: {
        printerModel: 'Prusa XL',
        buildVolume: { minXMm: 0, minYMm: 0, maxXMm: 360, maxYMm: 360, heightMm: 360.5 },
      },
      text: 'Prusa XL · 360 × 360 × 360.5 mm build volume',
    },
    {
      name: 'a named printer with no bed anyone knows',
      meta: { printerModel: 'Homebrew CoreXY' },
      text: 'Homebrew CoreXY · 220³ build volume assumed',
    },
    {
      name: 'a file that names no printer',
      meta: undefined,
      text: 'Unknown printer · 220³ build volume assumed',
    },
  ])('$name', ({ meta, text }) => {
    expect(formatPrinter(resolvePrinter(meta))).toBe(text);
  });
});

describe('toolpathColor', () => {
  it('keeps a colour that is already legible exactly as declared', () => {
    // The point of matching the filament colour is that a reader can check it against
    // the spool, so a colour inside the band must come through untouched.
    expect(toolpathColor('#ff6600')).toBe(0xff6600);
  });

  it.each([
    { name: 'black', declared: '#000000' },
    { name: 'white', declared: '#ffffff' },
  ])('lifts $name into the legible band', ({ declared }) => {
    // The renderer clears to transparent, so the panel background shows through - and
    // that is near-white in one theme and near-black in the other. Black filament is
    // the most common there is; drawn as black it is invisible in the dark theme.
    // The tolerance is one 8-bit step: the band is computed in floats and then packed
    // into a `0xrrggbb`, so the result can land a fraction outside it.
    const step = 1 / 255;
    const lightness = lightnessOf(toolpathColor(declared));
    expect(lightness).toBeGreaterThanOrEqual(MIN_LIGHTNESS - step);
    expect(lightness).toBeLessThanOrEqual(MAX_LIGHTNESS + step);
  });

  it('keeps the hue when it lifts a colour', () => {
    // Substituting a default would lose the one part of "filament color matching"
    // anyone can verify. A very dark red must still come out red.
    const color = toolpathColor('#220000');
    const r = (color >> 16) & 255;
    const g = (color >> 8) & 255;
    const b = color & 255;
    expect(r).toBeGreaterThan(g + 60);
    expect(r).toBeGreaterThan(b + 60);
  });

  it.each([
    { name: 'nothing declared', declared: undefined },
    { name: 'an empty string', declared: '' },
    { name: 'a name rather than a hex value', declared: 'green' },
    { name: 'a three digit shorthand', declared: '#abc' },
    { name: 'seven digits', declared: '#1234567' },
    { name: 'a digit that is not hex', declared: '#gg0000' },
  ])('falls back to a readable grey for $name', ({ declared }) => {
    expect(toolpathColor(declared)).toBe(toolpathColor(DEFAULT_FILAMENT));
  });

  it('drops the alpha of an eight digit colour', () => {
    // PrusaSlicer writes `#RRGGBBAA`. Reading the alpha as part of the colour shifts
    // the hue; honouring it would draw the toolpaths translucent, which reads as a
    // different filament rather than as the one declared.
    expect(toolpathColor('#ff660080')).toBe(toolpathColor('#ff6600'));
  });

  it('accepts upper case and surrounding space', () => {
    expect(toolpathColor('  #FF6600 ')).toBe(0xff6600);
  });

  it('measures a bed that does not start at the origin', () => {
    // A delta's bed_shape is centred on the origin, so reading maxX as the width would
    // report half of it. The server passes the corners through for exactly this reason.
    const printer = resolvePrinter({
      buildVolume: { minXMm: -105, minYMm: -105, maxXMm: 105, maxYMm: 105, heightMm: 300 },
    });
    expect(printer.volume).toEqual({ x: 210, y: 210, z: 300 });
  });

});

/** Relative lightness in the same sense `printer.ts` clamps it: the HSL L channel. */
function lightnessOf(color: number): number {
  const r = ((color >> 16) & 255) / 255;
  const g = ((color >> 8) & 255) / 255;
  const b = (color & 255) / 255;
  return (Math.max(r, g, b) + Math.min(r, g, b)) / 2;
}
