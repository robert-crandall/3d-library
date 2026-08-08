<script lang="ts">
  import { formatBytes, formatFileCount } from '$lib/format';
  import type { Model } from '$lib/upload';

  let { model }: { model: Model } = $props();
</script>

<!--
  One tile in the grid. Name, file count, total size - the three facts the
  milestone asks for.

  The thumbnail is a hatched placeholder for every model. Rendering a real
  preview means parsing an STL in the browser, which is a later milestone; a
  uniform placeholder is honest about that, where showing the first image file
  for some models and nothing for others would read as broken.

  Not a link yet. The model detail screen is milestone 2, and a link to a route
  that does not exist is worse than a tile that does not move.
-->
<article class="overflow-hidden rounded-tile border border-line bg-surface">
  <div
    class="h-42 border-b border-line"
    style="background-image: repeating-linear-gradient(45deg, var(--color-line) 0 1px, transparent 1px 9px)"
    aria-hidden="true"
  ></div>
  <div class="px-3.5 py-3">
    <h2 class="truncate text-sm font-medium" title={model.name}>{model.name}</h2>
    <p class="mt-1 text-xs text-muted">
      {formatFileCount(model.fileCount)} · {formatBytes(model.totalSize)}
    </p>
  </div>
</article>
