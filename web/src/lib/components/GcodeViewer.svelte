<script lang="ts">
  /*
    Screen 1c's G-code preview.

    Draws one sliced file's toolpaths with a slider that walks up the layers. Same shape
    as `MeshViewer` deliberately - a state machine, a lazily imported renderer, an
    AbortController and a generation counter - rather than one component generalised over
    both. The two share a viewport and a framing helper, and past that a mesh and a
    toolpath have different bytes, different failure modes and different controls.

    The file is read as a stream rather than downloaded whole: several hundred megabytes
    is an ordinary size for G-code, and holding all of it as text before parsing a line
    of it doubles the peak for no gain. `loadToolpath` owns that, and the parser owns the
    only real limit, which is segments rather than bytes.
  */
  import { onMount, untrack } from 'svelte';
  import { formatDimensions, sizeOf } from '$lib/viewer/framing';
  import { loadToolpath } from '$lib/gcode/load';
  import { formatPrinter, resolvePrinter, toolpathColor } from '$lib/gcode/printer';
  import type { Toolpath } from '$lib/gcode/toolpath';
  import type { GcodeViewer } from '$lib/gcode/scene';
  import type { ModelFile } from '$lib/upload';

  let { modelId, file }: { modelId: number; file: ModelFile } = $props();

  type Status = 'loading' | 'ready' | 'failed';
  let status = $state<Status>('loading');
  let error = $state('');
  // Undefined while the response declares no length, which is what a chunked response
  // does. The bar goes indeterminate rather than showing a fraction of an unknown.
  let progress = $state<number | undefined>(undefined);

  let toolpath = $state<Toolpath>();
  let layer = $state(0);
  let showTravel = $state(false);

  let canvas = $state<HTMLCanvasElement>();
  let viewer: GcodeViewer | undefined;
  // A browser with WebGL disabled or unavailable, or one that has run out of GL
  // contexts. Without this the panel sits on "Loading preview…" forever, having thrown
  // into a promise nobody is watching.
  let viewerBroken = $state(false);
  const NO_VIEWER = 'This browser could not start the 3D preview.';

  // Both are needed. The abort stops a download nobody is waiting for; the counter stops
  // a *response* that already left the server from painting over a newer one. A rejected
  // fetch settles in its own microtask, so the failure path needs the same guard as the
  // success path or a slow 404 lands on top of a file that loaded fine.
  let inFlight: AbortController | undefined;
  let generation = 0;

  // A toolpath that finished parsing before the renderer module did. Without this the
  // first file on a cold cache parses into nothing and the panel sits blank.
  let pending: Toolpath | undefined;

  const meta = $derived(file.extractedMeta ?? undefined);
  const printer = $derived(resolvePrinter(meta));
  const color = $derived(toolpathColor(meta?.filamentColor));

  onMount(() => {
    // Imported here rather than at the top of the file so three.js is fetched when a
    // model page is opened, not when the app boots. It is by far the largest thing in
    // the bundle and every other screen manages without it.
    let cancelled = false;
    import('$lib/gcode/scene')
      .then((module) => {
        if (cancelled || !canvas) return;
        viewer = module.createViewer(canvas);
        viewer.setTravelVisible(showTravel);
        if (pending) {
          draw(pending);
          pending = undefined;
        }
      })
      .catch(() => {
        if (cancelled) return;
        viewerBroken = true;
        pending = undefined;
        if (status !== 'failed') {
          status = 'failed';
          error = NO_VIEWER;
        }
      });

    const onResize = () => viewer?.resize();
    window.addEventListener('resize', onResize);

    return () => {
      cancelled = true;
      window.removeEventListener('resize', onResize);
      inFlight?.abort();
      viewer?.dispose();
      viewer = undefined;
    };
  });

  function draw(next: Toolpath) {
    viewer?.show(next, { color, volume: printer.volume });
    viewer?.setLayer(layer);
  }

  async function load(current: ModelFile) {
    inFlight?.abort();
    const mine = ++generation;

    status = 'loading';
    error = '';
    progress = undefined;
    toolpath = undefined;

    const controller = new AbortController();
    inFlight = controller;
    try {
      const parsed = await loadToolpath(`/api/models/${modelId}/files/${current.id}`, {
        signal: controller.signal,
        onProgress: (fraction) => {
          if (mine === generation) progress = fraction;
        },
      });
      if (mine !== generation) return;
      // Parsed fine, but there is nothing to draw it with. Saying so beats a readout
      // sitting under a blank rectangle.
      if (viewerBroken) throw new Error(NO_VIEWER);

      toolpath = parsed;
      // Opens on the finished print rather than on layer one. The whole object is what
      // someone recognises; the slider is for taking it apart afterwards.
      layer = Math.max(parsed.layers.length - 1, 0);
      if (viewer) draw(parsed);
      else pending = parsed;
      status = 'ready';
    } catch (failure) {
      if (mine !== generation) return;
      if (failure instanceof DOMException && failure.name === 'AbortError') return;
      status = 'failed';
      error = failure instanceof Error ? failure.message : 'This file could not be read.';
      toolpath = undefined;
    }
  }

  // Keyed on the id, not on the file object. The page replaces `model` wholesale after
  // every mutation, so the object in `file` is new each time even when nothing about the
  // selected file changed - tracking the object would re-download the file every time
  // the user pinned a thumbnail.
  const fileId = $derived(file.id);

  $effect(() => {
    fileId;
    untrack(() => void load(file));
  });

  function scrub(next: number) {
    layer = next;
    viewer?.setLayer(next);
  }

  function toggleTravel() {
    showTravel = !showTravel;
    viewer?.setTravelVisible(showTravel);
  }

  const lastLayer = $derived(Math.max((toolpath?.layers.length ?? 1) - 1, 0));
  const currentZ = $derived(toolpath?.layers[Math.min(layer, lastLayer)]?.z);
  const dimensions = $derived(toolpath ? formatDimensions(sizeOf(toolpath.bounds)) : '');
