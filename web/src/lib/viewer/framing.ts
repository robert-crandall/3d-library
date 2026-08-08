// Pure geometry helpers shared by every 3D preview. Nothing here imports three.js, so
// the camera and dimension maths can be tested without a canvas or a GPU - which
// matters, because the epic rules out browser tests and this is the arithmetic most
// likely to be silently wrong.
//
// This started as `mesh/geometry.ts` and moved here when the G-code viewer needed the
// same camera fit over a different kind of geometry. The only change to the maths was
// accepting several arrays instead of one: a mesh arrives as a single buffer, but a
// streamed toolpath arrives as a list of fixed-size chunks and is far too large to
// concatenate just to be measured.

export type Points = Float32Array | readonly Float32Array[];

function chunksOf(points: Points): readonly Float32Array[] {
  return points instanceof Float32Array ? [points] : points;
}

export type Bounds = {
  min: [number, number, number];
  max: [number, number, number];
};

/** Axis-aligned bounds of every vertex. Throws if there are none. */
export function boundsOf(points: Points): Bounds {
  const min: [number, number, number] = [Infinity, Infinity, Infinity];
  const max: [number, number, number] = [-Infinity, -Infinity, -Infinity];
  let seen = 0;
  for (const chunk of chunksOf(points)) {
    for (let i = 0; i + 2 < chunk.length; i += 3) {
      seen++;
      for (let axis = 0; axis < 3; axis++) {
        const v = chunk[i + axis];
        if (v < min[axis]) min[axis] = v;
        if (v > max[axis]) max[axis] = v;
      }
    }
  }
  if (seen === 0) {
    throw new Error('This file contains no geometry.');
  }
  return { min, max };
}

/** Bounding-box size in millimetres, X/Y/Z. */
export function sizeOf(bounds: Bounds): [number, number, number] {
  return [
    bounds.max[0] - bounds.min[0],
    bounds.max[1] - bounds.min[1],
    bounds.max[2] - bounds.min[2],
  ];
}

export function centerOf(bounds: Bounds): [number, number, number] {
  return [
    (bounds.min[0] + bounds.max[0]) / 2,
    (bounds.min[1] + bounds.max[1]) / 2,
    (bounds.min[2] + bounds.max[2]) / 2,
  ];
}

export type Framing = {
  /** Camera distance from the origin that fits the whole model. */
  distance: number;
  near: number;
  far: number;
  minDistance: number;
  maxDistance: number;
  /**
   * Bounds centre and size, in the coordinates the caller passed in.
   *
   * Returned rather than recomputed because measuring is a full pass over every vertex
   * and the caller needs both anyway: the centre to translate the geometry onto the
   * origin the camera is aimed at, and the size for the readout. A 300 MB toolpath is
   * the reason - three passes over it to answer one question is noticeable.
   */
  center: [number, number, number];
  size: [number, number, number];
};

/** Extra room around the model so it does not touch the viewport edges. */
const MARGIN = 1.15;

/**
 * Direction from the model to the camera: a three-quarter view from above, matching the
 * design's screenshots. A straight-on axis view hides depth entirely.
 *
 * Exported so `frameCamera` and the scene cannot disagree about the opening shot - the
 * framing below is only correct for the direction the camera is actually placed on.
 */
export const VIEW_DIRECTION: [number, number, number] = unit([1, -1, 0.65]);

/** Printers and slicers put Z up, so the preview does too; three's default is Y up. */
export const VIEW_UP: [number, number, number] = [0, 0, 1];

function unit(v: [number, number, number]): [number, number, number] {
  const length = Math.hypot(v[0], v[1], v[2]) || 1;
  return [v[0] / length, v[1] / length, v[2] / length];
}

function cross(
  a: [number, number, number],
  b: [number, number, number],
): [number, number, number] {
  return [
    a[1] * b[2] - a[2] * b[1],
    a[2] * b[0] - a[0] * b[2],
    a[0] * b[1] - a[1] * b[0],
  ];
}

