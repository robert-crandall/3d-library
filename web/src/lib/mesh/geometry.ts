// Pure geometry helpers for the mesh preview. Nothing here imports three.js, so the
// camera and dimension maths can be tested without a canvas or a GPU - which matters,
// because the epic rules out browser tests and this is the arithmetic most likely to be
// silently wrong.

/**
 * A parsed mesh, ready to hand to the renderer.
 *
 * Positions are non-indexed triangles - 9 floats each, three vertices of three
 * coordinates - already converted to millimetres by the parser. Non-indexed because
 * three's `computeVertexNormals()` then produces flat shading, which is what a printed
 * part should look like, without us tracking normals through the transform stack.
 */
export type ParsedMesh = {
  positions: Float32Array;
  /** Objects the file places: 1 for an STL, the build-item count for a 3MF. */
  objectCount: number;
};

export type Bounds = {
  min: [number, number, number];
  max: [number, number, number];
};

/** Axis-aligned bounds of every vertex. Throws if there are none. */
export function boundsOf(positions: Float32Array): Bounds {
  if (positions.length < 9) {
    throw new Error('This file contains no triangles.');
  }
  const min: [number, number, number] = [Infinity, Infinity, Infinity];
  const max: [number, number, number] = [-Infinity, -Infinity, -Infinity];
  for (let i = 0; i < positions.length; i += 3) {
    for (let axis = 0; axis < 3; axis++) {
      const v = positions[i + axis];
      if (v < min[axis]) min[axis] = v;
      if (v > max[axis]) max[axis] = v;
    }
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
};

/** Extra room around the model so it does not touch the viewport edges. */
const MARGIN = 1.15;

/**
 * Where to put the camera, and where to put the clip planes.
 *
 * The caller centres the geometry on the origin and points the camera at it, so this
 * only needs the bounding-box size. Distance is computed from the bounding *sphere*
 * rather than the box, so the framing does not change as the user orbits.
 *
 * The clip planes bracket the whole permitted zoom range, not just the initial
 * distance: `OrbitControls` lets the user zoom, and planes fitted to the opening shot
 * clip the front of the model the moment they do. Everything scales with the model's
 * radius, so a 1 mm part and a 300 mm plate get the same far/near ratio (~1e4) and so
 * the same depth-buffer precision.
 */
export function frameCamera(
  size: [number, number, number],
  fovDegrees: number,
  aspect: number,
): Framing {
  const radius = Math.max(Math.hypot(size[0], size[1], size[2]) / 2, 1e-6);
  const safeAspect = aspect > 0 && Number.isFinite(aspect) ? aspect : 1;

  // A perspective camera's vertical FOV is fixed; the horizontal one follows from the
  // aspect ratio. A tall, narrow viewport is horizontally tighter, so take the larger
  // of the two required distances - which is the one from the smaller angle.
  const vFov = (fovDegrees * Math.PI) / 180;
  const hFov = 2 * Math.atan(Math.tan(vFov / 2) * safeAspect);
  const distance = (MARGIN * radius) / Math.sin(Math.min(vFov, hFov) / 2);

  const minDistance = radius * 0.05;
  const maxDistance = distance * 10;
  return {
    distance,
    near: minDistance * 0.5,
    far: maxDistance + radius * 2,
    minDistance,
    maxDistance,
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

export function formatObjectCount(count: number): string {
  return count === 1 ? '1 object' : `${count} objects`;
}