</script>

<div data-testid="gcode-viewer">
  <div class="relative h-80">
    <!--
      Always mounted, so the GL context is created once and survives a switch between
      files. Hidden rather than removed while another state shows, because tearing the
      canvas down would take the context with it.
    -->
    <canvas
      bind:this={canvas}
      class="h-full w-full rounded-t-tile"
      class:invisible={status !== 'ready'}
      data-testid="gcode-canvas"
    ></canvas>

    {#if status !== 'ready'}
      <div class="absolute inset-0 flex flex-col items-center justify-center gap-2 px-6 text-center">
        {#if status === 'loading'}
          <p class="text-sm text-muted">Reading toolpaths…</p>
          <!--
            A determinate bar only once the response has declared a length. G-code is
            large enough that "how much longer" is a real question, unlike the mesh
            viewer where the download is the whole wait.
          -->
          <progress
            class="h-1 w-48"
            data-testid="gcode-progress"
            max="1"
            value={progress ?? undefined}
            aria-label="Reading toolpaths"
          ></progress>
        {:else}
          <p role="alert" class="max-w-md text-sm text-muted">{error}</p>
        {/if}
      </div>
    {/if}
  </div>

  {#if status === 'ready' && toolpath}
    <div class="flex flex-col gap-2 border-t border-line px-4 py-2.5">
      <div class="flex items-center gap-3">
        <!--
          A range input rather than a custom track: it comes with keyboard support, a
          value a screen reader reads out, and the browser's own touch handling, none of
          which a div would have. Only when there is more than one layer to walk - a
          slider with one stop is a control that cannot do anything.
        -->
        {#if lastLayer > 0}
          <input
            type="range"
            class="min-w-0 flex-1"
            min="0"
            max={lastLayer}
            step="1"
            value={layer}
            aria-label="Layer"
            data-testid="gcode-layer"
            oninput={(event) => scrub(event.currentTarget.valueAsNumber)}
          />
        {/if}
        <p class="shrink-0 text-xs tabular-nums text-muted" data-testid="gcode-layer-readout">
          Layer {layer + 1} of {lastLayer + 1}{currentZ === undefined
            ? ''
            : ` · Z ${currentZ.toFixed(2)} mm`}
        </p>
      </div>

      <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
        <button
          type="button"
          class="rounded border border-line-strong px-2 py-1 text-xs"
          class:font-bold={showTravel}
          aria-pressed={showTravel}
          onclick={toggleTravel}
        >
          Travel moves
        </button>
        <p class="text-xs text-muted" data-testid="gcode-readout">
          {dimensions} · {formatPrinter(printer)}
        </p>
      </div>
    </div>
  {/if}
</div>
