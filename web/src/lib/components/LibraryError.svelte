<script lang="ts">
  import { library } from '$lib/library.svelte';

  // A refresh that fails after a save leaves the screen showing a list the
  // server no longer has - a deleted row still there, a created one still
  // missing. Nothing else on either screen re-reads the taxonomy, so without
  // this button that stale list is what the user has until they reload the
  // page. The retry is the whole reason the failed refresh is allowed to keep
  // what it read.
  let { class: extra = '' }: { class?: string } = $props();
</script>

{#if library.error}
  <p role="alert" class="text-sm text-danger {extra}">
    {library.error}
    <button type="button" class="ml-1 underline" onclick={() => library.refresh()}>Try again</button>
  </p>
{/if}
