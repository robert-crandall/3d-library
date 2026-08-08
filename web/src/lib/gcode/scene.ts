import {
  BufferGeometry,
  Float32BufferAttribute,
  Group,
  LineBasicMaterial,
  LineSegments,
} from 'three';
import { createViewport, disposeTree } from '$lib/viewer/viewport';
import { layerRange, skipSegments, spreadRange, type Toolpath } from './toolpath';
import type { Volume } from './printer';

/*
  What is toolpath-shaped about the preview: line geometry per chunk, a build-volume
  grid, and the draw ranges that make the scrub slider work. The renderer, camera and
  controls under it are generic and live in `viewer/viewport.ts`.

  There are no lights and no normals - `LineBasicMaterial` is unlit, which is right for
  a toolpath: shading a 0.4 mm extrusion width conveys nothing and costs a normal per
  vertex on geometry that is already the largest thing on the page.

  This file needs a GPU to mean anything, so it is covered by using the app. What it
  decides that a test could catch - which segments belong to a layer - is in
  `toolpath.ts` and `layerRange` below, both of which are pure.
*/

export type GcodeViewer = {
  /** Replace the displayed toolpath and re-frame the camera on it. */
  show(toolpath: Toolpath, options: ShowOptions): void;
  /** Draw layers `0..index` inclusive. */
  setLayer(index: number): void;
  setTravelVisible(visible: boolean): void;
  /** Re-read the canvas's CSS size. Cheap enough to call on every resize event. */
  resize(): void;
  dispose(): void;
};

export type ShowOptions = {
  /** `0xrrggbb`, already lifted into a legible band by `printer.ts`. */
  readonly color: number;
  readonly volume: Volume;
};

/** Faint and neutral: the grid is a reference, not a thing to look at. */
const GRID_COLOR = 0x8e97a3;
/** Fainter still, and not the filament colour - travel is not printed material. */
const TRAVEL_COLOR = 0x6f7887;

export function createViewer(canvas: HTMLCanvasElement): GcodeViewer {
  const viewport = createViewport(canvas);

  const extrusionMaterial = new LineBasicMaterial({ color: 0xffffff });
  const travelMaterial = new LineBasicMaterial({
    color: TRAVEL_COLOR,
    transparent: true,
    opacity: 0.35,
  });
  const gridMaterial = new LineBasicMaterial({
    color: GRID_COLOR,
    transparent: true,
    opacity: 0.3,
  });

  // One group per role so a reload can empty it without touching the others, and so the
  // travel toggle is one `visible` rather than a walk over every chunk.
  const extrusion = new Group();
  const travel = new Group();
  const grid = new Group();
  viewport.scene.add(extrusion, travel, grid);

  let shown: Toolpath | undefined;

  function rebuild(group: Group, chunks: readonly Float32Array[], center: readonly number[]) {
    disposeTree(group);
    group.clear();
    for (const chunk of chunks) {
      const geometry = new BufferGeometry();
      geometry.setAttribute('position', new Float32BufferAttribute(chunk, 3));
      geometry.translate(-center[0], -center[1], -center[2]);
      group.add(
        new LineSegments(geometry, group === extrusion ? extrusionMaterial : travelMaterial),
      );
    }
  }

  return {
    show(toolpath, { color, volume }) {
      shown = toolpath;
      extrusionMaterial.color.setHex(color);

      // Framed on the extrusions alone. Travel moves reach the purge line and the wipe
      // tower, which are off to one side of the plate, and fitting them shrinks the
      // model itself to a corner of the panel. The grid is left out for the same
      // reason: a 20 mm part on a 256 mm bed should fill the panel.
      const { center } = viewport.frame(skipSegments(toolpath.extrusion, toolpath.purgeSegments));

      rebuild(extrusion, toolpath.extrusion, center);
      rebuild(travel, toolpath.travel, center);

      disposeTree(grid);
      grid.clear();
      grid.add(new LineSegments(buildVolumeGeometry(volume, center), gridMaterial));

      this.setLayer(toolpath.layers.length - 1);
      viewport.render();
    },

    setLayer(index) {
      if (!shown) return;
      applyRange(extrusion, layerRange(shown, index, 'extrusionEnd'));
      applyRange(travel, layerRange(shown, index, 'travelEnd'));
      viewport.render();
    },

    setTravelVisible(visible) {
      travel.visible = visible;
      viewport.render();
    },

    resize: viewport.resize,

    dispose() {
      disposeTree(extrusion);
      disposeTree(travel);
      disposeTree(grid);
      extrusionMaterial.dispose();
      travelMaterial.dispose();
      gridMaterial.dispose();
      viewport.dispose();
    },
  };
}

/**
 * Spread one segment budget across a group of fixed-size chunks. `setDrawRange` counts
 * vertices, and a segment is two.
 */
function applyRange(group: Group, segments: number) {
  const counts = spreadRange(
    group.children.map(
      (child) => (child as LineSegments).geometry.getAttribute('position').count / 2,
    ),
    segments,
  );
  group.children.forEach((child, index) => {
    (child as LineSegments).geometry.setDrawRange(0, counts[index] * 2);
  });
}

/**
 * The twelve edges of the build volume plus a 10 mm floor grid, in the same translated
 * space as the toolpaths.
 *
 * The bed's origin is its front-left corner, which is where a printer's coordinates
 * start, so the box runs from 0 to the volume's extent rather than being centred.
 */
function buildVolumeGeometry(volume: Volume, center: readonly number[]): BufferGeometry {
  const { x, y, z } = volume;
  const points: number[] = [];
  const line = (ax: number, ay: number, az: number, bx: number, by: number, bz: number) => {
    points.push(ax, ay, az, bx, by, bz);
  };

  for (const height of [0, z]) {
    line(0, 0, height, x, 0, height);
    line(x, 0, height, x, y, height);
    line(x, y, height, 0, y, height);
    line(0, y, height, 0, 0, height);
  }
  for (const [cx, cy] of [
    [0, 0],
    [x, 0],
    [x, y],
    [0, y],
  ]) {
    line(cx, cy, 0, cx, cy, z);
  }

  // A 10 mm floor grid, which is the only thing in the scene that gives the print a
  // sense of scale once the camera has framed it.
  const step = 10;
  for (let at = step; at < x; at += step) line(at, 0, 0, at, y, 0);
  for (let at = step; at < y; at += step) line(0, at, 0, x, at, 0);

  const geometry = new BufferGeometry();
  geometry.setAttribute('position', new Float32BufferAttribute(points, 3));
  geometry.translate(-center[0], -center[1], -center[2]);
  return geometry;
}
