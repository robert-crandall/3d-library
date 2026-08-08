<script lang="ts">
  let {
    labels,
    href
  }: {
    labels: { id: number; name: string }[];
    /** Given, the chips are links. Tags have somewhere to go (the filtered
     *  grid); materials do not, so they stay plain text rather than becoming
     *  links that lead nowhere. */
    href?: (label: { id: number; name: string }) => string;
  } = $props();
</script>

<!--
  A model's tags or its materials, as chips.

  No heading of its own: the two callers sit under different headings in the
  same panel, and a component that owns the heading cannot be reused for both
  without a prop that only exists to be a string.
-->
{#if labels.length > 0}
  <ul class="flex flex-wrap gap-1.5">
    {#each labels as item (item.id)}
      <li>
        {#if href}
          <a class="inline-block rounded bg-chip px-2 py-0.5 text-xs text-muted" href={href(item)}>
            {item.name}
          </a>
        {:else}
          <span class="inline-block rounded bg-chip px-2 py-0.5 text-xs text-muted">
            {item.name}
          </span>
        {/if}
      </li>
    {/each}
  </ul>
{/if}
