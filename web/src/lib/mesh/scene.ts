import {
  BufferGeometry,
  Float32BufferAttribute,
  DirectionalLight,
  DoubleSide,
  HemisphereLight,
  Mesh,
  MeshBasicMaterial,
  MeshLambertMaterial,
} from 'three';
import { createViewport } from '$lib/viewer/viewport';
import type { ParsedMesh } from './geometry';

// What is mesh-shaped about the preview: the lighting, the three shading materials, and
// turning a triangle soup into a `Mesh`. The renderer, camera and controls under it are
// generic and live in `viewer/viewport.ts`; the camera maths is in `viewer/framing.ts`.
// This file still needs a GPU to mean anything, so it is covered by using the app.

export type Shading = 'solid' | 'wireframe' | 'xray';

export type Viewer = {
  /** Replace the displayed mesh and re-frame the camera on it. */
  show(mesh: ParsedMesh): void;
  setShading(shading: Shading): void;
  /** Re-read the canvas's CSS size. Cheap enough to call on every resize event. */
  resize(): void;
  dispose(): void;
};

/** Mid-grey on purpose: the renderer clears to transparent so the panel's own
 *  `bg-surface` shows through, and this has to read against both themes. Naming a colour
 *  here rather than in CSS is what keeps `app.css` a palette and nothing else (D5). */
const SURFACE_COLOR = 0x8e97a3;
const LINE_COLOR = 0x5b6472;

export function createViewer(canvas: HTMLCanvasElement): Viewer {
  const viewport = createViewport(canvas);

  // Parented to the camera so the lighting orbits with the viewer. Fixed lights leave
  // the far side of the model in permanent shadow, which looks like a hole in the mesh.
  const key = new DirectionalLight(0xffffff, 2.2);
  key.position.set(0.5, 0.8, 1);
  viewport.camera.add(key);
  const fill = new HemisphereLight(0xffffff, 0x555566, 1.6);
  viewport.scene.add(fill);

  // Built once and swapped by reference. `DoubleSide` throughout because a build item
  // may carry a mirrored transform, which reverses triangle winding: back-face culling
  // would render such an object inside out.
  const materials: Record<Shading, MeshLambertMaterial | MeshBasicMaterial> = {
    solid: new MeshLambertMaterial({ color: SURFACE_COLOR, side: DoubleSide }),
    wireframe: new MeshBasicMaterial({ color: LINE_COLOR, wireframe: true, side: DoubleSide }),
    xray: new MeshBasicMaterial({
      color: LINE_COLOR,
      side: DoubleSide,
      transparent: true,
      opacity: 0.18,
      // Without this the nearest surface hides the ones behind it, which is the whole
      // point of the mode.
      depthWrite: false,
    }),
  };

  let mesh: Mesh | undefined;
  let active: Shading = 'solid';

  return {
    show(parsed) {
      // Framed before the geometry is built, because the fit is what says where the
      // centre is and the geometry has to be translated onto it. Centring here rather
      // than by moving the orbit target keeps the pivot at the model regardless of
      // where the file put it, and a plate-coordinate 3MF puts it a long way out.
      const { center } = viewport.frame(parsed.positions);

      const geometry = new BufferGeometry();
      geometry.setAttribute('position', new Float32BufferAttribute(parsed.positions, 3));
      geometry.translate(-center[0], -center[1], -center[2]);
      geometry.computeVertexNormals();

      if (mesh) {
        mesh.geometry.dispose();
        mesh.geometry = geometry;
      } else {
        // The chosen shading, not solid: the buttons are usable while the first mesh is
        // still downloading, and building it solid would leave the pressed button lying.
        mesh = new Mesh(geometry, materials[active]);
        viewport.scene.add(mesh);
      }

      viewport.render();
    },

    setShading(shading) {
      active = shading;
      if (mesh) mesh.material = materials[shading];
      viewport.render();
    },

    resize: viewport.resize,

    dispose() {
      mesh?.geometry.dispose();
      fill.dispose();
      key.dispose();
      for (const material of Object.values(materials)) material.dispose();
      viewport.dispose();
    },
  };
}
