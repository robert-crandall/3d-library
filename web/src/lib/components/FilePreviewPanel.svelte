<script lang="ts">
  /*
    Screen 1c's preview panel.

    This component owns which file is showing and nothing about how it is drawn. The two
    viewers below it each own one file type end to end - their own size cap, their own
    failure messages, their own three.js module - because a mesh and a toolpath have
    almost nothing in common past "there is a canvas". The one thing they must share is
    the strip, since the strip is how you get from one to the other.

    Swapping between the two unmounts one viewer and mounts the other, which does mean a
    new GL context. That is fine: `dispose` calls `forceContextLoss`, so the old one is
    released rather than left to age out, and switching between two files of the same
    kind keeps the same component and so the same context.
  */
  import GcodeViewer from './GcodeViewer.svelte';
  import MeshViewer from './MeshViewer.svelte';
  import { defaultFile, previewKind } from '$lib/preview';
  import type { ModelFile } from '$lib/upload';

  let { modelId, files }: { modelId: number; files: ModelFile[] } = $props();

  let selectedId = $state<number>();
  // Searched across every file, not just the previewable ones: the strip lists all of
  // them, and picking a .jpg has to be a selection that sticks rather than one that
  // silently snaps back to the mesh.
  const selected = $derived(files.find((file) => file.id === selectedId) ?? defaultFile(files));
  const kind = $derived(selected ? previewKind(selected) : undefined);
</script>

<section class="rounded-tile border border-line bg-surface" data-testid="preview-panel">
  {#if selected && kind === 'mesh'}
    <!--
      Not keyed on the file. Staying inside one branch keeps the same component instance
      across a change of file, which is what keeps its GL context alive; the viewer
      reloads from the prop instead.
    -->
    <MeshViewer {modelId} file={selected} />
  {:else if selected && kind === 'gcode'}
    <GcodeViewer {modelId} file={selected} />
  {:else}
    <div class="flex h-80 items-center justify-center px-6 text-center">
      <p role="status" class="max-w-md text-sm text-muted">
        There is no preview for {selected?.filename}. The viewer shows STL and 3MF meshes and
        G-code toolpaths; download it to open it in something that reads this.
      </p>
    </div>
  {/if}

  {#if files.length > 1}
    <!--
      Every file, not just the previewable ones, matching design 1c's strip - it lists a
      .gcode and a .jpg alongside the meshes. Clicking one of those is a real selection
      that lands on the "no preview" state, which is the point: a file the viewer cannot
      draw has to say so, not be missing from the strip and leave the user wondering
      where it went. Only when there is a choice to make, though - a control with a
      single option is a label pretending to be a button.
    -->
    <div class="flex flex-wrap gap-1 border-t border-line px-4 py-2">
      {#each files as file (file.id)}
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
