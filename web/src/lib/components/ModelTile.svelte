<script lang="ts">
  import { formatBytes, formatFileCount } from '$lib/format';
  import Thumbnail from '$lib/components/Thumbnail.svelte';
  import CategoryBadge from '$lib/components/CategoryBadge.svelte';
  import type { Model } from '$lib/upload';

  let {
    model,
    selected = false,
    onselect
  }: {
    model: Model;
    selected?: boolean;
    /** Absent where there is no selection to join - the tile is then a plain
     *  link, which is what it was before bulk actions existed. */
    onselect?: (mode: 'toggle' | 'range') => void;
  } = $props();

  // Whether the context menu already dealt with this gesture. Reset on
  // mousedown, which precedes both of the events below on every platform, so a
  // browser that fires contextmenu *and* click toggles once and a browser that
  // fires only one of them also toggles once.
  let handled = false;

  // macOS turns Ctrl-click into a context menu rather than a click, so a
  // ctrl-only click handler silently does nothing there - and the brief names
  // Ctrl-click. Cmd counts as well because it is the native macOS multi-select
  // modifier and it does arrive as a click.
  function contextmenu(event: MouseEvent) {
    if (!onselect || !event.ctrlKey) return;
    event.preventDefault();
    handled = true;
    onselect('toggle');
  }

  function click(event: MouseEvent) {
    if (!onselect) return;
    const toggle = event.ctrlKey || event.metaKey;
    if (!toggle && !event.shiftKey) return;
    // Without this a modified click on a link opens a new tab or a new window
    // instead of selecting anything.
    event.preventDefault();
    if (handled) return;
    onselect(event.shiftKey ? 'range' : 'toggle');
  }

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
  one-line one on a phone. Ctrl/Cmd-click and Shift-click select instead of
  following it; a plain click is untouched.
-->
<a
  class="block overflow-hidden rounded-tile border bg-surface"
  class:border-line={!selected}
  class:border-accent={selected}
  class:bg-selected={selected}
  href="/models/{model.id}"
  onmousedown={() => (handled = false)}
  oncontextmenu={contextmenu}
  onclick={click}
>
  <div class="h-42 border-b border-line">
    <!-- alt="" and not the model name: the name is rendered right below, so
         reading it twice is noise to a screen reader. -->
    <Thumbnail {src} />
  </div>
  <div class="px-3.5 py-3">
    <h2 class="truncate text-sm font-medium" title={model.name}>
      {model.name}<!--
        The selected state is a colour, which a screen reader cannot see, and an
        <a> cannot carry aria-selected without being inside a listbox. The
        accessible name says it instead. The leading comma is load-bearing:
        accessible-name computation trims each text node, so a leading space
        would be dropped and the two would run together.
      -->{#if selected}<span class="sr-only">, selected</span>{/if}
    </h2>
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
