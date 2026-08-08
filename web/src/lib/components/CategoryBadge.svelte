<script lang="ts">
  import type { Category } from '$lib/library.svelte';

  let {
    category,
    size = 'md'
  }: {
    /** null renders nothing at all rather than an "Uncategorized" chip: an
     *  uncategorized model is the common case, and a badge on every one of
     *  them is noise. */
    category: Pick<Category, 'name' | 'color'> | null | undefined;
    size?: 'sm' | 'md';
  } = $props();
</script>

<!--
  A category, as a coloured square and a name.

  The colour is per-user data, so it rides in an inline style rather than a
  class: there is no fixed set of them to name in app.css, and D5's rule is
  about the house palette, not about data. The value is validated server-side
  against ^#[0-9a-fA-F]{6}$ and again in the service, so nothing but a colour
  can reach this attribute.
-->
{#if category}
  {#if size === 'sm'}
    <span class="flex min-w-0 items-center gap-1.5 text-xs text-muted">
      <span
        class="h-1.5 w-1.5 shrink-0 rounded-xs"
        style="background-color: {category.color}"
        aria-hidden="true"
      ></span>
      <span class="truncate">{category.name}</span>
    </span>
  {:else}
    <span
      class="inline-flex items-center gap-1.5 rounded bg-chip px-2 py-0.5 text-xs font-medium"
    >
      <span
        class="h-1.5 w-1.5 shrink-0 rounded-xs"
        style="background-color: {category.color}"
        aria-hidden="true"
      ></span>
      {category.name}
    </span>
  {/if}
{/if}
