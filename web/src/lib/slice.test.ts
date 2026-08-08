import { describe, expect, it } from 'vitest';
import { detectedSlicer, fieldCount, sliceRows, type SliceMeta } from './slice';

/**
 * The values here are what the Go parser really produced from the real slicer
 * fixtures in internal/gcode/testdata, so these tests are about the last step -
 * whether a person can read the result - rather than about invented numbers.
 */
const orca: SliceMeta = {
  slicer: 'OrcaSlicer',
  slicerVersion: '2.3.2-dev',
  layerHeightMm: 0.2,
  infillPercent: 20,
  infillPattern: 'crosshatch',
  wallLoops: 2,
  topLayers: 4,
  bottomLayers: 3,
  nozzleTempC: 210,
  bedTempC: 55,
  printTimeSeconds: 365,
  filamentGrams: 1.12,
  filamentMm: 375.61,
  filamentType: 'PLA',
  filamentCost: 0.02,
  maxVolumetricSpeed: 12,
  printerModel: 'IdeaFormer IR3 V2',
  supports: false
};

const value = (meta: SliceMeta, label: string) =>
  sliceRows(meta).find((row) => row.label === label)?.value;

describe('slice settings rows', () => {
  it('renders a full file the way design 1c draws it', () => {
    expect(sliceRows(orca)).toEqual([
      { label: 'Layer height', value: '0.20 mm' },
      { label: 'Infill', value: '20% crosshatch' },
      { label: 'Wall loops', value: '2' },
      { label: 'Top / bottom', value: '4 / 3' },
      { label: 'Nozzle / bed', value: '210 °C / 55 °C' },
      { label: 'Print time', value: '6 m 5 s' },
      { label: 'Filament', value: '1.1 g · 376 mm · PLA' },
      { label: 'Est. cost', value: '$0.02' },
      { label: 'Supports', value: 'none' },
      { label: 'Printer', value: 'IdeaFormer IR3 V2' },
      { label: 'Max volumetric speed', value: '12 mm³/s' }
    ]);
  });

  /**
   * Cura writes three fields where Orca writes sixteen. Without this, a panel
   * that quietly rendered "—" for the thirteen it does not have would look
   * identical to one that had them, and the reader could not tell which
   * settings the file actually states.
   */
  it('drops every row the slicer did not write', () => {
    const cura: SliceMeta = {
      slicer: 'Cura',
      slicerVersion: 'master',
      layerHeightMm: 0.1,
      printTimeSeconds: 35,
      filamentMm: 8.29066
    };

    expect(sliceRows(cura)).toEqual([
      { label: 'Layer height', value: '0.10 mm' },
      { label: 'Print time', value: '35 s' },
      { label: 'Filament', value: '8 mm' }
    ]);
  });

  /**
   * Zero is a setting, not a gap. A vase has no top layers and a bedslinger
   * without a heater sits at 0 °C, and a falsiness check would hide both - which
   * is the same as claiming the file never said.
   */
  it('keeps zero values', () => {
    expect(value({ topLayers: 0, bottomLayers: 3 }, 'Top / bottom')).toBe('0 / 3');
    expect(value({ nozzleTempC: 215, bedTempC: 0 }, 'Nozzle / bed')).toBe('215 °C / 0 °C');
    expect(value({ wallLoops: 0 }, 'Wall loops')).toBe('0');
  });

  /**
   * "Top / bottom: 5" leaves the reader guessing which of the two it is, so a
   * half-known pair narrows its own label.
   */
  it('narrows a shared label when only one half is known', () => {
    expect(sliceRows({ topLayers: 5 })).toEqual([{ label: 'Top', value: '5' }]);
    expect(sliceRows({ bottomLayers: 4 })).toEqual([{ label: 'Bottom', value: '4' }]);
    expect(sliceRows({ nozzleTempC: 245 })).toEqual([{ label: 'Nozzle', value: '245 °C' }]);
    expect(sliceRows({ bedTempC: 80 })).toEqual([{ label: 'Bed', value: '80 °C' }]);
  });

  /**
   * 0.125 mm is a layer height people really print at. Two decimals everywhere
   * would render it as "0.13 mm" - a number nobody sliced at, and one that
   * cannot be typed back into a slicer to reproduce the print.
   */
  it('keeps a third decimal on layer height only when it carries information', () => {
    expect(value({ layerHeightMm: 0.2 }, 'Layer height')).toBe('0.20 mm');
    expect(value({ layerHeightMm: 0.125 }, 'Layer height')).toBe('0.125 mm');
    expect(value({ layerHeightMm: 0.08 }, 'Layer height')).toBe('0.08 mm');
  });

  /**
   * Filament length crosses units at a metre. A purge line of a few millimetres
   * rendered as "0.0 m" says nothing, and one of the real fixtures is exactly
   * that.
   */
  it('switches filament length to millimetres below a metre', () => {
    expect(value({ filamentMm: 61700 }, 'Filament')).toBe('61.7 m');
    expect(value({ filamentMm: 1000 }, 'Filament')).toBe('1.0 m');
    expect(value({ filamentMm: 999 }, 'Filament')).toBe('999 mm');
    expect(value({ filamentMm: 8.29 }, 'Filament')).toBe('8 mm');
  });

  /** Two units at most: a slicer's own estimate is not accurate to the second,
   *  so "9 h 42 m 17 s" is three digits of false precision. */
  it('renders print time to two units', () => {
    expect(value({ printTimeSeconds: 34920 }, 'Print time')).toBe('9 h 42 m');
    expect(value({ printTimeSeconds: 7200 }, 'Print time')).toBe('2 h');
    expect(value({ printTimeSeconds: 2520 }, 'Print time')).toBe('42 m');
    expect(value({ printTimeSeconds: 725 }, 'Print time')).toBe('12 m 5 s');
    expect(value({ printTimeSeconds: 35 }, 'Print time')).toBe('35 s');
    expect(value({ printTimeSeconds: 0 }, 'Print time')).toBe('0 s');
  });

  it('says whether supports were on, either way', () => {
    expect(value({ supports: true }, 'Supports')).toBe('enabled');
    expect(value({ supports: false }, 'Supports')).toBe('none');
    expect(value({}, 'Supports')).toBeUndefined();
  });

  /**
   * The API is typed, but the type is a promise about a server we could be
   * talking to over a broken proxy or an old cached build. A NaN reaching the
   * panel would render as "NaN mm", which reads as a bug in the printer rather
   * than in the payload.
   */
  it('treats non-finite numbers as absent', () => {
    expect(sliceRows({ layerHeightMm: NaN, wallLoops: Infinity })).toEqual([]);
  });
});

