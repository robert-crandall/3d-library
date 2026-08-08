import {
  PerspectiveCamera,
  Scene,
  Vector3,
  WebGLRenderer,
  type Object3D,
} from 'three';
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js';
import { frameCamera, VIEW_DIRECTION, VIEW_UP, type Framing, type Points } from './framing';

// The renderer, camera and controls, with nothing in them that knows whether it is
// showing a mesh or a toolpath. Milestone 5 built this inline in `mesh/scene.ts` and
// said to split it out when a second kind of geometry needed it; this is that split.
//
// The maths this file leans on is in `framing.ts`, which imports nothing and is
// unit-tested. What is left here is wiring a renderer to a canvas, which needs a GPU to
// mean anything and so is covered by using the app rather than by a test.

export type Viewport = {
  /** For a layer to add its own objects to. */
  readonly scene: Scene;
  /** For a layer to parent lights to, so they orbit with the viewer. */
  readonly camera: PerspectiveCamera;
  /**
   * Fit the camera to `points` and return the framing, whose `center` and `size` the
   * caller needs anyway.
   *
   * The camera is aimed at the origin, so the caller must translate its geometry by
   * `-center`. Centring the geometry rather than moving the orbit target keeps the
   * pivot on the model regardless of where the file put it, and a plate-coordinate 3MF
   * or a belt printer's machine coordinates put it a long way from the origin.
   */
  frame(points: Points): Framing;
  render(): void;
  /** Re-read the canvas's CSS size. Cheap enough to call on every resize event. */
  resize(): void;
  dispose(): void;
};

export const FOV = 45;

export function createViewport(canvas: HTMLCanvasElement): Viewport {
  const renderer = new WebGLRenderer({ canvas, antialias: true, alpha: true });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));

  const scene = new Scene();
  const camera = new PerspectiveCamera(FOV, 1, 0.1, 1000);
  camera.up.set(...VIEW_UP);
  scene.add(camera);

  const controls = new OrbitControls(camera, canvas);
  controls.enableDamping = false;

  let disposed = false;
  const render = () => {
    if (!disposed) renderer.render(scene, camera);
  };
  controls.addEventListener('change', render);

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

  function measure() {
    const width = canvas.clientWidth || 1;
    const height = canvas.clientHeight || 1;
    renderer.setSize(width, height, false);
    camera.aspect = width / height;
  }

  return {
    scene,
    camera,

    frame(points) {
      measure();
      framing = frameCamera(points, FOV, camera.aspect);
      applyFraming();
      camera.updateProjectionMatrix();
      camera.position.copy(new Vector3(...VIEW_DIRECTION).multiplyScalar(framing.distance));
      controls.target.set(0, 0, 0);
      controls.update();
      render();
      return framing;
    },

    render,

    resize() {
      measure();
      applyFraming();
      camera.updateProjectionMatrix();
      render();
    },

    dispose() {
      disposed = true;
      controls.removeEventListener('change', render);
      controls.dispose();
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

/**
 * Free a subtree's geometries. Three does not do this for you.
 *
 * Geometries only, because a material outlives the nodes wearing it: both scenes build
 * their materials once and hand the same instance to every node, so disposing them here
 * would free a material that the next draw still uses - and then free it a second time
 * when the viewer is torn down. Materials belong to whoever made them.
 */
export function disposeGeometries(root: Object3D) {
  root.traverse((node) => {
    (node as Object3D & { geometry?: { dispose(): void } }).geometry?.dispose();
  });
}
