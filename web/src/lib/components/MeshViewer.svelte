<script lang="ts">
  /*
    Screen 1c's 3D preview.

    The panel owns three things: which file is showing, getting its bytes into a
    `ParsedMesh`, and the readout beneath. Everything three.js-shaped is behind
    `createViewer`, which needs a real GL context and so is exercised by using the
    app rather than by a test; everything this component decides is testable here.

    The whole file is downloaded - a mesh cannot be read through a window the way
    `internal/gcode` and `internal/thumb` read their files, because the triangles are
    the file. `MAX_PREVIEW_BYTES` is the point at which we say so instead of spending
    the user's bandwidth on a request the tab cannot survive.
  */
  import { onMount, untrack } from 'svelte';
  import {
    formatDimensions,
    formatObjectCount,
    boundsOf,
    sizeOf,
    type ParsedMesh,
  } from '$lib/mesh/geometry';
  import { MAX_PREVIEW_BYTES, parseMesh, previewable } from '$lib/mesh/parse';
  import type { Shading, Viewer } from '$lib/mesh/scene';
  import { formatBytes } from '$lib/format';
  import type { ModelFile } from '$lib/upload';

  let { modelId, files }: { modelId: number; files: ModelFile[] } = $props();

  const meshFiles = $derived(files.filter((file) => previewable(file.type)));

  let selectedId = $state<number>();
  const selected = $derived(
    meshFiles.find((file) => file.id === selectedId) ?? meshFiles[0],
  );

  type Status = 'empty' | 'too-large' | 'loading' | 'ready' | 'failed';
  let status = $state<Status>('empty');
  let error = $state('');
  let readout = $state('');
  let shading = $state<Shading>('solid');

  let canvas = $state<HTMLCanvasElement>();
  let viewer: Viewer | undefined;
  // A browser with WebGL disabled or unavailable, or one that has run out of GL
  // contexts. Reachable enough to handle: without this the panel sits on "Loading
  // preview…" forever, having thrown into a promise nobody is watching.
  let viewerBroken = $state(false);
  const NO_VIEWER = 'This browser could not start the 3D preview.';

  // Both are needed. The abort stops a download nobody is waiting for; the counter
  // stops a *response* that already left the server from painting over a newer one.
  // A rejected fetch settles in its own microtask, so the failure path needs the same
  // guard as the success path or a slow 404 lands on top of a mesh that loaded fine.
  let inFlight: AbortController | undefined;
  let generation = 0;

  onMount(() => {
    // Imported here rather than at the top of the file so three.js is fetched when a
    // model page is opened, not when the app boots. It is by far the largest thing in
    // the bundle and every other screen manages without it.
    let cancelled = false;
    import('$lib/mesh/scene')
      .then((module) => {
        if (cancelled || !canvas) return;
        viewer = module.createViewer(canvas);
        viewer.setShading(shading);
        if (pending) {
          viewer.show(pending);
          pending = undefined;
        }
      })
      .catch(() => {
        if (cancelled) return;
        viewerBroken = true;
        pending = undefined;
        if (status === 'loading' || status === 'ready') {
          status = 'failed';
          error = NO_VIEWER;
          readout = '';
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

  // A mesh that finished parsing before the viewer module did. Without this the first
  // file on a cold cache parses into nothing and the panel sits blank.
  let pending: ParsedMesh | undefined;

  async function load(file: ModelFile) {
    inFlight?.abort();
    const mine = ++generation;

    if (file.size > MAX_PREVIEW_BYTES) {
      // Refused before the request, not after: the point is not to download it.
      status = 'too-large';
      error = `This file is ${formatBytes(file.size)}. Files over ${formatBytes(
        MAX_PREVIEW_BYTES,
      )} are too large to preview - download it to open in a slicer.`;
      readout = '';
      return;
    }

    status = 'loading';
    error = '';
    readout = '';

    const controller = new AbortController();
    inFlight = controller;
    try {
      const response = await fetch(`/api/models/${modelId}/files/${file.id}`, {
        signal: controller.signal,
      });
      // fetch only rejects on a transport failure, so a 404 or a 500 arrives here as a
      // perfectly good response whose body is an error document. Parsing that as a mesh
      // gives a confusing "corrupt file" for a file that is simply not there.
      if (!response.ok) throw new Error('This file could not be loaded.');
      const buffer = await response.arrayBuffer();
      const mesh = parseMesh(file.type, buffer);
      if (mine !== generation) return;
      // Parsed fine, but there is nothing to draw it with. Saying so beats a readout
      // sitting under a blank rectangle.
      if (viewerBroken) throw new Error(NO_VIEWER);

      if (viewer) viewer.show(mesh);
      else pending = mesh;

      const size = sizeOf(boundsOf(mesh.positions));
      readout = `${formatDimensions(size)} · ${formatObjectCount(mesh.objectCount)}`;
      status = 'ready';
    } catch (failure) {
      if (mine !== generation) return;
      if (failure instanceof DOMException && failure.name === 'AbortError') return;
      status = 'failed';
      error = failure instanceof Error ? failure.message : 'This file could not be read.';
      readout = '';
    }
  }

  // Keyed on the id, not on the file object. The page replaces `model` wholesale after
  // every mutation, so the objects in `files` are new each time even when nothing about
  // the selected file changed - tracking the object would re-download the mesh every
  // time the user pinned a thumbnail.
  const selectedFileId = $derived(selected?.id);

  $effect(() => {
    const id = selectedFileId;
    untrack(() => {
      const file = meshFiles.find((candidate) => candidate.id === id);
      if (!file) {
        generation++;
        inFlight?.abort();
        status = 'empty';
        error = '';
        readout = '';
        return;
      }
      void load(file);
    });
  });

  function choose(next: Shading) {
    shading = next;
    viewer?.setShading(next);
  }

  const MODES: Array<{ value: Shading; label: string }> = [
    { value: 'solid', label: 'Solid' },
    { value: 'wireframe', label: 'Wireframe' },
    { value: 'xray', label: 'X-ray' },
  ];
</script>

<section class="rounded-tile border border-line bg-surface" data-testid="mesh-viewer">
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
      data-testid="mesh-canvas"
    ></canvas>

    {#if status !== 'ready'}
      <div class="absolute inset-0 flex items-center justify-center px-6 text-center">
        {#if status === 'loading'}
          <p class="text-sm text-muted">Loading preview…</p>
        {:else if status === 'empty'}
          <p class="text-sm text-muted">This model has no STL or 3MF file to preview.</p>
        {:else}
          <p role="alert" class="max-w-md text-sm text-muted">{error}</p>
        {/if}
      </div>
    {/if}
  </div>

  <div
    class="flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-line px-4 py-2.5"
  >
    <div class="flex gap-1" role="group" aria-label="Shading">
      {#each MODES as mode (mode.value)}
        <button
          type="button"
          class="rounded border border-line-strong px-2 py-1 text-xs"
          class:font-bold={shading === mode.value}
          aria-pressed={shading === mode.value}
          onclick={() => choose(mode.value)}
        >
          {mode.label}
        </button>
      {/each}
    </div>

    {#if readout}
      <p class="text-xs text-muted" data-testid="mesh-readout">{readout}</p>
    {/if}
  </div>

  {#if meshFiles.length > 1}
    <!--
      Only when there is a choice to make. A model with one mesh file gets no strip,
      because a control with a single option is a label pretending to be a button.
    -->
    <div class="flex flex-wrap gap-1 border-t border-line px-4 py-2">
      {#each meshFiles as file (file.id)}
        <button
          type="button"
          class="max-w-56 truncate rounded border border-line-strong px-2 py-1 text-xs"
          class:font-bold={selected?.id === file.id}
          aria-pressed={selected?.id === file.id}
          onclick={() => (selectedId = file.id)}
        >
          {file.filename}
        </button>
      {/each}
    </div>
  {/if}
</section>
