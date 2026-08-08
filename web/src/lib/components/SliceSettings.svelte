<script lang="ts">
  import { detectedSlicer, fieldCount, sliceRows, type SliceMeta } from '$lib/slice';

  let { meta, filename }: { meta: SliceMeta; filename: string } = $props();

  const rows = $derived(sliceRows(meta));
  const fields = $derived(fieldCount(meta));
</script>

<!--
  Slice settings, screen 1c of the design.

  The panel is only ever rendered for a file the parser attributed to a slicer,
  so there is no empty state here: the caller decides whether there is anything
  to show, and an absent panel is the answer when there is not.

  Two deliberate changes from the design's footer, which reads
  "Detected slicer: OrcaSlicer 2.1.1 · all 24 fields" with the count as a link.

  The count is how many settings this file gave up, not a fixed 24. Every slicer
  writes a different amount - Cura yields three fields where PrusaSlicer yields
  sixteen - so a constant would be wrong for five of the six real files this is
  tested against. It counts settings and not rows, because several rows carry two
  or three of them; all of them are on the panel either way.

  And it is text, not a link. In the design that link points at 1c, the screen it
  is already on, which is how the mockup marks a target that does not exist yet.
  Every field we have is already on this panel, so there is nothing for it to
  open.
-->
<section class="overflow-hidden rounded-tile border border-line bg-surface">
  <header class="flex items-center gap-3 border-b border-line px-4 py-3">
    <h2 class="text-sm font-semibold">Slice settings</h2>
    <span class="ml-auto truncate font-mono text-xs text-faint" title={filename}>
      from {filename}
    </span>
  </header>

  <dl class="px-4 pt-1 pb-3">
    {#each rows as row (row.label)}
      <div class="flex items-baseline gap-3 border-b border-line/60 py-1.5 last:border-b-0">
        <dt class="w-32 flex-none text-xs text-muted">{row.label}</dt>
        <dd class="font-mono text-xs break-all text-ink">{row.value}</dd>
      </div>
    {/each}
  </dl>

  <!-- Outside the list: a dl may only contain dt, dd and div, and the footer is
       about the list rather than an entry in it. -->
  <p class="px-4 pb-3 text-xs text-faint">
    Detected slicer: {detectedSlicer(meta)} · {fields}
    {fields === 1 ? 'field' : 'fields'}
  </p>
</section>
