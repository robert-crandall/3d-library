<script lang="ts">
  /*
    One thumbnail, or the hatched placeholder.

    Both screens draw the same two states, so the fallback lives here rather
    than twice: the grid tile and the file list would otherwise drift, and the
    hatch is the thing that says "this app has no picture of this" in both.

    `failed` is keyed on the URL through the $effect below. Without that, an
    <img> that once errored stays failed after the model is pinned to a
    different file - the component is reused, the src changes, and the flag
    would never re-arm.
  */
  let {
    src,
    alt = '',
    class: className = '',
  }: { src: string | null; alt?: string; class?: string } = $props();

  let failed = $state(false);
  let shown = $state<string | null>(null);

  $effect(() => {
    if (src !== shown) {
      shown = src;
      failed = false;
    }
  });
</script>

{#if src && !failed}
  <img
    {src}
    {alt}
    class="h-full w-full object-cover {className}"
    loading="lazy"
    decoding="async"
    onerror={() => (failed = true)}
  />
{:else}
  <!--
    The hatch is inline rather than a class because it references a palette
    variable, and Tailwind has no utility for a repeating gradient. It is
    aria-hidden: it carries no information the name beside it does not.
  -->
  <div
    class="h-full w-full {className}"
    style="background-image: repeating-linear-gradient(45deg, var(--color-line) 0 1px, transparent 1px 9px)"
    aria-hidden="true"
    data-testid="thumbnail-placeholder"
  ></div>
{/if}
