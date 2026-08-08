import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import SliceSettings from './SliceSettings.svelte';
import type { SliceMeta } from '$lib/slice';

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

const cura: SliceMeta = {
  slicer: 'Cura',
  slicerVersion: 'master',
  layerHeightMm: 0.1,
  printTimeSeconds: 35,
  filamentMm: 8.29066
};

describe('slice settings panel', () => {
  it('names the file the settings came from', () => {
    render(SliceSettings, { meta: cura, filename: 'plate-1.gcode' });

    expect(screen.getByRole('heading', { name: 'Slice settings' })).toBeTruthy();
    // A model can hold several G-code files sliced differently. Without the
    // filename the panel is a claim about "the print" rather than about one
    // plate, and the reader has no way to tell which.
    expect(screen.getByText('from plate-1.gcode')).toBeTruthy();
  });

  /**
   * The footer count is the number of settings this file gave up, not design
   * 1c's fixed "all 24 fields". Cura yields three where OrcaSlicer yields
   * sixteen, so a constant would be wrong for almost every real file.
   *
   * The Orca case is here rather than the Cura one because Orca's sixteen
   * settings render as eleven rows, so a footer that had gone back to counting
   * rows would say eleven and this would catch it. On Cura's file the two
   * numbers are the same and it would not.
   */
  it('reports the slicer and how many settings it actually read', () => {
    render(SliceSettings, { meta: orca, filename: 'plate-1.gcode' });

    expect(screen.getByText('Detected slicer: OrcaSlicer 2.3.2-dev · 16 fields')).toBeTruthy();
    expect(screen.getAllByRole('term')).toHaveLength(11);
  });

  it('does not pluralise a single field', () => {
    render(SliceSettings, { meta: { slicer: 'Cura', layerHeightMm: 0.2 }, filename: 'a.gcode' });

    expect(screen.getByText('Detected slicer: Cura · 1 field')).toBeTruthy();
  });

  /**
   * Rows are a definition list, so a screen reader pairs each value with its
   * label. Rendered as plain divs the panel reads out as ten labels followed by
   * ten unattached numbers.
   */
  it('pairs each label with its value', () => {
    render(SliceSettings, { meta: cura, filename: 'plate-1.gcode' });

    const labels = screen.getAllByRole('term').map((el) => el.textContent);
    const values = screen.getAllByRole('definition').map((el) => el.textContent);

    expect(labels).toEqual(['Layer height', 'Print time', 'Filament']);
    expect(values).toEqual(['0.10 mm', '35 s', '8 mm']);
  });
});