/**
 * Where to put the camera, and where to put the clip planes.
 *
 * Distance is the smallest one that keeps every *vertex* inside the frustum from
 * `VIEW_DIRECTION`, solved directly: a vertex at depth `d` along the view axis and
 * `x` across it needs the camera at least `d + x / tan(fov/2)` away, so the answer is
 * the largest such requirement over the mesh. One pass, no search.
 *
 * Fitting the vertices rather than the bounding sphere is the whole point. A slicer
 * plate is wide and flat with its objects spread out, so its sphere is roughly twice
 * the radius of anything you can see: framing the sphere left a real 507 x 417 x 41 mm
 * plate occupying 2% of the panel, and it took fourteen scroll notches to fill the
 * frame. The cost is that the fit is only tight for the opening angle - orbiting to a
 * broader silhouette can push the model past the edges, and the user scrolls out. That
 * is the trade every desktop viewer makes, and it is the right way round: the opening
 * shot is what everyone sees, and the overflow needs a deliberate drag to reach.
 *
 * The two terms are coupled per vertex - `depth + MARGIN * max(across/tanH, down/tanV)`
 * is maximised over vertices, not assembled from independent extrema - so this cannot
 * be split into a "measure while streaming, fit later" pair. The vertex that needs the
 * greatest distance is often neither the deepest nor the widest, and `tanH` needs an
 * aspect ratio that is not known until the canvas is measured. Hence the chunk list:
 * the fit stays a single pass over the real vertices, just spread across several arrays.
 *
 * The clip planes and the zoom limits bracket the model itself rather than the opening
 * shot, so they stay valid at any aspect ratio and a resize does not have to re-fit.
 * They have to clear the whole permitted zoom range in any case: `OrbitControls` lets the
 * user zoom, and planes fitted to the opening distance clip the front of the model the
 * moment they do.
 */
export function frameCamera(points: Points, fovDegrees: number, aspect: number): Framing {
  const chunks = chunksOf(points);
  const bounds = boundsOf(chunks);
  const center = centerOf(bounds);
  const size = sizeOf(bounds);
  const radius = Math.max(Math.hypot(size[0], size[1], size[2]) / 2, 1e-6);
  const safeAspect = aspect > 0 && Number.isFinite(aspect) ? aspect : 1;

  // A perspective camera's vertical FOV is fixed; the horizontal one follows from the
  // aspect ratio, so a tall narrow viewport is horizontally the tighter of the two.
  const tanV = Math.tan((fovDegrees * Math.PI) / 360);
  const tanH = tanV * safeAspect;

  const forward = VIEW_DIRECTION;
  const right = unit(cross(VIEW_UP, forward));
  const up = cross(forward, right);

  let distance = 0;
  let frontmost = 0;
  for (const chunk of chunks) {
    for (let i = 0; i + 2 < chunk.length; i += 3) {
      const x = chunk[i] - center[0];
      const y = chunk[i + 1] - center[1];
      const z = chunk[i + 2] - center[2];
      const depth = x * forward[0] + y * forward[1] + z * forward[2];
      const across = Math.abs(x * right[0] + y * right[1] + z * right[2]);
      const down = Math.abs(x * up[0] + y * up[1] + z * up[2]);
      const need = depth + MARGIN * Math.max(across / tanH, down / tanV);
      if (need > distance) distance = need;
      if (depth > frontmost) frontmost = depth;
    }
  }

  const minDistance = radius * 0.05;
  const near = minDistance * 0.5;
  // Two floors under the fit, both reachable and both leaving the camera somewhere the
  // returned planes do not describe. A model long and thin *along* the view axis - a
  // needle pointing at the viewer - satisfies the lateral fit with almost no distance to
  // spare, and puts its own nose through the near plane; `minDistance` is the closest the
  // controls ever let the camera get, so it is the clearance to leave. A model with no
  // extent at all, one degenerate triangle, reaches here with a distance of zero, which
  // is nearer than `minDistance` and so is not a position the controls would allow.
  if (!(distance > frontmost + minDistance)) distance = frontmost + minDistance;
  if (!(distance > minDistance)) distance = radius;

  // Sized from the model rather than from the opening distance, so they stay correct at
  // any aspect ratio: `resize` re-applies these without re-fitting, and a fit made in a
  // wide window is a poor bound once the window is narrow. `Math.max` only ever raises
  // the ceiling, so the ordering the caller relies on holds however tight the fit came
  // out. Everything scales with the radius, so a 1 mm part and a 300 mm plate get the
  // same near/far ratio and so the same depth-buffer precision.
  const maxDistance = Math.max(radius * 40, distance * 1.5);
  return {
    distance,
    near,
    far: maxDistance + radius * 2,
    minDistance,
    maxDistance,
    center,
    size,
  };
}

/** One decimal place, trailing zeros stripped: 220 stays 220, 12.65 becomes 12.7. */
function millimetres(value: number): string {
  return String(Math.round(value * 10) / 10);
}

/** `220 × 180 × 145 mm`, matching the design's readout. */
export function formatDimensions(size: [number, number, number]): string {
  return `${millimetres(size[0])} × ${millimetres(size[1])} × ${millimetres(size[2])} mm`;
}
