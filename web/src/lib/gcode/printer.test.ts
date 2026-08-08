import { describe, expect, it } from 'vitest';
import {
  DEFAULT_FILAMENT,
  DEFAULT_VOLUME,
  formatPrinter,
  MAX_LIGHTNESS,
  MIN_LIGHTNESS,
  resolvePrinter,
  toolpathColor,
  volumeFor,
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
    expect(printer.volume).toEqual({ x: 300, y: 300, z: 400, originX: 0, originY: 0 });
    expect(printer.known).toBe(true);
  });

  it('falls back to the table for a printer that names itself but declares no bed', () => {
    // This is the whole reason the table exists: Bambu Studio writes
    // `printer_model = Bambu Lab X1 Carbon` and no bed shape at all, which is the case
    // in this repo's own Go fixtures.
    const printer = resolvePrinter({ printerModel: 'Bambu Lab X1 Carbon' });
    expect(printer.volume).toEqual({ x: 256, y: 256, z: 256, originX: 0, originY: 0 });
    expect(printer.known).toBe(true);
    expect(printer.model).toBe('Bambu Lab X1 Carbon');
  });

  it.each([
    { model: 'Bambu Lab X1 Carbon', volume: { x: 256, y: 256, z: 256, originX: 0, originY: 0 } },
    { model: 'Bambu Lab P1S', volume: { x: 256, y: 256, z: 256, originX: 0, originY: 0 } },
    { model: 'Bambu Lab X1E', volume: { x: 256, y: 256, z: 256, originX: 0, originY: 0 } },
    { model: 'Bambu Lab A1', volume: { x: 256, y: 256, z: 256, originX: 0, originY: 0 } },
    // The one Bambu that is not a 256 cube, and its name contains `A1`, so the order
    // the table is searched in is load-bearing.
    { model: 'Bambu Lab A1 mini', volume: { x: 180, y: 180, z: 180, originX: 0, originY: 0 } },
    { model: 'bambu lab a1 mini', volume: { x: 180, y: 180, z: 180, originX: 0, originY: 0 } },
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
    //
    // The origin has to survive too, not just the extent: the scene draws the plate from
    // it, and a 210 mm bed drawn from 0 instead of -105 is a box the print sits outside.
    const printer = resolvePrinter({
      buildVolume: { minXMm: -105, minYMm: -105, maxXMm: 105, maxYMm: 105, heightMm: 300 },
    });
    expect(printer.volume).toEqual({ x: 210, y: 210, z: 300, originX: -105, originY: -105 });
  });

  it.each([
    { name: 'a bed wider than ten metres', bed: { maxXMm: 1e300 } },
    { name: 'a bed taller than ten metres', bed: { heightMm: 1e300 } },
    {
      name: 'an origin further than ten metres out',
      // A believable 250 mm bed, just parked past the limit: the width check alone lets
      // this through, so it is the origin guard or nothing.
      bed: { minXMm: 50_000, maxXMm: 50_250 },
    },
  ])('assumes a volume rather than believing $name', ({ bed }) => {
    // `1e300` is finite, so the server's own finite check passes it, but the plate goes
    // to the GPU as Float32 where anything past ~3.4e38 is Infinity - and the camera fit
    // then divides into NaN and the panel draws nothing, silently. A hand-edited printer
    // profile with a fat-fingered bed_shape is where such a number comes from.
    const printer = resolvePrinter({
      printerModel: 'Some Machine',
      buildVolume: { minXMm: 0, minYMm: 0, maxXMm: 250, maxYMm: 210, heightMm: 210, ...bed },
    });
    expect(printer.known).toBe(false);
    expect(printer.volume).toEqual(DEFAULT_VOLUME);
  });
});

/** Relative lightness in the same sense `printer.ts` clamps it: the HSL L channel. */
function lightnessOf(color: number): number {
  const r = ((color >> 16) & 255) / 255;
  const g = ((color >> 8) & 255) / 255;
  const b = (color & 255) / 255;
  return (Math.max(r, g, b) + Math.min(r, g, b)) / 2;
}

describe('volumeFor', () => {
  const printer = resolvePrinter({ printerModel: 'Bambu Lab X1 Carbon' });

  it('draws the grid around a print that sits on the bed', () => {
    const volume = volumeFor(printer, { min: [60, 60, 0.2], max: [110, 110, 48] });
    expect(volume).toEqual(printer.volume);
  });

  it('draws nothing when the print is nowhere near the declared volume', () => {
    // The belt printer, in its own numbers: the IdeaFormer fixture prints on a
    // 45-degree belt where Y and Z are coupled, so its toolpaths run to Y1010 and
    // Z-990 while the file still declares an ordinary box starting at zero. The camera
    // frames the extrusions, so the print draws correctly either way - the box is what
    // ends up a metre away, describing a frame the file is not using.
    const volume = volumeFor(printer, {
      min: [112.811, 987.811, -990.254],
      max: [137.189, 1010.289, -987.979],
    });
    expect(volume).toBeUndefined();
  });

  it('keeps the grid for a skirt printed just off the bed', () => {
    // Plenty of profiles put the skirt or the purge line past the declared edge, and a
    // print a few millimetres over is still worth an orientation aid. Only a mismatch
    // big enough to be a different coordinate system should lose it.
    const { x, y } = printer.volume;
    const volume = volumeFor(printer, { min: [-8, -8, 0.2], max: [x + 8, y + 8, 48] });
    expect(volume).toEqual(printer.volume);
  });

  it('draws nothing for a belt print that has not climbed far yet', () => {
    // The belt fixture is 990 mm down the belt, but the first few layers of the same
    // print are not. A tolerance loose enough for a skirt is far too loose below the
    // bed, where nothing legitimately prints at all.
    const volume = volumeFor(printer, { min: [112, 100, -10], max: [137, 130, -8] });
    expect(volume).toBeUndefined();
  });

  it('keeps the grid for a print taller than the volume it assumed', () => {
    // An unrecognised printer gets a 256-cubed guess, so a 300 mm print on a real 400 mm
    // machine is ordinary. It has not changed coordinate frames - the guess is just
    // short, and the bed outline is still where the bed is.
    const assumed = resolvePrinter({ printerModel: 'Some Machine Nobody Has Heard Of' });
    expect(assumed.known).toBe(false);
    const volume = volumeFor(assumed, { min: [60, 60, 0.2], max: [110, 110, 300] });
    expect(volume).toEqual(assumed.volume);
  });
});
