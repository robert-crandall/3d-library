<script lang="ts">
  import { formatBytes, formatFileCount } from '$lib/format';
  import Thumbnail from '$lib/components/Thumbnail.svelte';
  import CategoryBadge from '$lib/components/CategoryBadge.svelte';
  import type { Model } from '$lib/upload';

  let { model }: { model: Model } = $props();

  // The server resolved which file to show; the tile only builds the URL. A
  // client-side rule here would be a second copy of the precedence order, and
  // the grid and the detail screen would eventually disagree about which
  // picture a model has.
  let src = $derived(
    model.thumbnailFileId == null
      ? null
      : `/api/models/${model.id}/files/${model.thumbnailFileId}/thumbnail`,
  );
</script>

<!--
  One tile in the grid. Name, file count, total size, and the thumbnail the
  server picked - an image the user uploaded, or the render the slicer embedded
  in a 3MF or a G-code file. A model with none of those keeps the hatched
  placeholder, which is honest rather than broken-looking: nothing in this app
  rasterises an STL, so plenty of models legitimately have no picture.

  The whole tile is the link, not just the name: the thumbnail is the biggest
  thing on it and the obvious thing to click, and a 42px-tall target beats a
  one-line one on a phone.
-->
<a
  class="block overflow-hidden rounded-tile border border-line bg-surface"
  href="/models/{model.id}"
>
  <div class="h-42 border-b border-line">
    <!-- alt="" and not the model name: the name is rendered right below, so
         reading it twice is noise to a screen reader. -->
    <Thumbnail {src} />
  </div>
  <div class="px-3.5 py-3">
    <h2 class="truncate text-sm font-medium" title={model.name}>{model.name}</h2>
    <!-- The category and the size share a row, as in the design's grid. The
         badge renders nothing when the model is uncategorized, which leaves
         the size where it already was rather than a placeholder word. -->
    <div class="mt-1 flex items-center gap-2 text-xs text-muted">
      <CategoryBadge category={model.category} size="sm" />
      <span class="ml-auto shrink-0">
        {formatFileCount(model.fileCount)} · {formatBytes(model.totalSize)}
      </span>
    </div>
  </div>
</a>
