import {
  BufferGeometry,
  Float32BufferAttribute,
  DirectionalLight,
  DoubleSide,
  HemisphereLight,
  Mesh,
  MeshBasicMaterial,
  MeshLambertMaterial,
  PerspectiveCamera,
  Scene,
  Vector3,
  WebGLRenderer,
} from 'three';
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js';
import {
  boundsOf,
  centerOf,
  frameCamera,
  VIEW_DIRECTION,
  VIEW_UP,
  type Framing,
  type ParsedMesh,
} from './geometry';

// Everything three.js-shaped lives here, and nothing else does. The maths this file
// leans on is in `geometry.ts`, which imports nothing and is unit-tested; what is left
// here is wiring a renderer to a canvas, which needs a GPU to mean anything and so is
// covered by using the app rather than by a test.

export type Shading = 'solid' | 'wireframe' | 'xray';

export type Viewer = {
  /** Replace the displayed mesh and re-frame the camera on it. */
  show(mesh: ParsedMesh): void;
  setShading(shading: Shading): void;
  /** Re-read the canvas's CSS size. Cheap enough to call on every resize event. */
  resize(): void;
  dispose(): void;
};

const FOV = 45;

/** Mid-grey on purpose: the renderer clears to transparent so the panel's own
 *  `bg-surface` shows through, and this has to read against both themes. Naming a colour
 *  here rather than in CSS is what keeps `app.css` a palette and nothing else (D5). */
const SURFACE_COLOR = 0x8e97a3;
const LINE_COLOR = 0x5b6472;

export function createViewer(canvas: HTMLCanvasElement): Viewer {
  const renderer = new WebGLRenderer({ canvas, antialias: true, alpha: true });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));

  const scene = new Scene();
  const camera = new PerspectiveCamera(FOV, 1, 0.1, 1000);
  camera.up.set(...VIEW_UP);

  // Parented to the camera so the lighting orbits with the viewer. Fixed lights leave
  // the far side of the model in permanent shadow, which looks like a hole in the mesh.
  const key = new DirectionalLight(0xffffff, 2.2);
  key.position.set(0.5, 0.8, 1);
  camera.add(key);
  scene.add(camera);
  scene.add(new HemisphereLight(0xffffff, 0x555566, 1.6));

  const controls = new OrbitControls(camera, canvas);
  controls.enableDamping = false;

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
  let disposed = false;

  const render = () => {
    if (!disposed) renderer.render(scene, camera);
  };
  controls.addEventListener('change', render);

  const resize = () => {
    const width = canvas.clientWidth || 1;
    const height = canvas.clientHeight || 1;
    renderer.setSize(width, height, false);
    camera.aspect = width / height;
    applyFraming();
    camera.updateProjectionMatrix();
    render();
  };

  // Kept so a resize can re-apply the clip planes and zoom limits. They are sized from
  // the model rather than the fit, so they need no recomputing; the fit itself is
  // deliberately not redone, because it would override the user's zoom and costs a pass
  // over every vertex on every frame of a window drag.
  let framing: Framing | undefined;

  function applyFraming() {
    if (!framing) return;
    camera.near = framing.near;
    camera.far = framing.far;
    controls.minDistance = framing.minDistance;
    controls.maxDistance = framing.maxDistance;
    // Panning moves the orbit target, not the camera's distance from it, so
    // `maxDistance` alone does not stop the user dragging the model off screen with no
    // way back. This bounds how far the target may wander from the model.
    controls.maxTargetRadius = framing.maxDistance;
  }

  return {
    show(parsed) {
      const bounds = boundsOf(parsed.positions);
      const center = centerOf(bounds);

      const geometry = new BufferGeometry();
      geometry.setAttribute('position', new Float32BufferAttribute(parsed.positions, 3));
      // Centred here rather than by moving the camera target: it keeps the orbit
      // pivot at the model regardless of where the file put it, and a plate-coordinate
      // 3MF puts it a long way from the origin.
      geometry.translate(-center[0], -center[1], -center[2]);
      geometry.computeVertexNormals();

      if (mesh) {
        mesh.geometry.dispose();
        mesh.geometry = geometry;
      } else {
        // The chosen shading, not solid: the buttons are usable while the first mesh is
        // still downloading, and building it solid would leave the pressed button lying.
        mesh = new Mesh(geometry, materials[active]);
        scene.add(mesh);
      }

      const width = canvas.clientWidth || 1;
      const height = canvas.clientHeight || 1;
      renderer.setSize(width, height, false);
      camera.aspect = width / height;
      framing = frameCamera(parsed.positions, FOV, camera.aspect);
      applyFraming();
      camera.updateProjectionMatrix();

      camera.position.copy(
        new Vector3(...VIEW_DIRECTION).multiplyScalar(framing.distance),
      );
      controls.target.set(0, 0, 0);
      controls.update();
      render();
    },

    setShading(shading) {
      active = shading;
      if (mesh) mesh.material = materials[shading];
      render();
    },

    resize,

    dispose() {
      disposed = true;
      controls.removeEventListener('change', render);
      controls.dispose();
      mesh?.geometry.dispose();
      for (const material of Object.values(materials)) material.dispose();
      // Browsers cap the number of live GL contexts (~16) and drop the oldest when a new
      // one exceeds it. `dispose()` frees the renderer's own resources but leaves the
      // context alive until it is collected, so a user who opens sixteen models in one SPA
      // session can knock out the viewer they are looking at; `forceContextLoss` hands it
      // back now.
      renderer.dispose();
      renderer.forceContextLoss();
    },
  };
}
