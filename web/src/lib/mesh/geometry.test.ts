import { describe, expect, it } from 'vitest';
import {
  boundsOf,
  centerOf,
  formatDimensions,
  formatObjectCount,
  frameCamera,
  sizeOf,
} from './geometry';

describe('boundsOf', () => {
  it('measures a box that is not at the origin', () => {
    // Two triangles spanning (10,20,30) to (30,50,80). A bounds implementation that
    // assumed the model sat at the origin - which the naive `max` version does - would
    // report the size as the max corner and pass every centred fixture.
    const positions = new Float32Array([
      10, 20, 30, 30, 50, 80, 10, 50, 30, 30, 20, 80, 10, 20, 30, 30, 50, 30,
    ]);
    const bounds = boundsOf(positions);
    expect(bounds.min).toEqual([10, 20, 30]);
    expect(bounds.max).toEqual([30, 50, 80]);
    expect(sizeOf(bounds)).toEqual([20, 30, 50]);
    expect(centerOf(bounds)).toEqual([20, 35, 55]);
  });

  it('spans every object, not just the first', () => {
    const positions = new Float32Array([
      0, 0, 0, 1, 1, 1, 0, 1, 0,
      // A second object 100 mm along X. Bounds taken per-object and then discarded
      // would report a 1 mm model.
      100, 0, 0, 101, 1, 1, 100, 1, 0,
    ]);
    expect(sizeOf(boundsOf(positions))).toEqual([101, 1, 1]);
  });

  it('handles negative coordinates', () => {
    const positions = new Float32Array([-5, -5, -5, 5, 5, 5, -5, 5, -5]);
    expect(sizeOf(boundsOf(positions))).toEqual([10, 10, 10]);
  });

  it('refuses a mesh with no triangles', () => {
    expect(() => boundsOf(new Float32Array([]))).toThrow(/no triangles/);
  });
});

/**
 * Is every corner of the model inside the view frustum, seen from `distance` along an
 * arbitrary axis? The geometry is centred on the origin, so this only needs the size.
 *
 * Checked in camera space: a corner is visible when it is in front of the camera and
 * within the half-angles the FOV and aspect allow.
 */
function contains(
  size: [number, number, number],
  fovDegrees: number,
  aspect: number,
  distance: number,
): boolean {
  const vFov = (fovDegrees * Math.PI) / 180;
  const tanV = Math.tan(vFov / 2);
  const tanH = tanV * aspect;
  for (const sx of [-1, 1]) {
    for (const sy of [-1, 1]) {
      for (const sz of [-1, 1]) {
        // Worst case: the camera looks down one axis, so a corner's depth shrinks by its
        // own offset and its lateral offsets are the other two half-extents.
        const depth = distance - (sz * size[2]) / 2;
        if (depth <= 0) return false;
        if (Math.abs((sx * size[0]) / 2) > tanH * depth) return false;
        if (Math.abs((sy * size[1]) / 2) > tanV * depth) return false;
      }
    }
  }
  return true;
}

describe('frameCamera', () => {
  const cases: Array<{ name: string; size: [number, number, number] }> = [
    { name: 'a printer-plate model', size: [220, 180, 145] },
    { name: 'a 1 mm part', size: [1, 1, 1] },
    { name: 'a 300 mm print', size: [300, 300, 300] },
    { name: 'a flat sheet', size: [200, 200, 0.4] },
    { name: 'a tall thin rod', size: [2, 2, 250] },
    { name: 'a wide flat bar', size: [400, 3, 3] },
  ];
  const aspects = [0.5, 1, 16 / 9, 3];

  for (const { name, size } of cases) {
    for (const aspect of aspects) {
      it(`fits ${name} at aspect ${aspect}`, () => {
        const framing = frameCamera(size, 45, aspect);
        expect(contains(size, 45, aspect, framing.distance)).toBe(true);

        // The clip planes have to bracket the whole zoom range, not just the opening
        // shot. Planes fitted to `distance` alone clip the model the moment the user
        // scrolls out, which is the bug this asserts against.
        expect(framing.far).toBeGreaterThan(
          framing.maxDistance + Math.hypot(...size) / 2,
        );
        expect(framing.near).toBeGreaterThan(0);
        expect(framing.near).toBeLessThan(framing.minDistance);
        expect(framing.minDistance).toBeLessThan(framing.distance);
        expect(framing.distance).toBeLessThan(framing.maxDistance);
      });
    }
  }

  it('is fully zoomed out at maxDistance without clipping', () => {
    const size: [number, number, number] = [220, 180, 145];
    const framing = frameCamera(size, 45, 16 / 9);
    expect(contains(size, 45, 16 / 9, framing.maxDistance)).toBe(true);
    expect(framing.far).toBeGreaterThan(framing.maxDistance + 145);
  });

  it('keeps the same depth precision for a tiny and a huge model', () => {
    const small = frameCamera([1, 1, 1], 45, 1);
    const large = frameCamera([300, 300, 300], 45, 1);
    // A fixed near plane would give a 1 mm model a far/near ratio thousands of times
    // worse than a 300 mm one, which is z-fighting on small parts only.
    expect(large.far / large.near).toBeCloseTo(small.far / small.near, 6);
  });

  it('moves further back as the viewport narrows', () => {
    // A tall narrow viewport is horizontally tighter than the vertical FOV implies.
    // Using the vertical FOV alone would return the same distance for both.
    const wide = frameCamera([100, 100, 100], 45, 2);
    const narrow = frameCamera([100, 100, 100], 45, 0.4);
    expect(narrow.distance).toBeGreaterThan(wide.distance);
  });

  it('survives a degenerate size and aspect', () => {
    // A single flat triangle has zero extent on one axis, and a canvas measured before
    // layout has zero height.
    const framing = frameCamera([0, 0, 0], 45, 0);
    expect(Number.isFinite(framing.distance)).toBe(true);
    expect(framing.near).toBeGreaterThan(0);
    expect(framing.far).toBeGreaterThan(framing.near);
  });
});

describe('formatting', () => {
  it('renders whole millimetres without a decimal point', () => {
    expect(formatDimensions([220, 180, 145])).toBe('220 × 180 × 145 mm');
  });

  it('rounds to one decimal place', () => {
    expect(formatDimensions([12.649, 0.04, 99.95])).toBe('12.6 × 0 × 100 mm');
  });

  it('counts objects in words the design uses', () => {
    expect(formatObjectCount(1)).toBe('1 object');
    expect(formatObjectCount(4)).toBe('4 objects');
  });
});