describe('field count', () => {
  /**
   * The number in the footer is settings read, not rows drawn, and the two are
   * genuinely different: this OrcaSlicer file has sixteen settings and eleven
   * rows, because top/bottom, nozzle/bed and the three filament values each
   * share one. Pinning both numbers is what stops the count quietly becoming
   * rows.length again - on a sparse file like Cura's they happen to agree.
   */
  it('counts settings, not rows', () => {
    expect(fieldCount(orca)).toBe(16);
    expect(sliceRows(orca)).toHaveLength(11);
  });

  it('counts nothing for an empty file, and skips a non-finite value', () => {
    expect(fieldCount({})).toBe(0);
    expect(fieldCount({ slicer: 'Cura' })).toBe(0);
    expect(fieldCount({ layerHeightMm: NaN })).toBe(0);
  });

  // Zero again: a vase's 0 top layers is a setting that was read.
  it('counts a zero', () => {
    expect(fieldCount({ topLayers: 0, supports: false })).toBe(2);
  });
});

describe('detected slicer', () => {
  it('joins the name and version, and copes without a version', () => {
    expect(detectedSlicer(orca)).toBe('OrcaSlicer 2.3.2-dev');
    expect(detectedSlicer({ slicer: 'Cura' })).toBe('Cura');
  });
});
