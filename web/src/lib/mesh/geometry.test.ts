import { describe, expect, it } from 'vitest';
import {
  boundsOf,
  centerOf,
  formatDimensions,
  formatObjectCount,
  frameCamera,
  sizeOf,
  VIEW_DIRECTION,
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
 * The tightest and the loosest vertex, as fractions of the frustum half-extent at their
 * own depth. Anything over 1 is off screen; the largest tells us the fit is snug.
 *
 * This is the same projection `frameCamera` solves, written out independently: it walks
 * the vertices through the camera basis and divides, rather than reusing the closed
 * form. A test that reused the implementation's own maths would agree with any bug in it.
 */
function frustumFit(
  positions: Float32Array,
  fovDegrees: number,
  aspect: number,
  distance: number,
): { worst: number; behindCamera: boolean } {
  const tanV = Math.tan((fovDegrees * Math.PI) / 360);
  const tanH = tanV * aspect;
  const center = centerOf(boundsOf(positions));
  const f = VIEW_DIRECTION;
  // right = normalize(VIEW_UP x forward), up = forward x right, with VIEW_UP = (0,0,1).
  const rawRight: [number, number, number] = [-f[1], f[0], 0];
  const rl = Math.hypot(...rawRight) || 1;
  const r: [number, number, number] = [rawRight[0] / rl, rawRight[1] / rl, 0];
  const u: [number, number, number] = [
    f[1] * r[2] - f[2] * r[1],
    f[2] * r[0] - f[0] * r[2],
    f[0] * r[1] - f[1] * r[0],
  ];

  let worst = 0;
  let behindCamera = false;
  for (let i = 0; i < positions.length; i += 3) {
    const x = positions[i] - center[0];
    const y = positions[i + 1] - center[1];
    const z = positions[i + 2] - center[2];
    // The camera sits at `distance` along +forward looking back down it, so a vertex's
    // depth is what is left after its own projection onto that axis.
    const depth = distance - (x * f[0] + y * f[1] + z * f[2]);
    if (depth <= 0) {
      behindCamera = true;
      continue;
    }
    const across = Math.abs(x * r[0] + y * r[1] + z * r[2]) / (tanH * depth);
    const down = Math.abs(x * u[0] + y * u[1] + z * u[2]) / (tanV * depth);
    worst = Math.max(worst, across, down);
  }
  return { worst, behindCamera };
}

/** Triangles covering the eight corners of a box centred on `origin`. */
function boxAt(
  size: [number, number, number],
  origin: [number, number, number] = [0, 0, 0],
): Float32Array {
  const out: number[] = [];
  for (const sx of [-1, 1]) {
    for (const sy of [-1, 1]) {
      for (const sz of [-1, 1]) {
        // A degenerate triangle per corner: `frameCamera` only ever reads vertices, so
        // this exercises the exact corner set without needing real faces.
        for (let i = 0; i < 3; i++) {
          out.push(
            origin[0] + (sx * size[0]) / 2,
            origin[1] + (sy * size[1]) / 2,
            origin[2] + (sz * size[2]) / 2,
          );
        }
      }
    }
  }
  return new Float32Array(out);
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
        const positions = boxAt(size);
        const framing = frameCamera(positions, 45, aspect);
        const fit = frustumFit(positions, 45, aspect, framing.distance);

        // Nothing off screen, nothing behind the camera...
        expect(fit.behindCamera).toBe(false);
        expect(fit.worst).toBeLessThanOrEqual(1);
        // ...and snug: the tightest vertex sits exactly on the margin. Framing the
        // bounding sphere satisfies the first half and fails this one, by 2x on a wide
        // flat plate.
        expect(fit.worst).toBeCloseTo(1 / 1.15, 6);

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

  it('frames a flat plate far closer than its bounding sphere would', () => {
    // The defect this replaced: a real 507 x 417 x 41 mm slicer plate framed on its
    // bounding sphere sat so far back it covered 2% of the panel. The sphere radius is
    // dominated by the plate diagonal, which is nothing you can see from above.
    const size: [number, number, number] = [507.2, 417.1, 40.8];
    const positions = boxAt(size);
    const aspect = 16 / 9;
    const framing = frameCamera(positions, 45, aspect);

    const radius = Math.hypot(...size) / 2;
    const vFov = (45 * Math.PI) / 180;
    const hFov = 2 * Math.atan(Math.tan(vFov / 2) * aspect);
    const sphereDistance = (1.15 * radius) / Math.sin(Math.min(vFov, hFov) / 2);

    expect(framing.distance).toBeLessThan(sphereDistance * 0.75);
  });

  it('ignores where in space the file put the model', () => {
    // A 3MF in plate coordinates sits hundreds of millimetres from the origin. Framing
    // that measured from the origin rather than the model's own centre would put the
    // camera in the next county.
    const size: [number, number, number] = [40, 30, 20];
    const centred = frameCamera(boxAt(size), 45, 16 / 9);
    const offset = frameCamera(boxAt(size, [128, -256, 512]), 45, 16 / 9);
    expect(offset.distance).toBeCloseTo(centred.distance, 4);
  });

  it('is fully zoomed out at maxDistance without clipping', () => {
    const size: [number, number, number] = [220, 180, 145];
    const positions = boxAt(size);
    const framing = frameCamera(positions, 45, 16 / 9);
    const fit = frustumFit(positions, 45, 16 / 9, framing.maxDistance);
    expect(fit.behindCamera).toBe(false);
    expect(fit.worst).toBeLessThanOrEqual(1);
    expect(framing.far).toBeGreaterThan(framing.maxDistance + 145);
  });

  it('keeps the same depth precision for a tiny and a huge model', () => {
    const small = frameCamera(boxAt([1, 1, 1]), 45, 1);
    const large = frameCamera(boxAt([300, 300, 300]), 45, 1);
    // A fixed near plane would give a 1 mm model a far/near ratio thousands of times
    // worse than a 300 mm one, which is z-fighting on small parts only.
    expect(large.far / large.near).toBeCloseTo(small.far / small.near, 6);
  });

  it('moves further back as the viewport narrows', () => {
    // A tall narrow viewport is horizontally tighter than the vertical FOV implies.
    // Using the vertical FOV alone would return the same distance for both.
    const wide = frameCamera(boxAt([100, 100, 100]), 45, 2);
    const narrow = frameCamera(boxAt([100, 100, 100]), 45, 0.4);
    expect(narrow.distance).toBeGreaterThan(wide.distance);
  });

  it('survives a degenerate mesh and aspect', () => {
    // A single degenerate triangle has no extent at all, and a canvas measured before
    // layout has zero height.
    const framing = frameCamera(new Float32Array(9), 45, 0);
    expect(Number.isFinite(framing.distance)).toBe(true);
    expect(framing.distance).toBeGreaterThan(0);
    expect(framing.near).toBeGreaterThan(0);
    expect(framing.far).toBeGreaterThan(framing.near);
    expect(framing.minDistance).toBeLessThan(framing.distance);
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
